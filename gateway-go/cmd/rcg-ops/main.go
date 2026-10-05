package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/StormLegend/rcodex/gateway-go/internal/ops"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config", "targets.json", "independent gateway targets")
	command := flag.String("command", "status", "status|select|current|watch")
	target := flag.String("target", "", "target to select")
	reason := flag.String("reason", "operator selection", "selection reason")
	interval := flag.Duration("interval", 30*time.Second, "watch interval (minimum 1s)")
	alertURL := flag.String("alert-url", "", "optional HTTPS webhook for health transitions")
	flag.Parse()
	c, err := ops.Load(*path)
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	out := json.NewEncoder(os.Stdout)
	switch *command {
	case "status":
		_ = out.Encode(c.Check(ctx))
	case "current":
		s, err := c.Current()
		if err != nil {
			fail(err)
		}
		_ = out.Encode(s)
	case "select":
		s, err := c.Select(ctx, *target, *reason)
		if err != nil {
			fail(err)
		}
		_ = out.Encode(s)
	case "watch":
		if *interval < time.Second {
			fail(fmt.Errorf("interval must be at least 1s"))
		}
		trackers := map[string]*ops.Tracker{}
		tick := time.NewTicker(*interval)
		defer tick.Stop()
		for {
			for _, h := range c.Check(ctx) {
				t := trackers[h.Name]
				if t == nil {
					t = &ops.Tracker{}
					trackers[h.Name] = t
				}
				changed := t.Observe(h.Healthy, c.FailureThreshold, c.RecoveryThreshold)
				_ = out.Encode(map[string]any{"health": h, "degraded": t.Degraded, "transition": changed})
				if changed && *alertURL != "" {
					sendAlert(ctx, *alertURL, map[string]any{"health": h, "degraded": t.Degraded})
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	default:
		fail(fmt.Errorf("unknown command"))
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func sendAlert(ctx context.Context, endpoint string, value any) {
	b, _ := json.Marshal(value)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
