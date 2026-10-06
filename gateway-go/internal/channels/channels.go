package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Channel, Chat, User, Text string
	ID, Token                 string
}
type Handler func(context.Context, Message) error

func Telegram(h Handler, c config.Channel) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		if !config.Equal(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), c.Secret) {
			http.Error(w, "unauthorized", 401)
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if e != nil {
			http.Error(w, "body", 400)
			return
		}
		var v struct {
			UpdateID int64 `json:"update_id"`
			Message  struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
				From struct {
					ID int64 `json:"id"`
				} `json:"from"`
				Text string `json:"text"`
			} `json:"message"`
		}
		if json.Unmarshal(b, &v) != nil {
			http.Error(w, "json", 400)
			return
		}
		if strings.TrimSpace(v.Message.Text) == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !allowed(strconv.FormatInt(v.Message.Chat.ID, 10), c.Chats) || !allowed(strconv.FormatInt(v.Message.From.ID, 10), c.Users) {
			http.Error(w, "forbidden", 403)
			return
		}
		if e = h(r.Context(), Message{"telegram", strconv.FormatInt(v.Message.Chat.ID, 10), strconv.FormatInt(v.Message.From.ID, 10), v.Message.Text, strconv.FormatInt(v.UpdateID, 10), ""}); e != nil {
			http.Error(w, "failed", 500)
			return
		}
		w.WriteHeader(204)
	})
}

