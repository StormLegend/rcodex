package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestClaudeUsesSessionIDThenResume(t *testing.T) {
	d := t.TempDir()
	script := filepath.Join(d, "fake-claude")
	argsFile := filepath.Join(d, "args")
	program := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CLAUDE_TEST_ARGS\"\nread line\nprintf '%s\\n' '{\"result\":\"ok\"}'\n"
	if err := os.WriteFile(script, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_TEST_ARGS", argsFile)
	rt := NewClaude(script)
	s, err := rt.Start(context.Background(), d, "", "readonly", nil)
	if err != nil || s.ID == "" {
		t.Fatalf("start=%+v err=%v", s, err)
	}
	if _, err = rt.Turn(context.Background(), s, "one", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Resume(context.Background(), s, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Turn(context.Background(), s, "two", nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "--session-id "+s.ID) || !strings.Contains(lines[1], "--resume "+s.ID) {
		t.Fatalf("claude args=%q session=%s", string(b), s.ID)
	}
}

func TestCodexServerRequestCanBeAnswered(t *testing.T) {
	d := t.TempDir()
	script := filepath.Join(d, "fake-codex")
	program := `#!/bin/sh
read line; echo '{"id":1,"result":{}}'
read line; echo '{"id":2,"result":{"thread":{"id":"thread-1","model":"m"}}}'
read line; echo '{"id":3,"result":{}}'
printf '%s\n' '{"jsonrpc":"2.0","id":"srv-1","method":"item/commandExecution/requestApproval","params":{"reason":"test"}}'
read response
case "$response" in *accept*) printf '%s\n' '{"method":"turn/completed","params":{}}' ;; *) exit 9 ;; esac
`
	if err := os.WriteFile(script, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	rt := NewCodex(script)
	s, err := rt.Start(context.Background(), d, "", "ask", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.Turn(context.Background(), s, "hello", func(ev Event) error {
		if ev.Method != "item/commandExecution/requestApproval" {
			return nil
		}
		if ev.Respond == nil {
			t.Fatal("missing response function")
		}
		return ev.Respond(map[string]string{"decision": "accept"})
	})
	if err != nil {
		t.Fatal(err)
	}
}
