package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/StormLegend/rcodex/gateway-go/internal/engine"
	"github.com/StormLegend/rcodex/gateway-go/internal/relay"
	"github.com/StormLegend/rcodex/gateway-go/internal/store"
	"github.com/StormLegend/rcodex/gateway-go/internal/web"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	file := flag.String("config", "gateway.json", "config file")
	doctor := flag.Bool("doctor", false, "validate and check database")
	backup := flag.String("backup", "", "write SQLite backup")
	flag.Parse()
	c, e := config.Load(*file)
	if e != nil {
		fmt.Fprintln(os.Stderr, "config:", e)
		os.Exit(2)
	}
	s, e := store.Open(c.DataDir + "/gateway.db")
	if e != nil {
		fmt.Fprintln(os.Stderr, "database:", e)
		os.Exit(2)
	}
	defer s.DB.Close()
	if *doctor {
		if e = s.Check(); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		fmt.Println("ok", s.Stats())
		return
	}
	if *backup != "" {
		if e = s.Backup(*backup); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		fmt.Println(*backup)
		return
	}
	// Only a serving process owns recovery. Maintenance may inspect or back up
	// the database of a live gateway without cancelling its active work.
	if e = s.Recover(); e != nil {
		fmt.Fprintln(os.Stderr, "recovery:", e)
		os.Exit(2)
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	eng := engine.New(s, c, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	eng.Start(ctx)
	defer eng.Stop()
	server := &web.Server{Cfg: c, Store: s, Engine: eng, Log: log}
	if c.Relay.URL != "" {
		rm := relay.New(c.Relay)
		go func() {
			backoff := 250 * time.Millisecond
			for ctx.Err() == nil {
				session, err := rm.ConnectRetry(ctx)
				if err != nil {
					if ctx.Err() == nil {
						log.Warn("relay connect failed", "error", err)
					}
					return
				}
				log.Info("relay connected", "id", c.Relay.ID)
				serveCtx, cancel := context.WithCancel(ctx)
				done := make(chan error, 1)
				go func() { done <- relay.ServeHTTP(serveCtx, session, server.Handler()) }()
				select {
				case <-ctx.Done():
					cancel()
					_ = session.Close()
					return
				case err := <-done:
					cancel()
					_ = session.Close()
					if ctx.Err() == nil && err != nil {
						log.Warn("relay connection lost", "error", err)
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff < 10*time.Second {
					backoff *= 2
				}
			}
		}()
	}
	fmt.Println("rcodex-go listening", c.Listen)
	if e = web.Run(ctx, server); e != nil && e.Error() != "http: Server closed" {
		log.Error("server stopped", "error", e)
	}
}
