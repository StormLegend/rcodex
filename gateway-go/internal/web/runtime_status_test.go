package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func TestRuntimeStatusRequiresAuthAndChecksExecutables(t *testing.T) {
	s, root := testServer(t)
	command := filepath.Join(root, "fixture")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.Cfg.CodexCommand = command
	s.Cfg.ClaudeCommand = filepath.Join(root, "missing")
	s.Cfg.ReadToken = strings.Repeat("r", 32)
	h := s.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/runtime/status", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/runtime/status", nil)
	r.Header.Set("Authorization", "Bearer "+s.Cfg.ReadToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result struct {
		Runtimes []struct {
			ID        string
			Available bool
		}
		Workspaces, Modes []string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatalf("status=%d body=%s err=%v", w.Code, w.Body, err)
	}
	if len(result.Runtimes) != 2 || !result.Runtimes[0].Available || result.Runtimes[1].Available || len(result.Workspaces) != 1 || result.Workspaces[0] != root {
		t.Fatalf("runtime info=%+v", result)
	}
	if strings.Contains(w.Body.String(), s.Cfg.Token) || strings.Contains(w.Body.String(), s.Cfg.ReadToken) || strings.Contains(w.Body.String(), `"full"`) {
		t.Fatal("status exposed a secret or unavailable full-access mode")
	}
}

func TestCreateSessionPersistsResolvedDefaultWorkspace(t *testing.T) {
	s, root := testServer(t)
	w := req(t, s.Handler(), http.MethodPost, "/api/sessions", strings.NewReader(`{"runtime":"claude","mode":"readonly"}`))
	var result struct{ Session store.Session }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 201 || result.Session.Workspace != root {
		t.Fatalf("status=%d body=%s error=%v", w.Code, w.Body, err)
	}
	stored, err := s.Store.Session(result.Session.ID)
	if err != nil || stored.Workspace != root {
		t.Fatalf("stored workspace=%q err=%v", stored.Workspace, err)
	}
}

func TestConsoleRootAndPrivateAPICachePolicy(t *testing.T) {
	s, _ := testServer(t)
	h := s.Handler()
	root := httptest.NewRecorder()
	h.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), `id="connect-form"`) || root.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("root status=%d cache=%q", root.Code, root.Header().Get("Cache-Control"))
	}
	private := httptest.NewRecorder()
	h.ServeHTTP(private, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if private.Code != http.StatusUnauthorized || private.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private status=%d cache=%q", private.Code, private.Header().Get("Cache-Control"))
	}
}
