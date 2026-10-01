package channels

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
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
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(c.Secret)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
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
	if len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return nil, errors.New("invalid encrypted payload")
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, keyHash[:block.BlockSize()]).CryptBlocks(plain, data)
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
		b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
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
			Type   int    `json:"type"`
			ID     string `json:"id"`
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
		if !allowed(v.ChannelID, c.Chats) || !allowed(v.Member.User.ID, c.Users) {
			http.Error(w, "forbidden", 403)
			return
		}
		_ = h(r.Context(), Message{"discord", v.ChannelID, v.Member.User.ID, v.Data.Name + " " + options(v.Data.Options), v.ID, v.Token})
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
		b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if e != nil {
			http.Error(w, "body", 400)
			return
		}
		var envelope struct {
			Encrypt string `json:"encrypt"`
		}
		if json.Unmarshal(b, &envelope) == nil && envelope.Encrypt != "" {
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
				EventID string `json:"event_id"`
			} `json:"header"`
			Event struct {
				Sender struct {
					SenderID struct {
						OpenID string `json:"open_id"`
					} `json:"sender_id"`
				} `json:"sender"`
				Message struct {
					ChatID  string `json:"chat_id"`
					Content string `json:"content"`
				} `json:"message"`
			} `json:"event"`
		}
		if json.Unmarshal(b, &v) != nil {
			http.Error(w, "json", 400)
			return
		}
		if v.Challenge != "" {
			if !hmac.Equal([]byte(v.Token), []byte(c.Secret)) {
				http.Error(w, "unauthorized", 401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"challenge": v.Challenge})
			return
		}
		if !allowed(v.Event.Message.ChatID, c.Chats) || !allowed(v.Event.Sender.SenderID.OpenID, c.Users) {
			http.Error(w, "forbidden", 403)
			return
		}
		_ = h(r.Context(), Message{"feishu", v.Event.Message.ChatID, v.Event.Sender.SenderID.OpenID, v.Event.Message.Content, v.Header.EventID, ""})
		w.WriteHeader(204)
	})
}
func allowed(v string, list []string) bool {
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

var _ = bytes.NewBuffer
var _ = time.Now

// Deliver sends one durable outbox item. It deliberately uses the provider's
// HTTPS APIs directly so delivery does not depend on a second gateway process.
func Deliver(ctx context.Context, d store.Delivery, c config.Config) error {
	ch := c.Channels[d.Destination.Channel]
	client := &http.Client{Timeout: 15 * time.Second}
	var endpoint string
	var body []byte
	switch d.Destination.Channel {
	case "telegram":
		endpoint = "https://api.telegram.org/bot" + ch.Token + "/sendMessage"
		body, _ = json.Marshal(map[string]any{"chat_id": d.Destination.Chat, "text": d.Text, "disable_web_page_preview": true})
	case "discord":
		if d.Destination.Token == "" || ch.AppID == "" {
			return errors.New("discord delivery requires interaction token and app_id")
		}
		endpoint = "https://discord.com/api/v10/webhooks/" + ch.AppID + "/" + d.Destination.Token
		body, _ = json.Marshal(map[string]any{"content": d.Text})
	case "feishu":
		token, err := feishuToken(ctx, client, ch)
		if err != nil {
			return err
		}
		endpoint = "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id"
		content, _ := json.Marshal(map[string]string{"text": d.Text})
		body, _ = json.Marshal(map[string]any{"receive_id": d.Destination.Chat, "msg_type": "text", "content": string(content)})
		return postJSON(ctx, client, endpoint, body, map[string]string{"Authorization": "Bearer " + token})
	default:
		return errors.New("unsupported delivery channel")
	}
	return postJSON(ctx, client, endpoint, body, nil)
}

func feishuToken(ctx context.Context, client *http.Client, ch config.Channel) (string, error) {
	body, _ := json.Marshal(map[string]string{"app_id": ch.AppID, "app_secret": ch.AppSecret})
	var out struct {
		TenantAccessToken string `json:"tenant_access_token"`
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
	}
	if err := postDecode(ctx, client, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", body, nil, &out); err != nil {
		return "", err
	}
	if out.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu token failed: %s", out.Msg)
	}
	return out.TenantAccessToken, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, body []byte, headers map[string]string) error {
	return postDecode(ctx, client, endpoint, body, headers, &struct{}{})
}

func postDecode(ctx context.Context, client *http.Client, endpoint string, body []byte, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil && resp.ContentLength != 0 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}