func decryptFeishu(encoded, encryptKey string) ([]byte, error) {
	keyHash := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) < 2*block.BlockSize() || len(data)%block.BlockSize() != 0 {
		return nil, errors.New("invalid encrypted payload")
	}
	// Feishu prefixes the ciphertext with a fresh IV; the IV is not the key.
	iv, encrypted := data[:block.BlockSize()], data[block.BlockSize():]
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, encrypted)
	if len(plain) == 0 {
		return nil, errors.New("empty encrypted payload")
	}
	p := int(plain[len(plain)-1])
	if p < 1 || p > block.BlockSize() || p > len(plain) {
		return nil, errors.New("invalid padding")
	}
	for _, x := range plain[len(plain)-p:] {
		if int(x) != p {
			return nil, errors.New("invalid padding")
		}
	}
	return plain[:len(plain)-p], nil
}
func Discord(h Handler, c config.Channel) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if e != nil {
			http.Error(w, "body", 400)
			return
		}
		ts := r.Header.Get("X-Signature-Timestamp")
		stamp, te := strconv.ParseInt(ts, 10, 64)
		if te != nil || abs(time.Now().Unix()-stamp) > 300 {
			http.Error(w, "stale signature", 401)
			return
		}
		pub, e := hex.DecodeString(c.PublicKey)
		sig, e2 := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
		if e != nil || e2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), append([]byte(ts), b...), sig) {
			http.Error(w, "invalid signature", 401)
			return
		}
		var v struct {
			Type int    `json:"type"`
			ID   string `json:"id"`
			User struct {
				ID string `json:"id"`
			} `json:"user"`
			Member struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			} `json:"member"`
			Data struct {
				Name    string `json:"name"`
				Options []struct {
					Value string `json:"value"`
				} `json:"options"`
			} `json:"data"`
			ChannelID string `json:"channel_id"`
			Token     string `json:"token"`
		}
		if json.Unmarshal(b, &v) != nil {
			http.Error(w, "json", 400)
			return
		}
		if v.Type == 1 {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"type":1}`)
			return
		}
		if v.Type != 2 || v.ID == "" || v.Token == "" {
			http.Error(w, "unsupported interaction", http.StatusBadRequest)
			return
		}
		user := v.Member.User.ID
		if user == "" {
			user = v.User.ID
		}
		if !allowed(v.ChannelID, c.Chats) || !allowed(user, c.Users) {
			http.Error(w, "forbidden", 403)
			return
		}
		if e := h(r.Context(), Message{"discord", v.ChannelID, user, v.Data.Name + " " + options(v.Data.Options), v.ID, v.Token}); e != nil {
			http.Error(w, "failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"type":4,"data":{"content":"已收到，任务已进入队列"}}`)
	})
}
func options(v []struct {
	Value string `json:"value"`
}) string {
	a := []string{}
	for _, x := range v {
		a = append(a, x.Value)
	}
	return strings.Join(a, " ")
}
func Feishu(h Handler, c config.Channel) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if e != nil {
			http.Error(w, "body", 400)
			return
		}
		var envelope struct {
			Encrypt string `json:"encrypt"`
		}
		raw := b
		if json.Unmarshal(b, &envelope) != nil {
			http.Error(w, "json", http.StatusBadRequest)
			return
		}
		if c.EncryptKey != "" && envelope.Encrypt == "" {
			http.Error(w, "encrypted payload required", http.StatusUnauthorized)
			return
		}
		if envelope.Encrypt != "" {
			if c.EncryptKey == "" {
				http.Error(w, "encryption not configured", http.StatusUnauthorized)
				return
			}
			b, e = decryptFeishu(envelope.Encrypt, c.EncryptKey)
			if e != nil {
				http.Error(w, "decrypt", 400)
				return
			}
		}
		var v struct {
			Challenge string `json:"challenge"`
			Token     string `json:"token"`
			Header    struct {
				EventID   string `json:"event_id"`
				Token     string `json:"token"`
				EventType string `json:"event_type"`
			} `json:"header"`
			Event struct {
				Sender struct {
					SenderID struct {
						OpenID string `json:"open_id"`
					} `json:"sender_id"`
				} `json:"sender"`
				Message struct {
					ChatID      string `json:"chat_id"`
					Content     string `json:"content"`
					MessageType string `json:"message_type"`
				} `json:"message"`
			} `json:"event"`
		}
		if json.Unmarshal(b, &v) != nil {
			http.Error(w, "json", 400)
			return
		}
		if v.Challenge != "" {
			if !config.Equal(v.Token, c.Secret) {
				http.Error(w, "unauthorized", 401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": v.Challenge})
			return
		}
		if !config.Equal(v.Header.Token, c.Secret) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if c.EncryptKey != "" {
			ts, nonce := r.Header.Get("X-Lark-Request-Timestamp"), r.Header.Get("X-Lark-Request-Nonce")
			stamp, err := strconv.ParseInt(ts, 10, 64)
			want := sha256.Sum256(append([]byte(ts+nonce+c.EncryptKey), raw...))
			got, errSig := hex.DecodeString(r.Header.Get("X-Lark-Signature"))
			if err != nil || abs(time.Now().Unix()-stamp) > 300 || nonce == "" || errSig != nil || subtle.ConstantTimeCompare(got, want[:]) != 1 {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}
		if v.Header.EventType != "im.message.receive_v1" || v.Event.Message.MessageType != "text" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var content struct {
			Text string `json:"text"`
		}
		if v.Header.EventID == "" || json.Unmarshal([]byte(v.Event.Message.Content), &content) != nil || strings.TrimSpace(content.Text) == "" {
			http.Error(w, "invalid message", http.StatusBadRequest)
			return
		}
		if !allowed(v.Event.Message.ChatID, c.Chats) || !allowed(v.Event.Sender.SenderID.OpenID, c.Users) {
			http.Error(w, "forbidden", 403)
			return
		}
		if e := h(r.Context(), Message{"feishu", v.Event.Message.ChatID, v.Event.Sender.SenderID.OpenID, content.Text, v.Header.EventID, ""}); e != nil {
			http.Error(w, "failed", 500)
			return
		}
		w.WriteHeader(204)
	})
}
func allowed(v string, list []string) bool {
	if v == "" {
		return false
	}
	for _, x := range list {
		if x == "*" || subtle.ConstantTimeCompare([]byte(v), []byte(x)) == 1 {
			return true
		}
	}
	return false
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
func Signature(secret, body string) string {
	h := sha256.Sum256([]byte(secret + body))
	return hex.EncodeToString(h[:])
}

// Deliver sends one durable outbox item. It deliberately uses the provider's
// HTTPS APIs directly so delivery does not depend on a second gateway process.
func Deliver(ctx context.Context, d store.Delivery, c config.Config) error {
	client := &http.Client{Timeout: 15 * time.Second}
	return DeliverWithClient(ctx, d, c, client)
}

// DeliverWithClient keeps the provider protocol identical while allowing a
// bounded local transport in integration tests. Production callers should use
// Deliver; APIBaseURL accepts HTTPS or loopback HTTP only.
func DeliverWithClient(ctx context.Context, d store.Delivery, c config.Config, client *http.Client) error {
	ch := c.Channels[d.Destination.Channel]
	var endpoint string
	var body []byte
	base := ch.APIBaseURL
	switch d.Destination.Channel {
	case "telegram":
		if base == "" {
			base = "https://api.telegram.org"
		}
		endpoint = base + "/bot" + ch.Token + "/sendMessage"
		body, _ = json.Marshal(map[string]any{"chat_id": d.Destination.Chat, "text": d.Text, "disable_web_page_preview": true})
	case "discord":
		if d.Destination.Token == "" || ch.AppID == "" {
			return errors.New("discord delivery requires interaction token and app_id")
		}
		if base == "" {
			base = "https://discord.com"
		}
		endpoint = base + "/api/v10/webhooks/" + ch.AppID + "/" + d.Destination.Token
		body, _ = json.Marshal(map[string]any{"content": d.Text, "allowed_mentions": map[string]any{"parse": []string{}}})
	case "feishu":
		if base == "" {
			base = "https://open.feishu.cn"
		}
		token, err := feishuToken(ctx, client, ch, base)
		if err != nil {
			return err
		}
		endpoint = base + "/open-apis/im/v1/messages?receive_id_type=chat_id"
		content, _ := json.Marshal(map[string]string{"text": d.Text})
		body, _ = json.Marshal(map[string]any{"receive_id": d.Destination.Chat, "msg_type": "text", "content": string(content)})
		return postJSON(ctx, client, endpoint, body, map[string]string{"Authorization": "Bearer " + token}, "feishu")
	default:
		return errors.New("unsupported delivery channel")
	}
	return postJSON(ctx, client, endpoint, body, nil, d.Destination.Channel)
}

func feishuToken(ctx context.Context, client *http.Client, ch config.Channel, base string) (string, error) {
	body, _ := json.Marshal(map[string]string{"app_id": ch.AppID, "app_secret": ch.AppSecret})
	var out struct {
		TenantAccessToken string `json:"tenant_access_token"`
		Code              *int   `json:"code"`
	}
	if err := postDecode(ctx, client, base+"/open-apis/auth/v3/tenant_access_token/internal", body, nil, &out); err != nil {
		return "", err
	}
	if out.Code == nil || *out.Code != 0 || out.TenantAccessToken == "" {
		return "", errors.New("feishu token request rejected")
	}
	return out.TenantAccessToken, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, body []byte, headers map[string]string, provider string) error {
	// Discord uses the HTTP status; Telegram and Feishu also report failures
	// inside HTTP 200 responses. Never acknowledge those as delivered.
	if provider == "discord" {
		return postDecode(ctx, client, endpoint, body, headers, nil)
	}
	var result struct {
		OK   *bool `json:"ok"`
		Code *int  `json:"code"`
	}
	if err := postDecode(ctx, client, endpoint, body, headers, &result); err != nil {
		return err
	}
	if provider == "telegram" && (result.OK == nil || !*result.OK) || provider == "feishu" && (result.Code == nil || *result.Code != 0) {
		return fmt.Errorf("%s rejected delivery", provider)
	}
	return nil
}

func postDecode(ctx context.Context, client *http.Client, endpoint string, body []byte, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid provider endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Credential-bearing URLs and headers must not follow provider redirects.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("provider request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || len(b) > 1<<20 || json.Unmarshal(b, out) != nil {
			return errors.New("invalid provider response")
		}
	}
	return nil
}
