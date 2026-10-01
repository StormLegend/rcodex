package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Event struct {
	ID           json.RawMessage
	Method       string
	Params       json.RawMessage
	Respond      func(any) error
	RespondError func(int, string) error
}
type Request struct {
	ID     any
	Method string
	Params json.RawMessage
}
type Handler func(Event) error
type Runtime interface {
	Start(context.Context, string, string, string, Handler) (Session, error)
	Resume(context.Context, Session, Handler) (Session, error)
	Turn(context.Context, Session, string, Handler) (string, error)
	Interrupt(context.Context, Session) error
	Stop() error
}
type Session struct {
	ID      string
	Runtime string
	CWD     string
	Model   string
	Mode    string
}
type adapter struct {
	cmd     string
	args    []string
	mu      sync.Mutex
	runMu   sync.Mutex
	proc    *exec.Cmd
	stdin   io.WriteCloser
	next    int64
	scanner *bufio.Scanner
}

func (a *adapter) send(v any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stdin == nil {
		return errors.New("runtime is not running")
	}
	b, _ := json.Marshal(v)
	_, e := a.stdin.Write(append(b, '\n'))
	return e
}
func (a *adapter) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a.mu.Lock()
	a.next++
	id := a.next
	a.mu.Unlock()
	if e := a.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); e != nil {
		return nil, e
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if a.scanner == nil || !a.scanner.Scan() {
			return nil, errors.New("runtime exited")
		}
		var msg struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(a.scanner.Bytes(), &msg) != nil {
			continue
		}
		if msg.Method != "" {
			continue
		}
		if fmt.Sprint(msg.ID) != fmt.Sprint(id) {
			continue
		}
		if len(msg.Error) > 0 && string(msg.Error) != "null" {
			return nil, fmt.Errorf("%s failed: %s", method, msg.Error)
		}
		return msg.Result, nil
	}
}
func (a *adapter) startProcess(ctx context.Context, cwd string) (*bufio.Scanner, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc != nil {
		return nil, nil
	}
	a.proc = exec.CommandContext(ctx, a.cmd, a.args...)
	a.proc.Dir = cwd
	a.proc.Env = os.Environ()
	in, e := a.proc.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := a.proc.StdoutPipe()
	if e != nil {
		return nil, e
	}
	a.stdin = in
	if e = a.proc.Start(); e != nil {
		return nil, e
	}
	a.scanner = bufio.NewScanner(out)
	return a.scanner, nil
}
func (a *adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil {
		return nil
	}
	e := a.proc.Process.Kill()
	a.proc = nil
	a.stdin = nil
	a.scanner = nil
	return e
}

// Codex JSON-RPC adapter. It keeps protocol framing separate from session policy.
type Codex struct{ adapter }

