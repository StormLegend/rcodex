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
