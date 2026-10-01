package engine

import (
	"context"
	"encoding/json"
	"errors"
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
}

func New(s *store.Store, c config.Config, log *slog.Logger) *Engine {
	return &Engine{Store: s, Config: c, Log: log, Runtimes: map[string]runtime.Runtime{"codex": runtime.NewCodex(c.CodexCommand), "claude": runtime.NewClaude(c.ClaudeCommand)}}
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
				rs = runtime.Session{ID: s.NativeID, Runtime: s.Runtime, CWD: s.Workspace, Model: s.Model, Mode: s.Mode}
				rs, er = rt.Resume(ctx, rs, nil)
			} else {
				rs, er = rt.Start(ctx, s.Workspace, s.Model, s.Mode, nil)
			}
			if er != nil {
				err = er
			} else {
				if rs.ID != "" {
					_ = e.Store.Native(s.ID, rs.ID)
				}
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
	if err = e.Store.Finish(t, result, err); err != nil {
		e.Log.Error("turn finish failed", "error", err)
	}
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
