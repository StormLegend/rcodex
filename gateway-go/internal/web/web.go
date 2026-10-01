package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/StormLegend/rcodex/gateway-go/internal/channels"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/engine"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	Cfg    config.Config
	Store  *store.Store
	Engine *engine.Engine
	Log    *slog.Logger
}

var Version = "0.1.0-go"

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/healthz", s.health)
	m.HandleFunc("/console", console)
	m.HandleFunc("/console/", console)
	m.HandleFunc("/metrics", s.metrics)
	m.HandleFunc("/api/sessions", s.sessions)
	m.HandleFunc("/api/sessions/", s.session)
	m.HandleFunc("/api/turns", s.turns)
	m.HandleFunc("/api/turns/", s.turn)
	m.HandleFunc("/api/approvals/", s.approval)
	m.HandleFunc("/api/approvals", s.approvals)
	if c, ok := s.Cfg.Channels["telegram"]; ok {
		m.Handle("/webhooks/telegram", channels.Telegram(s.channelMessage, c))
	}
	if c, ok := s.Cfg.Channels["discord"]; ok {
		m.Handle("/webhooks/discord", channels.Discord(s.channelMessage, c))
	}
	if c, ok := s.Cfg.Channels["feishu"]; ok {
		m.Handle("/webhooks/feishu", channels.Feishu(s.channelMessage, c))
	}
	return auth(s.Cfg, m)
}
func (s *Server) channelMessage(ctx context.Context, m channels.Message) error {
	ch := s.Cfg.Channels[m.Channel]
	session, e := s.Store.Bind(m.Channel+":"+m.Chat, store.Session{Runtime: ch.Runtime, Workspace: ch.Workspace, Model: ch.Model, Mode: ch.Mode, Title: m.Channel + " " + m.Chat})
	if e != nil {
		return e
	}
	notify, _ := json.Marshal(store.Notification{Channel: m.Channel, Chat: m.Chat, Token: m.Token, AppID: ch.AppID})
	_, e = s.Store.Enqueue(session.ID, m.Text, "channel:"+m.Channel+":"+m.ID, string(notify), s.Cfg.QueueLimit)
	return e
}
func auth(c config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/console" || strings.HasPrefix(r.URL.Path, "/console/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
			next.ServeHTTP(w, r)
			return
		}
		v := r.Header.Get("Authorization")
		if !strings.HasPrefix(v, "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(v, "Bearer ")), []byte(c.Token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.write(w, 200, map[string]any{"ok": true, "version": Version, "stats": s.Store.Stats()})
}
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	st := s.Store.Stats()
	for k, v := range st {
		w.Write([]byte("rcg_" + k + " " + strconv.FormatInt(v, 10) + "\n"))
	}
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		v, e := s.Store.Sessions(0, 100)
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, 200, map[string]any{"sessions": v})
		return
	}
	if r.Method == "POST" {
		var v struct {
			Runtime   string `json:"runtime"`
			Workspace string `json:"workspace"`
			Model     string `json:"model"`
			Mode      string `json:"mode"`
			Title     string `json:"title"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v) != nil {
			s.writeErr(w, errors.New("invalid body"))
			return
		}
		if v.Runtime == "" {
			v.Runtime = "codex"
		}
		if v.Mode == "" {
			v.Mode = "ask"
		}
		if v.Runtime != "codex" && v.Runtime != "claude" {
			s.writeErr(w, errors.New("unsupported runtime"))
			return
		}
		if v.Mode != "readonly" && v.Mode != "ask" && v.Mode != "auto" && v.Mode != "full" {
			s.writeErr(w, errors.New("invalid permission mode"))
			return
		}
		if v.Mode == "full" && !s.Cfg.AllowFull {
			s.writeErr(w, errors.New("full access is disabled"))
			return
		}
		if _, e := s.Cfg.Workspace(v.Workspace); e != nil {
			s.writeErr(w, e)
			return
		}
		x, e := s.Store.CreateSession(store.Session{Runtime: v.Runtime, Workspace: v.Workspace, Model: v.Model, Mode: v.Mode, Title: v.Title})
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, 201, map[string]any{"session": x})
		return
	}
	http.NotFound(w, r)
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	id := parts[0]
	v, e := s.Store.Session(id)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	if len(parts) > 2 && parts[1] == "events" && parts[2] == "stream" {
		s.eventStream(w, r, id)
		return
	}
	if len(parts) > 1 && parts[1] == "events" {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		ev, e := s.Store.Events(id, after, before, 100)
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, 200, map[string]any{"events": ev})
		return
	}
	s.write(w, 200, map[string]any{"session": v})
}
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request, sessionID string) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := s.Store.Events(sessionID, after, 0, 100)
		if err != nil {
			return
		}
		for _, ev := range events {
			b, _ := json.Marshal(ev)
			_, _ = w.Write([]byte("id: " + strconv.FormatInt(ev.ID, 10) + "\ndata: " + string(b) + "\n\n"))
			after = ev.ID
		}
		if len(events) > 0 {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Server) turns(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	var v struct {
		SessionID      string `json:"session_id"`
		Prompt         string `json:"prompt"`
		IdempotencyKey string `json:"idempotency_key"`
		Notify         string `json:"notify"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v) != nil {
		s.writeErr(w, errors.New("invalid body"))
		return
	}
	if v.SessionID == "" || v.Prompt == "" {
		s.writeErr(w, errors.New("session_id and prompt are required"))
		return
	}
	if _, e := s.Store.Session(v.SessionID); e != nil {
		s.writeErr(w, e)
		return
	}
	t, e := s.Store.Enqueue(v.SessionID, v.Prompt, v.IdempotencyKey, v.Notify, s.Cfg.QueueLimit)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	s.write(w, 202, map[string]any{"turn": t})
}
func (s *Server) turn(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/turns/")
	if strings.HasSuffix(id, "/cancel") {
		id = strings.TrimSuffix(id, "/cancel")
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if e := s.Store.CancelQueued(id); e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, 200, map[string]bool{"ok": true})
		return
	}
	t, e := s.Store.Turn(id)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	s.write(w, 200, map[string]any{"turn": t})
}
func (s *Server) approval(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/approvals/"), "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		v, e := s.Store.Approval(id)
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, 200, map[string]any{"approval": v})
		return
	}
	if r.Method == http.MethodPost {
		var v struct {
			Approved bool           `json:"approved"`
			Reason   string         `json:"reason"`
			Answers  map[string]any `json:"answers,omitempty"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v) != nil {
			s.writeErr(w, errors.New("invalid body"))
			return
		}
		if e := s.Store.Resolve(id, v); e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, 200, map[string]bool{"ok": true})
		return
	}
	http.NotFound(w, r)
}
func (s *Server) approvals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	items, e := s.Store.Approvals(r.URL.Query().Get("state"), 100)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	s.write(w, 200, map[string]any{"approvals": items})
}
func (s *Server) write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (s *Server) writeErr(w http.ResponseWriter, e error) {
	status := 500
	if errors.Is(e, store.ErrNotFound) {
		status = 404
	}
	if errors.Is(e, store.ErrBusy) {
		status = 409
	}
	if status == 500 {
		status = 400
	}
	s.write(w, status, map[string]string{"error": e.Error()})
}
func Run(ctx context.Context, s *Server) error {
	srv := &http.Server{Addr: s.Cfg.Listen, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	go s.deliver(ctx)
	if s.Cfg.TLSCert != "" {
		return srv.ListenAndServeTLS(s.Cfg.TLSCert, s.Cfg.TLSKey)
	}
	return srv.ListenAndServe()
}

func (s *Server) deliver(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			items, err := s.Store.Deliveries()
			if err != nil {
				s.Log.Error("outbox scan failed", "error", err)
				continue
			}
			for _, item := range items {
				err := channels.Deliver(ctx, item, s.Cfg)
				if e := s.Store.DeliveryResult(item.ID, item.Attempts+1, err == nil); e != nil {
					s.Log.Error("outbox update failed", "error", e)
				}
				if err != nil {
					s.Log.Warn("outbox delivery failed", "id", item.ID, "attempt", item.Attempts+1, "error", err)
				}
			}
		}
	}
}
