package web

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/engine"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

// A network listener, SQLite, engine, CLI subprocess, outbox worker and database
// reopen are real. Only the external bot APIs and model output are fixtures.
func TestChannelsIngressRuntimeOutboxRestart(t *testing.T) {
	for _, channel := range []string{"telegram", "discord", "feishu"} {
		t.Run(channel, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			command := filepath.Join(root, "claude-fixture")
			if err := os.WriteFile(command, []byte("#!/bin/sh\nread line\nprintf '%s\\n' '{\"type\":\"result\",\"is_error\":false,\"result\":\"acceptance reply\"}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var deliveries atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "tenant_access_token/internal") {
					io.WriteString(w, `{"code":0,"tenant_access_token":"test-tenant"}`)
					return
				}
				b, err := io.ReadAll(r.Body)
				if err != nil || !strings.Contains(string(b), "acceptance reply") {
					t.Errorf("reply missing: %s error=%v", b, err)
				}
				switch channel {
				case "telegram":
					if r.URL.Path != "/bottest-bot/sendMessage" {
						t.Errorf("telegram path=%s", r.URL.Path)
					}
				case "discord":
					if r.URL.Path != "/api/v10/webhooks/test-app/test-interaction" {
						t.Errorf("discord path=%s", r.URL.Path)
					}
				case "feishu":
					if r.Header.Get("Authorization") != "Bearer test-tenant" || r.URL.Query().Get("receive_id_type") != "chat_id" {
						t.Error("feishu destination/auth missing")
					}
				}
				if deliveries.Add(1) == 1 {
					// HTTP 200 is not enough to acknowledge Telegram/Feishu delivery.
					switch channel {
					case "telegram":
						io.WriteString(w, `{"ok":false,"error_code":429}`)
					case "feishu":
						io.WriteString(w, `{"code":99991400}`)
					default:
						w.WriteHeader(http.StatusServiceUnavailable)
					}
					return
				}
				switch channel {
				case "telegram":
					io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
				case "feishu":
					io.WriteString(w, `{"code":0,"data":{"message_id":"42"}}`)
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer provider.Close()
			pub, priv, err := ed25519.GenerateKey(nil)
			if err != nil {
				t.Fatal(err)
			}
			ch := config.Channel{Secret: "test-verification-secret", EncryptKey: "test-encrypt-key", Token: "test-bot", AppID: "test-app", AppSecret: "test-app-secret", PublicKey: hex.EncodeToString(pub), Users: []string{"7"}, Chats: []string{"9"}, Runtime: "claude", Workspace: root, Mode: "readonly", APIBaseURL: provider.URL}
			cfg := config.Config{DataDir: root, Token: strings.Repeat("t", 32), Workers: 1, TurnSeconds: 5, QueueLimit: 10, ClaudeCommand: command, Channels: map[string]config.Channel{channel: ch}}
			start := func() (*Server, *httptest.Server, func(), func()) {
				s, err := store.Open(filepath.Join(root, "gateway.db"))
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Recover(); err != nil {
					t.Fatal(err)
				}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				e := engine.New(s, cfg, log)
				g := &Server{Cfg: cfg, Store: s, Engine: e, Log: log}
				httpServer := httptest.NewServer(g.Handler())
				e.Start(context.Background())
				ctx, cancel := context.WithCancel(context.Background())
				var workers sync.WaitGroup
				runDelivery := func() { workers.Add(1); go func() { defer workers.Done(); g.deliver(ctx) }() }
				var once sync.Once
				stop := func() {
					once.Do(func() {
						httpServer.Close()
						cancel()
						workers.Wait()
						e.Stop()
						if err := s.DB.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				t.Cleanup(stop)
				return g, httpServer, runDelivery, stop
			}
			gateway, httpServer, runDelivery, stop := start()
			send := func(valid bool) {
				body, headers := channelFixture(t, channel, ch, priv)
				if !valid {
					headers = http.Header{}
				}
				r, err := http.NewRequest(http.MethodPost, httpServer.URL+"/webhooks/"+channel, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				r.Header = headers
				resp, err := httpServer.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if valid && (resp.StatusCode < 200 || resp.StatusCode >= 300) || !valid && resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("valid=%v ingress status=%d", valid, resp.StatusCode)
				}
			}
			send(false)
			if stats := gateway.Store.Stats(); stats["turns"] != 0 || stats["sessions"] != 0 {
				t.Fatalf("unauthenticated request changed state: %v", stats)
			}
			send(true)
			send(true)
			awaitAcceptance(t, func() bool { return gateway.Store.Stats()["outbox"] == 1 })
			if stats := gateway.Store.Stats(); stats["turns"] != 1 || stats["sessions"] != 1 {
				t.Fatalf("duplicate executed: %v", stats)
			}
			var turnID, sessionID, state, result, nativeID string
			if err := gateway.Store.DB.QueryRow("SELECT t.id,t.session_id,t.state,t.result,s.native_id FROM turns t JOIN sessions s ON s.id=t.session_id").Scan(&turnID, &sessionID, &state, &result, &nativeID); err != nil {
				t.Fatal(err)
			}
			if state != "completed" || result != "acceptance reply" || nativeID == "" {
				t.Fatalf("turn=%s result=%q native=%q", state, result, nativeID)
			}
			runDelivery()
			awaitAcceptance(t, func() bool { return outboxMatches(gateway.Store, turnID, "queued", 1) })
			var nextAt int64
			if err := gateway.Store.DB.QueryRow("SELECT next_at FROM outbox WHERE id=?", turnID).Scan(&nextAt); err != nil {
				t.Fatal(err)
			}
			if nextAt <= store.Now() {
				t.Fatal("failed delivery has no backoff")
			}
			stop()
			gateway, httpServer, runDelivery, stop = start()
			defer stop()
			if !outboxMatches(gateway.Store, turnID, "queued", 1) {
				t.Fatal("retry state lost on reopen")
			}
			session, err := gateway.Store.Session(sessionID)
			if err != nil || session.NativeID != nativeID {
				t.Fatalf("native ID lost: %+v %v", session, err)
			}
			send(true)
			if stats := gateway.Store.Stats(); stats["turns"] != 1 || stats["outbox"] != 1 {
				t.Fatalf("duplicate after restart: %v", stats)
			}
			runDelivery()
			awaitAcceptance(t, func() bool { return outboxMatches(gateway.Store, turnID, "sent", 2) })
			if deliveries.Load() != 2 {
				t.Fatalf("provider attempts=%d", deliveries.Load())
			}
			if pending, err := gateway.Store.Deliveries(); err != nil || len(pending) != 0 {
				t.Fatalf("acknowledged item still pending: %v %v", pending, err)
			}
		})
	}
}

func awaitAcceptance(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("acceptance condition timed out")
}

func outboxMatches(s *store.Store, id, state string, attempts int) bool {
	var got string
	var n int
	return s.DB.QueryRow("SELECT state,attempts FROM outbox WHERE id=?", id).Scan(&got, &n) == nil && got == state && n == attempts
}

func TestOutboxStopsAfterEightFailedAttempts(t *testing.T) {
	s, root := testServer(t)
	var attempts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		io.WriteString(w, `{"ok":false,"error_code":403}`)
	}))
	defer provider.Close()
	s.Cfg.Channels = map[string]config.Channel{"telegram": {Token: "fixture", APIBaseURL: provider.URL}}
	s.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	session, err := s.Store.CreateSession(store.Session{Runtime: "claude", Workspace: root, Mode: "readonly"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Store.Enqueue(session.ID, "hello", "fixture", `{"channel":"telegram","chat":"9"}`, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.Claim(); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Finish(turn, "fixture answer", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.deliver(ctx) }()
	defer func() { cancel(); <-done }()
	for n := 1; n <= 8; n++ {
		// Advance only the persisted due time; exercise the real delivery worker
		// and attempt cap without waiting through exponential backoff in CI.
		if _, err = s.Store.DB.Exec("UPDATE outbox SET next_at=0 WHERE id=?", turn.ID); err != nil {
			t.Fatal(err)
		}
		want := "queued"
		if n == 8 {
			want = "dead"
		}
		awaitAcceptance(t, func() bool { return outboxMatches(s.Store, turn.ID, want, n) })
	}
	if pending, err := s.Store.Deliveries(); err != nil || len(pending) != 0 {
		t.Fatalf("dead delivery selected again: %v %v", pending, err)
	}
	if attempts.Load() != 8 {
		t.Fatalf("provider calls=%d", attempts.Load())
	}
	if stats := s.Store.Stats(); stats["outbox_dead"] != 1 || stats["outbox_queued"] != 0 {
		t.Fatalf("dead-letter metrics=%v", stats)
	}
}

func channelFixture(t *testing.T, channel string, ch config.Channel, priv ed25519.PrivateKey) (string, http.Header) {
	t.Helper()
	headers := http.Header{}
	switch channel {
	case "telegram":
		headers.Set("X-Telegram-Bot-Api-Secret-Token", ch.Secret)
		return `{"update_id":123,"message":{"chat":{"id":9},"from":{"id":7},"text":"hello"}}`, headers
	case "discord":
		body := `{"type":2,"id":"123","token":"test-interaction","channel_id":"9","member":{"user":{"id":"7"}},"data":{"name":"ask","options":[{"value":"hello"}]}}`
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		headers.Set("X-Signature-Timestamp", ts)
		headers.Set("X-Signature-Ed25519", hex.EncodeToString(ed25519.Sign(priv, []byte(ts+body))))
		return body, headers
	default:
		plain := []byte(`{"header":{"event_id":"123","token":"test-verification-secret","event_type":"im.message.receive_v1"},"event":{"sender":{"sender_id":{"open_id":"7"}},"message":{"chat_id":"9","message_type":"text","content":"{\"text\":\"hello\"}"}}}`)
		key := sha256.Sum256([]byte(ch.EncryptKey))
		block, err := aes.NewCipher(key[:])
		if err != nil {
			t.Fatal(err)
		}
		pad := aes.BlockSize - len(plain)%aes.BlockSize
		plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
		data := make([]byte, aes.BlockSize+len(plain))
		if _, err := rand.Read(data[:aes.BlockSize]); err != nil {
			t.Fatal(err)
		}
		cipher.NewCBCEncrypter(block, data[:aes.BlockSize]).CryptBlocks(data[aes.BlockSize:], plain)
		body, err := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(data)})
		if err != nil {
			t.Fatal(err)
		}
		ts, nonce := strconv.FormatInt(time.Now().Unix(), 10), "acceptance-nonce"
		sum := sha256.Sum256(append([]byte(ts+nonce+ch.EncryptKey), body...))
		headers.Set("X-Lark-Request-Timestamp", ts)
		headers.Set("X-Lark-Request-Nonce", nonce)
		headers.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
		return string(body), headers
	}
}
