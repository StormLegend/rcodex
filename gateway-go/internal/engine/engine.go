package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/runtime"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	Store    *store.Store
	Config   config.Config
	Log      *slog.Logger
	Runtimes map[string]runtime.Runtime
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	activeMu sync.Mutex
	active   map[string]context.CancelFunc
}

func New(s *store.Store, c config.Config, log *slog.Logger) *Engine {
	return &Engine{Store: s, Config: c, Log: log, Runtimes: map[string]runtime.Runtime{"codex": runtime.NewCodex(c.CodexCommand), "claude": runtime.NewClaude(c.ClaudeCommand)}, active: map[string]context.CancelFunc{}}
}
func (e *Engine) Start(ctx context.Context) {
	ctx, e.cancel = context.WithCancel(ctx)
	workers := e.Config.Workers
	if workers < 1 {
		workers = 1
	}
	e.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go e.loop(ctx)
	}
	e.wg.Add(1)
	go e.scheduleLoop(ctx)
}
func (e *Engine) loop(ctx context.Context) {
	defer e.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t, err := e.Store.Claim()
		if err != nil {
			time.Sleep(40 * time.Millisecond)
			continue
		}
		e.run(ctx, t)
	}
}
func (e *Engine) run(parent context.Context, t store.Turn) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(e.Config.TurnSeconds)*time.Second)
	defer cancel()
	e.activeMu.Lock()
	e.active[t.ID] = cancel
	e.activeMu.Unlock()
	defer func() {
		e.activeMu.Lock()
		delete(e.active, t.ID)
		e.activeMu.Unlock()
	}()
	s, err := e.Store.Session(t.SessionID)
	result := ""
	if err == nil {
		rt := e.Runtimes[s.Runtime]
		if rt == nil {
			err = errors.New("runtime unavailable")
		} else {
			var rs runtime.Session
			var er error
			if s.NativeID != "" {
				rs = runtime.Session{ID: s.NativeID, Runtime: s.Runtime, CWD: s.Workspace, Model: s.Model, Mode: s.Mode, Effort: s.Effort}
				rs, er = rt.Resume(ctx, rs, nil)
			} else {
				rs, er = rt.Start(ctx, s.Workspace, s.Model, s.Mode, nil)
			}
			if s.Effort != "" {
				rs.Effort = s.Effort
			}
			if er != nil {
				err = er
			} else {
				if rs.ID != "" {
					err = e.Store.RecordRuntimeSession(s.ID, rs.ID, rs.Model, rs.Effort)
				}
				if err == nil {
					result, err = rt.Turn(ctx, rs, t.Prompt, func(ev runtime.Event) error {
						_, err := e.Store.Event(t.SessionID, t.ID, ev.Method, ev.Params)
						if err != nil {
							return err
						}
						return e.handleEvent(ctx, t, s, ev)
					})
				}
			}
		}
	}
	if err = e.Store.Finish(t, result, err); err != nil {
		e.Log.Error("turn finish failed", "error", err)
	}
}

// Cancel stops a running runtime process or marks a queued turn cancelled. It
// never requeues a partially executed turn, because doing so could duplicate
// external side effects.
func (e *Engine) Cancel(turnID string) error {
	for i := 0; i < 10; i++ {
		e.activeMu.Lock()
		cancel := e.active[turnID]
		e.activeMu.Unlock()
		if cancel != nil {
			cancel()
			return nil
		}
		t, err := e.Store.Turn(turnID)
		if err != nil {
			return err
		}
		if t.State != "running" {
			return e.Store.CancelQueued(turnID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return store.ErrBusy
}

func (e *Engine) handleEvent(ctx context.Context, t store.Turn, s store.Session, ev runtime.Event) error {
	if ev.Respond == nil {
		return nil
	}
	approval := strings.HasSuffix(ev.Method, "/requestApproval") || strings.HasSuffix(ev.Method, "Approval")
	question := ev.Method == "item/tool/requestUserInput"
	if !approval && !question {
		return nil
	}
	a, err := e.Store.NewApproval(t, map[string]any{"method": ev.Method, "params": json.RawMessage(ev.Params), "kind": map[bool]string{true: "question", false: "approval"}[question]})
	if err != nil {
		return err
	}
	if _, err = e.Store.Event(t.SessionID, t.ID, "approval.requested", map[string]any{"id": a.ID, "kind": map[bool]string{true: "question", false: "approval"}[question], "method": ev.Method, "request": json.RawMessage(ev.Params)}); err != nil {
		return err
	}
	if !question && (s.Mode == "auto" || s.Mode == "full") {
		if err = e.Store.Resolve(a.ID, map[string]any{"approved": true, "reason": "session permission mode"}); err != nil {
			return err
		}
		return ev.Respond(map[string]string{"decision": "accept"})
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		resolved, err := e.Store.Approval(a.ID)
		if err != nil {
			return err
		}
		if resolved.State != "resolved" {
			continue
		}
		if question {
			var v struct {
				Answers map[string]any `json:"answers"`
			}
			if json.Unmarshal(resolved.Response, &v) != nil {
				return ev.RespondError(-32602, "invalid question response")
			}
			return ev.Respond(map[string]any{"answers": v.Answers})
		}
		var v struct {
			Approved bool `json:"approved"`
		}
		if json.Unmarshal(resolved.Response, &v) != nil {
			return ev.RespondError(-32602, "invalid approval response")
		}
		decision := "decline"
		if v.Approved {
			decision = "accept"
		}
		return ev.Respond(map[string]string{"decision": decision})
	}
}
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	for _, r := range e.Runtimes {
		_ = r.Stop()
	}
}

func (e *Engine) scheduleLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			items, err := e.Store.DueSchedules(time.Now().UnixMilli(), 20)
			if err != nil {
				e.Log.Error("schedule scan failed", "error", err)
				continue
			}
			for _, item := range items {
				interval, once, parseErr := ParseScheduleInterval(item.Expression)
				if parseErr != nil {
					_ = e.Store.UpdateScheduleNext(item.ID, item.NextAt, true)
					e.Log.Warn("schedule paused", "id", item.ID, "error", parseErr)
					continue
				}
				_, enqueueErr := e.Store.Enqueue(item.SessionID, item.Prompt, "schedule:"+item.ID+":"+fmt.Sprint(item.NextAt), "", e.Config.QueueLimit)
				if enqueueErr != nil && !errors.Is(enqueueErr, store.ErrBusy) {
					e.Log.Warn("schedule enqueue failed", "id", item.ID, "error", enqueueErr)
				}
				if once || interval <= 0 {
					_ = e.Store.UpdateScheduleNext(item.ID, item.NextAt, true)
				} else {
					_ = e.Store.UpdateScheduleNext(item.ID, time.Now().Add(interval).UnixMilli(), false)
				}
			}
		}
	}
}

func ParseScheduleInterval(expression string) (time.Duration, bool, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" || expression == "once" {
		return 0, true, nil
	}
	expression = strings.TrimPrefix(expression, "every:")
	expression = strings.TrimSpace(strings.TrimPrefix(expression, "@every"))
	d, err := time.ParseDuration(expression)
	if err != nil || d < time.Second {
		return 0, false, errors.New("schedule expression must be once or a duration of at least 1s")
	}
	return d, false, nil
}
