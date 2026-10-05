package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeRejectsGenericOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer srv.Close()
	h := Probe(context.Background(), Target{Name: "x", Kind: "go", URL: srv.URL, HealthPath: "/healthz"}, time.Second)
	if h.Healthy || h.Error == "" {
		t.Fatalf("expected unhealthy response: %+v", h)
	}
}

func TestSelectRefusesUnhealthyAndWritesAtomicState(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer good.Close()
	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	d := t.TempDir()
	p := filepath.Join(d, "targets.json")
	cfg := Config{Targets: []Target{{Name: "good", Kind: "go", URL: good.URL, HealthPath: "/healthz"}, {Name: "bad", Kind: "legacy", URL: bad.URL, HealthPath: "/healthz"}}, StateFile: filepath.Join(d, "state.json"), TimeoutSeconds: 1, FailureThreshold: 2, RecoveryThreshold: 2}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Select(context.Background(), "bad", "test"); err == nil {
		t.Fatal("selected unhealthy target")
	}
	s, err := c.Select(context.Background(), "good", "test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Target != "good" {
		t.Fatalf("selection=%+v", s)
	}
	got, err := c.Current()
	if err != nil || got.Target != "good" {
		t.Fatalf("current=%+v err=%v", got, err)
	}
}

func TestTrackerDebouncesTransitions(t *testing.T) {
	var tr Tracker
	if tr.Observe(false, 2, 2) || !tr.Observe(false, 2, 2) {
		t.Fatal("failure threshold transition missing")
	}
	if tr.Observe(true, 2, 2) || !tr.Observe(true, 2, 2) {
		t.Fatal("recovery threshold transition missing")
	}
}
