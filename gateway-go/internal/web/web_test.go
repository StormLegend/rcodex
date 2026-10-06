package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/engine"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	// Match config.Load's canonical roots (macOS /var is a symlink).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dbdir := t.TempDir()
	s, err := store.Open(filepath.Join(dbdir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DB.Close() })
	c := config.Config{Listen: "127.0.0.1:0", DataDir: dbdir, Token: "test-token-012345678901234567890123456789", Roots: []string{root}, Workers: 1, QueueLimit: 10, TurnSeconds: 10, CodexCommand: "codex", ClaudeCommand: "claude", RateLimitPerMinute: 1000, ReadRateLimitPerMinute: 1000}
	e := engine.New(s, c, slog.Default())
	return &Server{Cfg: c, Store: s, Engine: e, Log: slog.Default()}, root
}
func req(t *testing.T, h http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("Authorization", "Bearer test-token-012345678901234567890123456789")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestSessionLifecycleAttachmentsSchedulesAndModels(t *testing.T) {
	s, root := testServer(t)
	h := s.Handler()
	body := bytes.NewBufferString(`{"runtime":"claude","workspace":"` + root + `","mode":"ask","title":"demo"}`)
	w := req(t, h, http.MethodPost, "/api/sessions", body)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var created struct{ Session store.Session }
	if json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Session.ID == "" {
		t.Fatal(w.Body.String())
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "note.txt")
	_, _ = part.Write([]byte("hello"))
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+created.Session.ID+"/attachments", &buf)
	r.Header.Set("Authorization", "Bearer test-token-012345678901234567890123456789")
	r.Header.Set("Content-Type", mw.FormDataContentType())
	aw := httptest.NewRecorder()
	h.ServeHTTP(aw, r)
	if aw.Code != 201 {
		t.Fatalf("attachment %d %s", aw.Code, aw.Body)
	}
	if _, err := os.Stat(filepath.Join(s.Cfg.DataDir, "attachments", created.Session.ID)); err != nil {
		t.Fatal(err)
	}
	schedule := bytes.NewBufferString(`{"session_id":"` + created.Session.ID + `","prompt":"ping","expression":"once"}`)
	w = req(t, h, http.MethodPost, "/api/schedules", schedule)
	if w.Code != 201 {
		t.Fatalf("schedule %d %s", w.Code, w.Body)
	}
	w = req(t, h, http.MethodGet, "/api/providers", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = req(t, h, http.MethodGet, "/api/providers/claude/models", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"provider":"claude"`) {
		t.Fatalf("provider models status=%d body=%s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodPatch, "/api/sessions/"+created.Session.ID, bytes.NewBufferString(`{"title":"renamed"}`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"renamed"`) {
		t.Fatalf("partial patch status=%d body=%s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodGet, "/api/sessions/"+created.Session.ID+"/turns", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = req(t, h, http.MethodGet, "/api/sessions/"+created.Session.ID+"/history?limit=10", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"turns"`) {
		t.Fatalf("history status=%d body=%s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodGet, "/api/sessions?limit=1", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sessions"`) {
		t.Fatalf("sessions status=%d body=%s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodDelete, "/api/sessions/"+created.Session.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	w = req(t, h, http.MethodGet, "/api/sessions/"+created.Session.ID, nil)
	if w.Code != 404 {
		t.Fatalf("deleted session status %d", w.Code)
	}
}

func TestAuthRateLimitSeparatesHealth(t *testing.T) {
	s, _ := testServer(t)
	s.Cfg.RateLimitPerMinute = 1
	s.Cfg.ReadRateLimitPerMinute = 1
	h := s.Handler()
	first := req(t, h, http.MethodGet, "/api/providers", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first request=%d", first.Code)
	}
	second := req(t, h, http.MethodGet, "/api/models", nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit=%d", second.Code)
	}
	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health=%d", health.Code)
	}
}

func TestSessionPaginationReturnsNextCursor(t *testing.T) {
	s, root := testServer(t)
	h := s.Handler()
	for i := 0; i < 2; i++ {
		body := bytes.NewBufferString(`{"runtime":"codex","workspace":"` + root + `","mode":"ask"}`)
		if w := req(t, h, http.MethodPost, "/api/sessions", body); w.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", w.Code, w.Body)
		}
	}
	w := req(t, h, http.MethodGet, "/api/sessions?limit=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("first page status=%d", w.Code)
	}
	var page struct {
		Sessions   []store.Session `json:"sessions"`
		NextBefore int64           `json:"next_before"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Sessions) != 1 || page.NextBefore == 0 {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	w = req(t, h, http.MethodGet, "/api/sessions?limit=1&before="+strconv.FormatInt(page.NextBefore, 10), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sessions"`) {
		t.Fatalf("second page status=%d body=%s", w.Code, w.Body)
	}
}
