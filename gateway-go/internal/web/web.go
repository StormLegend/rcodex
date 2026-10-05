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
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	m.HandleFunc("/api/attachments/", s.attachment)
	m.HandleFunc("/api/schedules", s.schedules)
	m.HandleFunc("/api/schedules/", s.schedule)
	m.HandleFunc("/api/providers", s.providers)
	m.HandleFunc("/api/models", s.models)
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

type rateWindow struct {
	Started time.Time
	Count   int
}
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

func newRateLimiter() *rateLimiter { return &rateLimiter{windows: map[string]rateWindow{}} }
func (l *rateLimiter) allow(key string, limit int) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[key]
	if now.Sub(w.Started) >= time.Minute {
		w = rateWindow{Started: now}
	}
	if w.Count >= limit {
		return false
	}
	w.Count++
	l.windows[key] = w
	if len(l.windows) > 10000 {
		for k, old := range l.windows {
			if now.Sub(old.Started) >= time.Minute {
				delete(l.windows, k)
			}
		}
	}
	return true
}
func remoteKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func auth(c config.Config, next http.Handler) http.Handler {
	limiter := newRateLimiter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			limit := c.RateLimitPerMinute
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				limit = c.ReadRateLimitPerMinute
			}
			class := "write"
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				class = "read"
			}
			if limit > 0 && !limiter.allow(remoteKey(r)+":"+class, limit) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/console" || strings.HasPrefix(r.URL.Path, "/console/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
			next.ServeHTTP(w, r)
			return
		}
		v := r.Header.Get("Authorization")
		if !strings.HasPrefix(v, "Bearer ") {
			http.Error(w, "unauthorized", 401)
			return
		}
		provided := strings.TrimPrefix(v, "Bearer ")
		valid := subtle.ConstantTimeCompare([]byte(provided), []byte(c.Token)) == 1
		if !valid && (r.Method == http.MethodGet || r.Method == http.MethodHead) && c.ReadToken != "" {
			valid = subtle.ConstantTimeCompare([]byte(provided), []byte(c.ReadToken)) == 1
		}
		if !valid {
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
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		v, e := s.Store.Sessions(before, queryLimit(r.URL.Query().Get("limit"), 100, 500))
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
	if id == "" || strings.ContainsAny(id, "\\/") {
		http.NotFound(w, r)
		return
	}
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
	if len(parts) > 1 && (parts[1] == "turns" || parts[1] == "history") {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		items, e := s.Store.Turns(id, before, queryLimit(r.URL.Query().Get("limit"), 100, 500))
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, http.StatusOK, map[string]any{"turns": items})
		return
	}
	if len(parts) > 1 && parts[1] == "attachments" {
		if r.Method == http.MethodGet {
			items, e := s.Store.Attachments(id, queryLimit(r.URL.Query().Get("limit"), 100, 500))
			if e != nil {
				s.writeErr(w, e)
				return
			}
			s.write(w, http.StatusOK, map[string]any{"attachments": items})
			return
		}
		if r.Method == http.MethodPost {
			s.uploadAttachment(w, r, id)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		var x struct {
			Model string `json:"model"`
			Mode  string `json:"mode"`
			Title string `json:"title"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&x) != nil {
			s.writeErr(w, errors.New("invalid body"))
			return
		}
		if x.Mode != "readonly" && x.Mode != "ask" && x.Mode != "auto" && x.Mode != "full" {
			s.writeErr(w, errors.New("invalid permission mode"))
			return
		}
		if x.Mode == "full" && !s.Cfg.AllowFull {
			s.writeErr(w, errors.New("full access is disabled"))
			return
		}
		v, e = s.Store.UpdateSession(id, x.Model, x.Mode, x.Title)
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, http.StatusOK, map[string]any{"session": v})
		return
	}
	if r.Method == http.MethodDelete {
		paths, e := s.Store.DeleteSession(id)
		if e != nil {
			s.writeErr(w, e)
			return
		}
		for _, p := range paths {
			_ = os.Remove(p)
		}
		s.write(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		return
	}
	s.write(w, 200, map[string]any{"session": v})
}
func queryLimit(raw string, fallback, max int) int {
	n, _ := strconv.Atoi(raw)
	if n < 1 {
		n = fallback
	}
	if n > max {
		n = max
	}
	return n
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
		if e := s.Engine.Cancel(id); e != nil {
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
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request, sessionID string) {
	const max = 16 << 20
	r.Body = http.MaxBytesReader(w, r.Body, max+1<<20)
	if e := r.ParseMultipartForm(max); e != nil {
		s.writeErr(w, errors.New("invalid multipart attachment"))
		return
	}
	f, header, e := r.FormFile("file")
	if e != nil {
		s.writeErr(w, errors.New("file field is required"))
		return
	}
	defer f.Close()
	name := filepath.Base(header.Filename)
	name = safeFilename(name)
	if name == "." || name == "" || name == string(filepath.Separator) {
		s.writeErr(w, errors.New("invalid attachment name"))
		return
	}
	if len(name) > 255 {
		s.writeErr(w, errors.New("attachment name too long"))
		return
	}
	id := store.ID()
	dir := filepath.Join(s.Cfg.DataDir, "attachments", sessionID)
	if e = os.MkdirAll(dir, 0700); e != nil {
		s.writeErr(w, e)
		return
	}
	tmp, e := os.CreateTemp(dir, ".upload-*")
	if e != nil {
		s.writeErr(w, e)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	n, e := io.CopyN(tmp, f, max+1)
	if e != nil && e != io.EOF {
		_ = tmp.Close()
		s.writeErr(w, e)
		return
	}
	if n > max {
		_ = tmp.Close()
		s.writeErr(w, errors.New("attachment exceeds 16 MiB"))
		return
	}
	if e = tmp.Sync(); e == nil {
		e = tmp.Close()
	} else {
		_ = tmp.Close()
	}
	if e != nil {
		s.writeErr(w, e)
		return
	}
	final := filepath.Join(dir, id+"-"+name)
	if e = os.Rename(tmpName, final); e != nil {
		s.writeErr(w, e)
		return
	}
	v, e := s.Store.AddAttachment(store.Attachment{ID: id, SessionID: sessionID, Name: name, Path: final, Mime: header.Header.Get("Content-Type"), Size: n})
	if e != nil {
		_ = os.Remove(final)
		s.writeErr(w, e)
		return
	}
	s.write(w, http.StatusCreated, map[string]any{"attachment": v})
}
func (s *Server) attachment(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/attachments/"), "/")
	if id == "" || strings.ContainsAny(id, "\\/") {
		http.NotFound(w, r)
		return
	}
	v, e := s.Store.Attachment(id)
	if e != nil {
		s.writeErr(w, e)
		return
	}
	if r.Method == http.MethodDelete {
		v, e = s.Store.DeleteAttachment(id)
		if e != nil {
			s.writeErr(w, e)
			return
		}
		_ = os.Remove(v.Path)
		s.write(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	f, e := os.Open(v.Path)
	if e != nil {
		s.writeErr(w, store.ErrNotFound)
		return
	}
	defer f.Close()
	if v.Mime != "" {
		w.Header().Set("Content-Type", v.Mime)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(v.Name, `"`, `'`)+`"`)
	http.ServeContent(w, r, v.Name, time.UnixMilli(v.Created), f)
}
func safeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '-'
		}
		return r
	}, name)
}
func (s *Server) schedules(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		items, e := s.Store.Schedules(r.URL.Query().Get("session_id"), queryLimit(r.URL.Query().Get("limit"), 100, 500))
		if e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, http.StatusOK, map[string]any{"schedules": items})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var v struct {
		SessionID  string `json:"session_id"`
		Prompt     string `json:"prompt"`
		Expression string `json:"expression"`
		NextAt     int64  `json:"next_at"`
		Paused     bool   `json:"paused"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v) != nil || v.SessionID == "" || strings.TrimSpace(v.Prompt) == "" {
		s.writeErr(w, errors.New("session_id and prompt are required"))
		return
	}
	if _, e := s.Store.Session(v.SessionID); e != nil {
		s.writeErr(w, e)
		return
	}
	if _, _, e := engine.ParseScheduleInterval(v.Expression); e != nil {
		s.writeErr(w, e)
		return
	}
	if v.NextAt == 0 {
		v.NextAt = store.Now()
	}
	x, e := s.Store.CreateSchedule(store.Schedule{SessionID: v.SessionID, Prompt: v.Prompt, Expression: v.Expression, NextAt: v.NextAt, Paused: v.Paused})
	if e != nil {
		s.writeErr(w, e)
		return
	}
	s.write(w, http.StatusCreated, map[string]any{"schedule": x})
}
func (s *Server) schedule(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/schedules/"), "/")
	parts := strings.Split(tail, "/")
	id := parts[0]
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) > 1 && parts[1] == "pause" && r.Method == http.MethodPost {
		var v struct {
			Paused bool `json:"paused"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v) != nil {
			s.writeErr(w, errors.New("invalid body"))
			return
		}
		if e := s.Store.UpdateScheduleNext(id, store.Now(), v.Paused); e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, http.StatusOK, map[string]any{"ok": true, "paused": v.Paused})
		return
	}
	if r.Method == http.MethodDelete {
		if e := s.Store.DeleteSchedule(id); e != nil {
			s.writeErr(w, e)
			return
		}
		s.write(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
		return
	}
	http.NotFound(w, r)
}
func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	out := map[string]any{"codex": map[string]any{"runtime": "codex", "command": s.Cfg.CodexCommand, "enabled": true}, "claude": map[string]any{"runtime": "claude", "command": s.Cfg.ClaudeCommand, "enabled": true}}
	for name, p := range s.Cfg.Providers {
		out[name] = map[string]any{"runtime": p.Runtime, "models": p.Models, "enabled": p.Enabled}
	}
	s.write(w, http.StatusOK, map[string]any{"providers": out})
}
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	out := map[string][]string{}
	for name, p := range s.Cfg.Providers {
		if p.Enabled {
			out[name] = append([]string(nil), p.Models...)
		}
	}
	s.write(w, http.StatusOK, map[string]any{"models": out})
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
