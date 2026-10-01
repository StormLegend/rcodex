package engine

import (
	"context"
	"errors"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/runtime"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
	"log/slog"
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
	e.wg.Add(1)
	go e.loop(ctx)
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
	if err == nil {
		rt := e.Runtimes[s.Runtime]
		if rt == nil {
			err = errors.New("runtime unavailable")
		} else {
			rs, er := rt.Start(ctx, s.Workspace, s.Model, s.Mode, nil)
			if er != nil {
				err = er
			} else {
				_, err = rt.Turn(ctx, rs, t.Prompt, func(ev runtime.Event) error {
					_, err := e.Store.Event(t.SessionID, t.ID, ev.Method, ev.Params)
					return err
				})
			}
		}
	}
	if err = e.Store.Finish(t, "", err); err != nil {
		e.Log.Error("turn finish failed", "error", err)
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
