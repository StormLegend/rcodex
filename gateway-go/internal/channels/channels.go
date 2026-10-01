package channels

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Channel, Chat, User, Text string
	ID                        string
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
		if e = h(r.Context(), Message{"telegram", strconv.FormatInt(v.Message.Chat.ID, 10), strconv.FormatInt(v.Message.From.ID, 10), v.Message.Text, strconv.FormatInt(v.UpdateID, 10)}); e != nil {
			http.Error(w, "failed", 500)
			return
		}
		w.WriteHeader(204)
	})
}
func Discord(h Handler, c config.Channel) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if e != nil {
			http.Error(w, "body", 400)
			return
		}
		pub, e := hex.DecodeString(c.PublicKey)
		sig, e2 := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
		if e != nil || e2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), append([]byte(r.Header.Get("X-Signature-Timestamp")), b...), sig) {
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
		_ = h(r.Context(), Message{"discord", v.ChannelID, v.Member.User.ID, v.Data.Name + " " + options(v.Data.Options), v.ID})
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
		_ = h(r.Context(), Message{"feishu", v.Event.Message.ChatID, v.Event.Sender.SenderID.OpenID, v.Event.Message.Content, v.Header.EventID})
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
func Signature(secret, body string) string {
	h := sha256.Sum256([]byte(secret + body))
	return hex.EncodeToString(h[:])
}

var _ = bytes.NewBuffer
var _ = time.Now
