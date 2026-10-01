package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:8790", "gateway URL")
	token := flag.String("token", os.Getenv("RCG_ACCESS_TOKEN"), "API token")
	cmd := flag.String("command", "health", "health|sessions|create|turn")
	session := flag.String("session", "", "session ID for turn")
	prompt := flag.String("prompt", "", "prompt for turn")
	workspace := flag.String("workspace", "", "workspace for create")
	runtime := flag.String("runtime", "codex", "runtime for create")
	mode := flag.String("mode", "ask", "permission mode for create")
	flag.Parse()
	var method, path string
	var body any
	switch *cmd {
	case "health":
		method, path = http.MethodGet, "/healthz"
	case "sessions":
		method, path = http.MethodGet, "/api/sessions"
	case "create":
		method, path, body = http.MethodPost, "/api/sessions", map[string]string{"runtime": *runtime, "mode": *mode, "workspace": *workspace}
	case "turn":
		method, path, body = http.MethodPost, "/api/turns", map[string]string{"session_id": *session, "prompt": *prompt}
	default:
		fail("unknown command")
	}
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(*base, "/")+path, reader)
	if err != nil {
		fail(err.Error())
	}
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(err.Error())
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s: %s\n", resp.Status, strings.TrimSpace(string(out)))
		os.Exit(1)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") == nil {
		out = pretty.Bytes()
	}
	fmt.Println(string(out))
}

func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }
