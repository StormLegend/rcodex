package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/StormLegend/rcodex/gateway-go/internal/relay"
)

func main() {
	file := flag.String("config", "relay.json", "relay config file")
	flag.Parse()
	b, err := os.ReadFile(*file)
	if err != nil {
		fail(err)
	}
	var cfg relay.ServerConfig
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&cfg); err != nil {
		fail(err)
	}
	if err = cfg.Validate(); err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = relay.NewServer(cfg).ListenAndServe(ctx); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rcg-relay:", err)
	os.Exit(2)
}
