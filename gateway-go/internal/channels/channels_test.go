package channels

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
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
