package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/runtime"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
)

func waitTurn(t *testing.T, s *store.Store, id, state string) store.Turn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tn, err := s.Turn(id)
		if err != nil {
			t.Fatal(err)
		}
		if tn.State == state {
			return tn
		}
		if tn.State != "queued" && tn.State != "running" {
			t.Fatalf("want %s got %+v", state, tn)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("turn %s never reached %s", id, state)
	return store.Turn{}
}

func TestZeroTurnSecondsHasNoArtificialDeadline(t *testing.T) {
	e := New(nil, config.Config{TurnSeconds: 0}, slog.Default())
	ctx, cancel := e.executionContext(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("zero turn_seconds unexpectedly cancelled the Goal")
	case <-time.After(40 * time.Millisecond):
	}
}

func TestNativeIdentityMustPersistBeforeRuntimeExecution(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(filepath.Join(root, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	// An actual SQLite write failure must prevent spawning the model CLI.
	if _, err = s.DB.Exec(`CREATE TRIGGER reject_native BEFORE UPDATE OF native_id ON sessions BEGIN SELECT RAISE(ABORT,'fixture persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(store.Session{Runtime: "claude", Workspace: root, Mode: "readonly"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Enqueue(session.ID, "hello", "native-failure", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim()
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "claude-fixture")
	if err = os.WriteFile(program, []byte("#!/bin/sh\ntouch executed\nread line\nprintf '%s\\n' '{\"type\":\"result\",\"result\":\"ok\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	e := New(s, config.Config{ClaudeCommand: program, TurnSeconds: 5}, slog.Default())
	defer e.Stop()
	e.run(context.Background(), claimed)
	got, err := s.Turn(turn.ID)
	if err != nil || got.State != "failed" || !strings.Contains(got.Error, "fixture persistence failure") {
		t.Fatalf("turn=%+v error=%v", got, err)
	}
	if _, err = os.Stat(filepath.Join(root, "executed")); !os.IsNotExist(err) {
		t.Fatalf("CLI executed after native ID persistence failed: %v", err)
	}
}

func TestApprovalResolutionReachesRuntime(t *testing.T) {
	for _, mode := range []string{"ask", "auto"} {
		t.Run(mode, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			session, err := s.CreateSession(store.Session{Runtime: "codex", Workspace: t.TempDir(), Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := s.Enqueue(session.ID, "approval test", "approval", "", 10)
			if err != nil {
				t.Fatal(err)
			}
			e := New(s, config.Config{}, slog.Default())
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			response := make(chan any, 1)
			done := make(chan error, 1)
			go func() {
				done <- e.handleEvent(ctx, turn, session, runtime.Event{Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"command":"fixture"}`), Respond: func(v any) error { response <- v; return nil }})
			}()
			if mode == "ask" {
				deadline := time.Now().Add(2 * time.Second)
				for {
					pending, err := s.Approvals("pending", 10)
					if err != nil {
						t.Fatal(err)
					}
					if len(pending) == 1 {
						if err = s.Resolve(pending[0].ID, map[string]bool{"approved": false}); err != nil {
							t.Fatal(err)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("approval never persisted")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			got := (<-response).(map[string]string)["decision"]
			want := "decline"
			if mode == "auto" {
				want = "accept"
			}
			if got != want {
				t.Fatalf("decision=%s", got)
			}
			pending, err := s.Approvals("pending", 10)
			if err != nil || len(pending) != 0 {
				t.Fatalf("resolved approval remains pending: %v %v", pending, err)
			}
		})
	}
}

// This uses a real subprocess and SQLite reopen, with only the remote model
// replaced by a deterministic CLI protocol fixture.
func TestClaudeEngineResumeAcrossRestartAndTerminalStates(t *testing.T) {
	d := t.TempDir()
	program := filepath.Join(d, "claude-fixture")
	if err := os.WriteFile(program, []byte(`#!/bin/sh
printf '%s\n' "$*" >> args.log
read line
case "$line" in
  *block*) printf '%s\n' '{"type":"assistant","message":{"content":[]}}'; sleep 30 ;;
  *upstream-failure*) printf '%s\n' '{"type":"result","is_error":true,"result":"denied"}' ;;
  *) printf '%s\n' '{"type":"result","is_error":false,"result":"fixture answer"}' ;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{Workers: 1, TurnSeconds: 1, QueueLimit: 10, ClaudeCommand: program}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := store.Open(filepath.Join(d, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := New(s, c, log)
	defer func() { e.Stop(); _ = s.DB.Close() }()
	v, err := s.CreateSession(store.Session{Runtime: "claude", Workspace: d, Mode: "readonly"})
	if err != nil {
		t.Fatal(err)
	}
	e.Start(context.Background())
	first, err := s.Enqueue(v.ID, "hello", "first", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if tn := waitTurn(t, s, first.ID, "completed"); tn.Result != "fixture answer" {
		t.Fatal(tn)
	}
	v, err = s.Session(v.ID)
	if err != nil || v.NativeID == "" {
		t.Fatalf("native session=%+v err=%v", v, err)
	}
	native := v.NativeID
	e.Stop()
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(filepath.Join(d, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	e = New(s, c, log)
	e.Start(context.Background())
	second, err := s.Enqueue(v.ID, "remember", "second", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, s, second.ID, "completed")
	v, err = s.Session(v.ID)
	if err != nil || v.NativeID != native {
		t.Fatalf("lost identity: %+v %v", v, err)
	}
	b, err := os.ReadFile(filepath.Join(d, "args.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "--session-id "+native) || !strings.Contains(lines[1], "--resume "+native) {
		t.Fatalf("CLI args=%q", b)
	}
	cancelled, err := s.Enqueue(v.ID, "block", "cancel", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, s, cancelled.ID, "running")
	if err = e.Cancel(cancelled.ID); err != nil {
		t.Fatal(err)
	}
	waitTurn(t, s, cancelled.ID, "cancelled")
	timed, err := s.Enqueue(v.ID, "block", "timeout", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, s, timed.ID, "timed_out")
	failed, err := s.Enqueue(v.ID, "upstream-failure", "failed", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	waitTurn(t, s, failed.ID, "failed")
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM events WHERE kind='turn.finished' AND session_id=?", v.ID).Scan(&count); err != nil || count != 5 {
		t.Fatalf("terminal events=%d err=%v", count, err)
	}
}
