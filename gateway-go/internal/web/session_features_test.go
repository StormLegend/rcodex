package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSessionSettingsCatalogAndUsage(t *testing.T) {
	s, root := testServer(t)
	s.Cfg.CodexHome = t.TempDir()
	os.WriteFile(filepath.Join(s.Cfg.CodexHome, "models_cache.json"), []byte(`{"models":[{"slug":"visible-model","display_name":"Visible","visibility":"list","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]},{"slug":"hidden-model","visibility":"hide"}]}`), 0600)
	h := s.Handler()
	r := req(t, h, "GET", "/api/models", nil)
	if !strings.Contains(r.Body.String(), "visible-model") || strings.Contains(r.Body.String(), "hidden-model") {
		t.Fatal(r.Body.String())
	}
	v, e := s.Store.CreateSession(store.Session{Runtime: "codex", Workspace: root, Mode: "readonly"})
	if e != nil {
		t.Fatal(e)
	}
	path := "/api/sessions/" + v.ID
	r = req(t, h, "PATCH", path, bytes.NewBufferString(`{"model":"visible-model","effort":"ultra"}`))
	if r.Code != 400 {
		t.Fatalf("unsupported effort: %d", r.Code)
	}
	r = req(t, h, "PATCH", path, bytes.NewBufferString(`{"model":"visible-model","effort":"high","archived":true}`))
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	v, _ = s.Store.Session(v.ID)
	if !v.Archived || v.Effort != "high" || v.Model != "visible-model" {
		t.Fatal(v)
	}
	r = req(t, h, "POST", "/api/turns", bytes.NewBufferString(`{"session_id":"`+v.ID+`","prompt":"test"}`))
	if r.Code != 400 {
		t.Fatalf("archived turn %d", r.Code)
	}
	req(t, h, "PATCH", path, bytes.NewBufferString(`{"archived":false}`))
	turn, e := s.Store.Enqueue(v.ID, "test", "", "", 10)
	if e != nil {
		t.Fatal(e)
	}
	r = req(t, h, "PATCH", path, bytes.NewBufferString(`{"effort":"low"}`))
	if r.Code != 409 {
		t.Fatalf("busy settings %d", r.Code)
	}
	s.Store.Event(v.ID, turn.ID, "thread/tokenUsage/updated", json.RawMessage(`{"tokenUsage":{"total":{"totalTokens":90000},"last":{"totalTokens":250},"modelContextWindow":1000}}`))
	r = req(t, h, "GET", path+"/history", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"context_tokens":250`) {
		t.Fatal(r.Body.String())
	}
}
func TestEventStreamSurvivesServerWriteTimeoutAndReplaysCursor(t *testing.T) {
	s, _ := testServer(t)
	v, _ := s.Store.CreateSession(store.Session{Runtime: "codex"})
	old, _ := s.Store.Event(v.ID, "t", "old", map[string]any{})
	ts := httptest.NewUnstartedServer(s.Handler())
	ts.Config.WriteTimeout = 80 * time.Millisecond
	ts.Start()
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/sessions/"+v.ID+"/events/stream", nil)
	request.Header.Set("Authorization", "Bearer "+s.Cfg.Token)
	request.Header.Set("Last-Event-ID", strconv.FormatInt(old.ID, 10))
	response, e := ts.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, e := reader.ReadString('\n')
	if e != nil || line != ": connected\n" {
		t.Fatalf("initial flush %q %v", line, e)
	}
	time.Sleep(180 * time.Millisecond)
	s.Store.Event(v.ID, "t", "after-timeout", map[string]any{})
	for {
		line, e = reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(line, `"kind":"old"`) {
			t.Fatal("cursor ignored")
		}
		if strings.Contains(line, "after-timeout") {
			break
		}
	}
}
