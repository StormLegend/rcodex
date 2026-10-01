package relay

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/hashicorp/yamux"
)

func TestAuthenticatedRelayHandshake(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	m := New(config.Relay{URL: "ws://127.0.0.1:1", ID: "node-1", Token: "secret-token-012345678901234567890"})
	m.Dial = func(context.Context, string) (net.Conn, error) { return clientConn, nil }
	go func() {
		s, err := yamux.Server(serverConn, nil)
		if err != nil {
			return
		}
		stream, err := s.Accept()
		if err != nil {
			return
		}
		defer stream.Close()
		var hello Hello
		if json.NewDecoder(stream).Decode(&hello) != nil {
			return
		}
		_ = json.NewEncoder(stream).Encode(HelloResult{OK: hello.ID == "node-1" && hello.Token == "secret-token-012345678901234567890"})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := m.ConnectAuthenticated(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}
