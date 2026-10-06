package channels

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func TestProviderFailuresCannotAcknowledgeOutbox(t *testing.T) {
	for _, tc := range []struct {
		name, provider, body string
		status               int
	}{
		{"telegram business error", "telegram", `{"ok":false,"description":"fixture-secret"}`, 200},
		{"telegram missing status", "telegram", `{}`, 200},
		{"telegram empty response", "telegram", ``, 200},
		{"telegram invalid json", "telegram", `{"ok":true}garbage`, 200},
		{"feishu business error", "feishu", `{"code":999,"msg":"fixture-secret"}`, 200},
		{"feishu missing status", "feishu", `{}`, 200},
		{"discord HTTP error", "discord", `fixture-secret`, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					io.WriteString(w, `{"code":0,"tenant_access_token":"fixture-secret"}`)
					return
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c := config.Config{Channels: map[string]config.Channel{tc.provider: {APIBaseURL: srv.URL, Token: "fixture-secret", AppID: "app"}}}
			err := Deliver(context.Background(), store.Delivery{Destination: store.Notification{Channel: tc.provider, Chat: "9", Token: "fixture-secret"}, Text: "hello"}, c)
			if err == nil || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("error must reject delivery without exposing credentials: %v", err)
			}
		})
	}
}

func TestProviderRedirectAndNetworkErrorsDoNotLeakSecrets(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	c := config.Config{Channels: map[string]config.Channel{"telegram": {Token: "fixture-secret", APIBaseURL: srv.URL}}}
	d := store.Delivery{Destination: store.Notification{Channel: "telegram", Chat: "9"}, Text: "hello"}
	if err := Deliver(context.Background(), d, c); err == nil || called {
		t.Fatalf("redirect followed=%v error=%v", called, err)
	}
	srv.Close()
	if err := Deliver(context.Background(), d, c); err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("network error exposes token: %v", err)
	}
}

func TestDiscordDirectMessageIdentityAndDisabledMentions(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := config.Channel{PublicKey: hex.EncodeToString(pub), Users: []string{"7"}, Chats: []string{"9"}}
	body := `{"type":2,"id":"interaction","token":"token","channel_id":"9","user":{"id":"7"},"data":{"name":"ask","options":[{"value":"hello"}]}}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r.Header.Set("X-Signature-Timestamp", ts)
	r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(ed25519.Sign(priv, []byte(ts+body))))
	w := httptest.NewRecorder()
	var got Message
	Discord(func(_ context.Context, m Message) error { got = m; return nil }, c).ServeHTTP(w, r)
	if w.Code != 200 || got.User != "7" || got.Text != "ask hello" {
		t.Fatalf("status=%d message=%+v", w.Code, got)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"allowed_mentions":{"parse":[]}`) {
			t.Errorf("mentions not disabled: %s", b)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := config.Config{Channels: map[string]config.Channel{"discord": {APIBaseURL: srv.URL, AppID: "app"}}}
	if err := Deliver(context.Background(), store.Delivery{Destination: store.Notification{Channel: "discord", Token: "token"}, Text: "@everyone"}, cfg); err != nil {
		t.Fatal(err)
	}
}
