package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
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
	Effort  string
}
type adapter struct {
	cmd                      string
	args                     []string
	mu                       sync.Mutex
	writeMu                  sync.Mutex
	gate                     chan struct{}
	proc                     *exec.Cmd
	procStop                 context.CancelFunc
	procDone                 chan struct{}
	stdin                    io.WriteCloser
	stdout                   io.ReadCloser
	next                     int64
	scanner                  *bufio.Scanner
	pending                  [][]byte
	activeThread, activeTurn string
}

func (a *adapter) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	a.mu.Lock()
	in := a.stdin
	a.mu.Unlock()
	if in == nil {
		return errors.New("runtime is not running")
	}
	_, err = in.Write(append(b, '\n'))
	return err
}

// begin serializes app-server operations, but allows a waiting caller to
// cancel. A cancelled active operation tears down the stalled process and
// unblocks pipe reads. Native threads can be resumed by the next operation.
func (a *adapter) begin(ctx context.Context) (func(), error) {
	select {
	case a.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-a.gate
		return nil, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = a.Stop(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		<-a.gate
	}, nil
}

func (a *adapter) read(ctx context.Context, queued bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if queued && len(a.pending) > 0 {
		b := a.pending[0]
		a.pending = a.pending[1:]
		return b, nil
	}
	a.mu.Lock()
	sc := a.scanner
	a.mu.Unlock()
	if sc == nil || !sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if sc != nil && sc.Err() != nil {
			return nil, sc.Err()
		}
		return nil, errors.New("runtime exited")
	}
	return append([]byte(nil), sc.Bytes()...), nil
}

func (a *adapter) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a.next++
	id := a.next
	if err := a.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	for {
		b, err := a.read(ctx, false)
		if err != nil {
			return nil, err
		}
		var msg struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(b, &msg) != nil {
			continue
		}
		if msg.Method != "" {
			if len(a.pending) >= 256 {
				return nil, errors.New("too many runtime events before response")
			}
			a.pending = append(a.pending, b)
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

func (a *adapter) startProcess(ctx context.Context, cwd string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc != nil {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.stdout != nil {
		_ = a.stdout.Close()
	}
	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, a.cmd, a.args...)
	cmd.Dir = cwd
	configureCommand(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return false, err
	}
	// An explicit pipe is not closed by cmd.Wait. Drain the complete protocol
	// stream even when the child exits immediately after its final notification.
	out, writer, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		cancel()
		return false, err
	}
	cmd.Stdout = writer
	cmd.Stderr = io.Discard
	cmd.Cancel = func() error { err := killCommandGroup(cmd); _ = out.Close(); _ = in.Close(); return err }
	if err = cmd.Start(); err != nil {
		cancel()
		_ = in.Close()
		_ = out.Close()
		_ = writer.Close()
		return false, err
	}
	_ = writer.Close()
	a.proc, a.procStop, a.stdin, a.stdout = cmd, cancel, in, out
	done := make(chan struct{})
	a.procDone = done
	a.pending = nil
	a.scanner = bufio.NewScanner(out)
	a.scanner.Buffer(make([]byte, 64<<10), 16<<20)
	go func() {
		_ = cmd.Wait()
		cancel()
		a.mu.Lock()
		if a.proc == cmd {
			a.proc = nil
			a.stdin = nil
			a.procStop = nil
		}
		a.mu.Unlock()
		close(done)
	}()
	return true, nil
}

func (a *adapter) Stop() error {
	a.mu.Lock()
	cmd, cancel, done, in, out := a.proc, a.procStop, a.procDone, a.stdin, a.stdout
	a.proc = nil
	a.procStop = nil
	a.procDone = nil
	a.stdin = nil
	a.stdout = nil
	a.scanner = nil
	a.mu.Unlock()
	if in != nil {
		_ = in.Close()
	}
	if out != nil {
		_ = out.Close()
	}
	if cancel != nil {
		cancel()
	}
	if cmd != nil {
		_ = killCommandGroup(cmd)
	}
	if done != nil {
		<-done
	}
	return nil
}

func (a *adapter) ensureProcess(ctx context.Context, cwd string) error {
	fresh, err := a.startProcess(ctx, cwd)
	if err != nil || !fresh {
		return err
	}
	if _, err = a.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "rcodex-go", "version": "0.2"}}); err != nil {
		_ = a.Stop()
		return err
	}
	return a.send(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
}