func NewCodex(command string) Runtime {
	return &Codex{adapter{cmd: command, args: []string{"app-server"}}}
}
func (a *Codex) Start(ctx context.Context, cwd, model, mode string, h Handler) (Session, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	_, e := a.startProcess(ctx, cwd)
	if e != nil {
		return Session{}, e
	}
	if _, e = a.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "rcodex-go", "version": "0.1"}}); e != nil {
		return Session{}, e
	}
	settings := permissionSettings(cwd, mode)
	threadRaw, e := a.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": model, "sandbox": settings.Sandbox, "approvalPolicy": settings.ApprovalPolicy, "approvalsReviewer": settings.ApprovalsReviewer})
	if e != nil {
		return Session{}, e
	}
	var response struct {
		Thread struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"thread"`
	}
	if json.Unmarshal(threadRaw, &response) != nil || response.Thread.ID == "" {
		return Session{}, errors.New("codex did not return thread id")
	}
	return Session{ID: response.Thread.ID, Runtime: "codex", CWD: cwd, Model: response.Thread.Model, Mode: mode}, nil
}
func (a *Codex) Resume(ctx context.Context, s Session, h Handler) (Session, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	settings := permissionSettings(s.CWD, s.Mode)
	_, e := a.request(ctx, "thread/resume", map[string]any{"threadId": s.ID, "cwd": s.CWD, "model": s.Model, "approvalPolicy": settings.ApprovalPolicy, "approvalsReviewer": settings.ApprovalsReviewer, "sandbox": settings.Sandbox})
	return s, e
}
func (a *Codex) Turn(ctx context.Context, s Session, prompt string, h Handler) (string, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	settings := permissionSettings(s.CWD, s.Mode)
	if _, e := a.request(ctx, "turn/start", map[string]any{"threadId": s.ID, "cwd": s.CWD, "model": s.Model, "approvalPolicy": settings.ApprovalPolicy, "approvalsReviewer": settings.ApprovalsReviewer, "sandboxPolicy": settings.SandboxPolicy, "input": []map[string]any{{"type": "text", "text": prompt, "text_elements": []string{}}}}); e != nil {
		return "", e
	}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		if a.scanner == nil || !a.scanner.Scan() {
			return "", errors.New("codex exited")
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(a.scanner.Bytes(), &msg) == nil && msg.Method != "" {
			if h != nil {
				ev := Event{ID: msg.ID, Method: msg.Method, Params: msg.Params}
				if len(msg.ID) > 0 && string(msg.ID) != "null" {
					ev.Respond = func(v any) error {
						return a.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": v})
					}
					ev.RespondError = func(code int, message string) error {
						return a.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "error": map[string]any{"code": code, "message": message}})
					}
				}
				if e := h(ev); e != nil {
					return "", e
				}
			}
			if msg.Method == "turn/completed" || msg.Method == "thread/status/changed" && strings.Contains(string(msg.Params), `"idle"`) {
				return fmt.Sprintf("turn-%d", time.Now().UnixNano()), nil
			}
		}
	}
}

type codexSettings struct {
	ApprovalPolicy, ApprovalsReviewer, Sandbox string
	SandboxPolicy                              map[string]any
}

func permissionSettings(cwd, mode string) codexSettings {
	if mode == "full" {
		return codexSettings{"never", "user", "danger-full-access", map[string]any{"type": "dangerFullAccess"}}
	}
	return codexSettings{"on-request", map[bool]string{true: "auto_review", false: "user"}[mode == "auto"], "workspace-write", map[string]any{"type": "workspaceWrite", "writableRoots": []string{cwd}, "networkAccess": false, "excludeTmpdirEnvVar": false, "excludeSlashTmp": false}}
}
func (a *Codex) Interrupt(ctx context.Context, s Session) error {
	return a.send(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "turn/interrupt", "params": map[string]any{"threadId": s.ID}})
}

// Claude Code uses the stable stream-json CLI protocol. No shell is involved.
type Claude struct {
	command string
	mu      sync.Mutex
	procs   map[string]*exec.Cmd
}

func NewClaude(command string) Runtime {
	return &Claude{command: command, procs: map[string]*exec.Cmd{}}
}
func (a *Claude) Start(ctx context.Context, cwd, model, mode string, h Handler) (Session, error) {
	return Session{Runtime: "claude", CWD: cwd, Model: model}, nil
}
func (a *Claude) Resume(ctx context.Context, s Session, h Handler) (Session, error) { return s, nil }
func (a *Claude) Turn(ctx context.Context, s Session, prompt string, h Handler) (string, error) {
	args := []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose"}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if strings.EqualFold(s.CWD, "") {
		return "", errors.New("claude cwd required")
	}
	cmd := exec.CommandContext(ctx, a.command, args...)
	cmd.Dir = s.CWD
	in, e := cmd.StdinPipe()
	if e != nil {
		return "", e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		return "", e
	}
	if e = cmd.Start(); e != nil {
		return "", e
	}
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
	_, e = in.Write(append(b, '\n'))
	if e != nil {
		return "", e
	}
	_ = in.Close()
	result := ""
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		var v map[string]any
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		if x, ok := v["result"].(string); ok && x != "" {
			result = x
		}
		if h != nil {
			b, _ := json.Marshal(v)
			if e := h(Event{Method: "claude/event", Params: b}); e != nil {
				_ = cmd.Process.Kill()
				return "", e
			}
		}
	}
	if e := sc.Err(); e != nil {
		_ = cmd.Process.Kill()
		return "", e
	}
	if e := cmd.Wait(); e != nil {
		return result, e
	}
	return result, nil
}
func (a *Claude) Interrupt(ctx context.Context, s Session) error { return nil }
func (a *Claude) Stop() error                                    { return nil }
