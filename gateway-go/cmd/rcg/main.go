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
	if e = s.Recover(); e != nil {
		fmt.Fprintln(os.Stderr, "recovery:", e)
		os.Exit(2)
	}
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
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	eng := engine.New(s, c, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	eng.Start(ctx)
	defer eng.Stop()
	if c.Relay.URL != "" {
		rm := relay.New(c.Relay)
		go func() {
			session, err := rm.ConnectRetry(ctx)
			if err != nil {
				log.Error("relay stopped", "error", err)
				return
			}
			log.Info("relay connected", "id", c.Relay.ID)
			<-ctx.Done()
			_ = session.Close()
		}()
	}
	fmt.Println("rcodex-go listening", c.Listen)
	if e = web.Run(ctx, &web.Server{Cfg: c, Store: s, Engine: eng, Log: log}); e != nil && e.Error() != "http: Server closed" {
		log.Error("server stopped", "error", e)
	}
}
