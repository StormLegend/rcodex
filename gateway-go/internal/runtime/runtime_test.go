package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeTurnWaitsForResult(t *testing.T) {
	d := t.TempDir()
	script := filepath.Join(d, "fake-claude")
	program := "#!/bin/sh\nread line\nprintf '%s\\n' '{\"type\":\"assistant\",\"message\":{\"content\":\"ok\"}}'\nprintf '%s\\n' '{\"result\":\"final answer\"}'\n"
	if err := os.WriteFile(script, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	rt := NewClaude(script)
	s, err := rt.Start(context.Background(), d, "", "ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	result, err := rt.Turn(context.Background(), s, "hello", func(Event) error { seen++; return nil })
	if err != nil || result != "final answer" || seen != 2 {
		t.Fatalf("result=%q events=%d err=%v", result, seen, err)
	}
}
