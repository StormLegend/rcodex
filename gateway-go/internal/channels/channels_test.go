package channels

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func TestTelegramWebhook(t *testing.T) {
	var got Message
	c := config.Channel{Secret: "telegram-secret-012345678901234567890", Users: []string{"7"}, Chats: []string{"9"}}
	h := Telegram(func(_ context.Context, m Message) error { got = m; return nil }, c)
	body := `{"update_id":12,"message":{"chat":{"id":9},"from":{"id":7},"text":"hello"}}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", c.Secret)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || got.Channel != "telegram" || got.Text != "hello" {
		t.Fatalf("code=%d got=%+v", w.Code, got)
	}
}

func TestDiscordPingAndSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	c := config.Channel{PublicKey: hex.EncodeToString(pub), Users: []string{"7"}, Chats: []string{"9"}}
	body := `{"type":1}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(priv, append([]byte(ts), []byte(body)...))
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-Signature-Timestamp", ts)
	r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	w := httptest.NewRecorder()
	Discord(func(context.Context, Message) error { t.Fatal("ping dispatched"); return nil }, c).ServeHTTP(w, r)
	var v map[string]int
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || v["type"] != 1 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestFeishuChallenge(t *testing.T) {
	c := config.Channel{Secret: "verify"}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"challenge":"abc","token":"verify"}`))
	w := httptest.NewRecorder()
	Feishu(func(context.Context, Message) error { return nil }, c).ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "abc") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

// This vector was generated independently with OpenSSL AES-256-CBC / PKCS#7,
// SHA256("encrypt-key") and IV 000102030405060708090a0b0c0d0e0f. Feishu's
// official SDK serializes base64(IV || ciphertext), not a key-derived IV.
const feishuEventVector = "AAECAwQFBgcICQoLDA0ODz9Lxj77uokvUqazLE8ih8IIPoJ0qU041Slle81CkvPIRtvPcVFU0+HfpPUQWyg6EidZp+t/H03wXxvnzAs664wPRYE2fqSeNnOgQz2C8vbaFsxBTjdtg2kwNyKHMe9jHrok4Mhadu5/Ekthl3PiHtNwy3XjRPIZubMhKgTb6yc51r9QoPnV511sjT5qCp7bhUQLicizdRXTbbXqW/L0Zq9AL6AzMe8Su/biogVNLcK6pNpRjLzeYgbzBsh1UhtjT+op+SnIbVL8yoWyHYx1pFEWAaCillduTobNd+8Q0F41"

func feishuRequest(body, key string) *http.Request {
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	ts, nonce := strconv.FormatInt(time.Now().Unix(), 10), "fixture-nonce"
	sum := sha256.Sum256([]byte(ts + nonce + key + body))
	r.Header.Set("X-Lark-Request-Timestamp", ts)
	r.Header.Set("X-Lark-Request-Nonce", nonce)
	r.Header.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
	return r
}

func TestFeishuEncryptedEvent(t *testing.T) {
	c := config.Channel{Secret: "verify", EncryptKey: "encrypt-key", Users: []string{"u1"}, Chats: []string{"c1"}}
	body := `{"encrypt":"` + feishuEventVector + `"}`
	for _, scenario := range []string{"valid", "missing signature", "bad signature", "stale", "wrong token", "plaintext"} {
		t.Run(scenario, func(t *testing.T) {
			var got Message
			cfg := c
			r := feishuRequest(body, c.EncryptKey)
			want := http.StatusUnauthorized
			switch scenario {
			case "valid":
				want = http.StatusNoContent
			case "missing signature":
				r.Header.Del("X-Lark-Signature")
			case "bad signature":
				r.Header.Set("X-Lark-Signature", strings.Repeat("0", 64))
			case "stale":
				ts := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
				r.Header.Set("X-Lark-Request-Timestamp", ts)
				sum := sha256.Sum256([]byte(ts + "fixture-nonce" + c.EncryptKey + body))
				r.Header.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
			case "wrong token":
				cfg.Secret = "different"
			case "plaintext":
				r = feishuRequest(`{"event":{"message":{"chat_id":"c1","message_type":"text","content":"{\"text\":\"hello\"}"},"sender":{"sender_id":{"open_id":"u1"}}},"header":{"event_id":"e1","event_type":"im.message.receive_v1","token":"verify"}}`, c.EncryptKey)
			}
			w := httptest.NewRecorder()
			Feishu(func(_ context.Context, m Message) error { got = m; return nil }, cfg).ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body)
			}
			if scenario == "valid" {
				if got.Text != "hello" || got.ID != "e1" {
					t.Fatalf("message=%+v", got)
				}
			} else if got.ID != "" {
				t.Fatalf("unauthorized event dispatched: %+v", got)
			}
		})
	}
}

func TestDurableDeliveryProviderProtocolsWithLocalBase(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "tenant_access_token/internal") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"tenant-test"}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/bot") {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()
	base := srv.URL
	c := config.Config{Channels: map[string]config.Channel{
		"telegram": {Token: "telegram-token", APIBaseURL: base},
		"discord":  {AppID: "app", APIBaseURL: base},
		"feishu":   {AppID: "app", AppSecret: "secret", APIBaseURL: base},
	}}
	ctx := context.Background()
	if err := DeliverWithClient(ctx, store.Delivery{Destination: store.Notification{Channel: "telegram", Chat: "42"}, Text: "tg"}, c, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if err := DeliverWithClient(ctx, store.Delivery{Destination: store.Notification{Channel: "discord", Chat: "42", Token: "interaction"}, Text: "dc"}, c, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if err := DeliverWithClient(ctx, store.Delivery{Destination: store.Notification{Channel: "feishu", Chat: "42"}, Text: "fs"}, c, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("provider requests=%v", paths)
	}
	if paths[0] != "/bottelegram-token/sendMessage" {
		t.Fatalf("telegram path=%v", paths[0])
	}
	if paths[1] != "/api/v10/webhooks/app/interaction" {
		t.Fatalf("discord path=%v", paths[1])
	}
	if paths[2] != "/open-apis/auth/v3/tenant_access_token/internal" || paths[3] != "/open-apis/im/v1/messages" {
		t.Fatalf("feishu paths=%v", paths[2:])
	}
}
