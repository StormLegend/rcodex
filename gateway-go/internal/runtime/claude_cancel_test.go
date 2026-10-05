package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClaudeCancellationReturnsContextErrorAndReapsProcess(t *testing.T) {
	d := t.TempDir()
	script := filepath.Join(d, "fake-claude")
	pidFile := filepath.Join(d, "pid")
	program := "#!/bin/sh\necho $$ > \"$CLAUDE_TEST_PID\"\nread line\nsleep 60\n"
	if err := os.WriteFile(script, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_TEST_PID", pidFile)
	rt := NewClaude(script)
	s, err := rt.Start(context.Background(), d, "", "ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := rt.Turn(ctx, s, "cancel me", nil)
		done <- e
	}()
	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, e := os.ReadFile(pidFile); e == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake Claude process did not start")
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Claude turn did not stop after cancellation")
	}
	if err != context.Canceled {
		t.Fatalf("cancellation error=%v", err)
	}
	if err := processExists(pid); err == nil {
		t.Fatalf("Claude process %d still exists after cancellation", pid)
	}
}

func processExists(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.Signal(0))
}