// Codex JSON-RPC adapter. It keeps protocol framing separate from session policy.
type Codex struct{ adapter }

func NewCodex(command string) Runtime {
	return &Codex{adapter{cmd: command, args: []string{"app-server"}, gate: make(chan struct{}, 1)}}
}
func (a *Codex) Start(ctx context.Context, cwd, model, mode string, h Handler) (Session, error) {
	end, e := a.begin(ctx)
	if e != nil {
		return Session{}, e
	}
	defer end()
	if e = a.ensureProcess(ctx, cwd); e != nil {
		return Session{}, e
	}
	settings := permissionSettings(cwd, mode)
	params := map[string]any{"cwd": cwd, "sandbox": settings.Sandbox, "approvalPolicy": settings.ApprovalPolicy, "approvalsReviewer": settings.ApprovalsReviewer}
	if model != "" {
		params["model"] = model
	}
	threadRaw, e := a.request(ctx, "thread/start", params)
	if e != nil {
		return Session{}, e
	}
	var response struct {
		Model  string `json:"model"`
		Effort string `json:"reasoningEffort"`
		Thread struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"thread"`
	}
	if json.Unmarshal(threadRaw, &response) != nil || response.Thread.ID == "" {
		return Session{}, errors.New("codex did not return thread id")
	}
	if response.Model == "" {
		response.Model = response.Thread.Model
	}
	if response.Model == "" {
		response.Model = model
	}
	return Session{ID: response.Thread.ID, Runtime: "codex", CWD: cwd, Model: response.Model, Mode: mode, Effort: response.Effort}, nil
}
func (a *Codex) Resume(ctx context.Context, s Session, h Handler) (Session, error) {
	end, e := a.begin(ctx)
	if e != nil {
		return s, e
	}
	defer end()
	if e = a.ensureProcess(ctx, s.CWD); e != nil {
		return s, e
	}
	settings := permissionSettings(s.CWD, s.Mode)
	params := map[string]any{"threadId": s.ID, "cwd": s.CWD, "approvalPolicy": settings.ApprovalPolicy, "approvalsReviewer": settings.ApprovalsReviewer, "sandbox": settings.Sandbox}
	if s.Model != "" {
		params["model"] = s.Model
	}
	raw, e := a.request(ctx, "thread/resume", params)
	if e != nil {
		return s, e
	}
	var response struct {
		Model  string `json:"model"`
		Effort string `json:"reasoningEffort"`
	}
	if e = json.Unmarshal(raw, &response); e != nil {
		return s, e
	}
	if s.Model == "" {
		s.Model = response.Model
	}
	if s.Effort == "" {
		s.Effort = response.Effort
	}
	return s, nil
}
func (a *Codex) Turn(ctx context.Context, s Session, prompt string, h Handler) (string, error) {
	end, err := a.begin(ctx)
	if err != nil {
		return "", err
	}
	defer end()
	complete := false
	defer func() {
		if !complete {
			_ = a.Stop()
		}
		a.mu.Lock()
		a.activeThread = ""
		a.activeTurn = ""
		a.mu.Unlock()
	}()
	settings := permissionSettings(s.CWD, s.Mode)
	params := map[string]any{"threadId": s.ID, "cwd": s.CWD, "approvalPolicy": settings.ApprovalPolicy, "approvalsReviewer": settings.ApprovalsReviewer, "sandboxPolicy": settings.SandboxPolicy, "input": []map[string]any{{"type": "text", "text": prompt, "text_elements": []string{}}}}
	if s.Model != "" {
		params["model"] = s.Model
	}
	if s.Effort != "" {
		params["effort"] = s.Effort
	}
	raw, err := a.request(ctx, "turn/start", params)
	if err != nil {
		return "", err
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err = json.Unmarshal(raw, &started); err != nil || started.Turn.ID == "" {
		return "", errors.New("codex did not return turn id")
	}
	a.mu.Lock()
	a.activeThread = s.ID
	a.activeTurn = started.Turn.ID
	a.mu.Unlock()
	resultText := ""
	for {
		b, err := a.read(ctx, true)
		if err != nil {
			return "", err
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(b, &msg) == nil && msg.Method != "" {
			var scope struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			if json.Unmarshal(msg.Params, &scope) == nil && (scope.ThreadID != "" && scope.ThreadID != s.ID || scope.TurnID != "" && scope.TurnID != started.Turn.ID) {
				continue
			}
			if msg.Method == "item/agentMessage/delta" {
				var delta struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(msg.Params, &delta) == nil {
					resultText += delta.Delta
				}
			}
			if msg.Method == "item/completed" && resultText == "" {
				var completed struct {
					Item struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				}
				if json.Unmarshal(msg.Params, &completed) == nil && completed.Item.Type == "agentMessage" {
					resultText = completed.Item.Text
				}
			}
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
			if msg.Method == "turn/completed" {
				var finished struct {
					ThreadID string `json:"threadId"`
					Turn     struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					} `json:"turn"`
				}
				if json.Unmarshal(msg.Params, &finished) != nil {
					return "", errors.New("invalid codex completion")
				}
				if finished.ThreadID != s.ID || finished.Turn.ID != started.Turn.ID {
					continue
				}
				switch finished.Turn.Status {
				case "completed":
					complete = true
					return resultText, nil
				case "interrupted":
					return "", context.Canceled
				default:
					return resultText, errors.New("codex reported an unsuccessful turn")
				}
			}
		}
	}
}

type codexSettings struct {
	ApprovalPolicy, ApprovalsReviewer, Sandbox string
	SandboxPolicy                              map[string]any
}

func permissionSettings(cwd, mode string) codexSettings {
	if mode == "readonly" {
		return codexSettings{"never", "user", "read-only", map[string]any{"type": "readOnly"}}
	}
	if mode == "full" {
		return codexSettings{"never", "user", "danger-full-access", map[string]any{"type": "dangerFullAccess"}}
	}
	return codexSettings{"on-request", map[bool]string{true: "auto_review", false: "user"}[mode == "auto"], "workspace-write", map[string]any{"type": "workspaceWrite", "writableRoots": []string{cwd}, "networkAccess": false, "excludeTmpdirEnvVar": false, "excludeSlashTmp": false}}
}
func (a *Codex) Interrupt(ctx context.Context, s Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	thread, turn := a.activeThread, a.activeTurn
	a.mu.Unlock()
	if thread != s.ID || turn == "" {
		return nil
	}
	return a.send(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "turn/interrupt", "params": map[string]any{"threadId": thread, "turnId": turn}})
}

// Claude Code uses the stable stream-json CLI protocol. No shell is involved.
type Claude struct {
	command string
	mu      sync.Mutex
	procs   map[string]*exec.Cmd
	started map[string]bool
}

func NewClaude(command string) Runtime {
	return &Claude{command: command, procs: map[string]*exec.Cmd{}, started: map[string]bool{}}
}
func (a *Claude) Start(ctx context.Context, cwd, model, mode string, h Handler) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if cwd == "" {
		return Session{}, errors.New("claude cwd required")
	}
	if _, err := os.Stat(cwd); err != nil {
		return Session{}, err
	}
	id, err := claudeSessionID()
	if err != nil {
		return Session{}, err
	}
	a.mu.Lock()
	a.started[id] = true
	a.mu.Unlock()
	return Session{ID: id, Runtime: "claude", CWD: cwd, Model: model, Mode: mode}, nil
}
func (a *Claude) Resume(ctx context.Context, s Session, h Handler) (Session, error) {
	if s.ID == "" || s.CWD == "" {
		return Session{}, errors.New("claude session id and cwd required")
	}
	if _, err := os.Stat(s.CWD); err != nil {
		return Session{}, err
	}
	a.mu.Lock()
	a.started[s.ID] = false
	a.mu.Unlock()
	return s, nil
}
func (a *Claude) Turn(ctx context.Context, s Session, prompt string, h Handler) (string, error) {
	if s.ID == "" {
		return "", errors.New("claude session id required")
	}
	if strings.EqualFold(s.CWD, "") {
		return "", errors.New("claude cwd required")
	}
	args := []string{"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose"}
	a.mu.Lock()
	isNew := a.started[s.ID]
	if isNew {
		args = append(args, "--session-id", s.ID)
	} else {
		args = append(args, "--resume", s.ID)
	}
	delete(a.started, s.ID)
	a.mu.Unlock()
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.Effort != "" {
		args = append(args, "--effort", s.Effort)
	}
	args = append(args, claudePermissionArgs(s.Mode)...)
	cmd := exec.CommandContext(ctx, a.command, args...)
	cmd.Dir = s.CWD
	cmd.Stderr = io.Discard
	configureCommand(cmd)
	in, e := cmd.StdinPipe()
	if e != nil {
		return "", e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		_ = in.Close()
		return "", e
	}
	cmd.Cancel = func() error {
		err := killCommandGroup(cmd)
		// A descendant can inherit stdout even after the CLI process exits.
		// Unblock the scanner on every supported platform as well.
		_ = out.Close()
		return err
	}
	if e = cmd.Start(); e != nil {
		_ = in.Close()
		_ = out.Close()
		return "", e
	}
	waited := false
	defer func() {
		_ = in.Close()
		_ = out.Close()
		if !waited {
			_ = killCommandGroup(cmd)
			_ = cmd.Wait()
		}
	}()
	a.mu.Lock()
	a.procs[s.ID] = cmd
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.procs[s.ID] == cmd {
			delete(a.procs, s.ID)
		}
		a.mu.Unlock()
	}()
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}})
	_, e = in.Write(append(b, '\n'))
	if e != nil {
		return "", e
	}
	_ = in.Close()
	result := ""
	resultSeen := false
	var resultErr error
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var v map[string]any
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		if x, ok := v["result"].(string); ok {
			result = x
			resultSeen = true
		}
		if v["type"] == "result" {
			resultSeen = true
			if failed, _ := v["is_error"].(bool); failed || strings.HasPrefix(fmt.Sprint(v["subtype"]), "error_") {
				resultErr = errors.New("claude reported an unsuccessful result")
			}
		}
		if h != nil {
			b, _ := json.Marshal(v)
			if e := h(Event{Method: "claude/event", Params: b}); e != nil {
				return "", e
			}
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if e := sc.Err(); e != nil {
		return "", e
	}
	waitErr := cmd.Wait()
	waited = true
	// CommandContext reaps the child on cancellation. Preserve the context
	// error so the engine records cancelled/timed_out instead of the platform
	// specific "signal: killed" process error.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if waitErr != nil {
		return result, waitErr
	}
	if resultErr != nil {
		return result, resultErr
	}
	if !resultSeen {
		return "", errors.New("claude exited without a result")
	}
	return result, nil
}
func (a *Claude) Interrupt(ctx context.Context, s Session) error { return a.interrupt(s) }
func (a *Claude) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var first error
	for id, p := range a.procs {
		if err := p.Cancel(); err != nil && first == nil {
			first = err
		}
		delete(a.procs, id)
	}
	return first
}

func (a *Claude) interrupt(s Session) error {
	a.mu.Lock()
	p := a.procs[s.ID]
	a.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Cancel()
}

func claudeSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func claudePermissionArgs(mode string) []string {
	switch mode {
	case "readonly":
		return []string{"--permission-mode", "plan"}
	case "auto":
		return []string{"--permission-mode", "acceptEdits"}
	case "full":
		return []string{"--permission-mode", "bypassPermissions", "--dangerously-skip-permissions"}
	default:
		return []string{"--permission-mode", "default"}
	}
}
