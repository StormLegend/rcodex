package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeRejectsUnsuccessfulOrMissingResult(t *testing.T) {
	for _, output := range []string{
		`{"type":"result","is_error":true,"result":"upstream failed"}`,
		`{"type":"result","subtype":"error_max_turns","is_error":false}`,
		`{"type":"assistant","message":{"content":[]}}`,
	} {
		t.Run(output, func(t *testing.T) {
			d := t.TempDir()
			p := filepath.Join(d, "claude-fixture")
			if err := os.WriteFile(p, []byte("#!/bin/sh\nread line\nprintf '%s\\n' '"+output+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			r := NewClaude(p)
			s, err := r.Start(context.Background(), d, "", "ask", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Turn(context.Background(), s, "test", nil); err == nil {
				t.Fatal("unsuccessful stream recorded as success")
			}
		})
	}
}

func TestClaudeHandlerFailureReapsChild(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "claude-fixture")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nread line\nprintf '%s\\n' '{\"type\":\"assistant\"}'\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := NewClaude(p).(*Claude)
	s, err := r.Start(context.Background(), d, "", "ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("event store unavailable")
	var child *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = r.Turn(ctx, s, "test", func(Event) error {
		r.mu.Lock()
		child = r.procs[s.ID]
		r.mu.Unlock()
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
	if child == nil || child.ProcessState == nil {
		t.Fatal("failed turn did not wait for its child")
	}
	r.mu.Lock()
	remaining := len(r.procs)
	r.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("remaining processes=%d", remaining)
	}
}
