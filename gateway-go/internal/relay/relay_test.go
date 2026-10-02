package relay

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/hashicorp/yamux"
)

func TestRelayForwardsMobileHTTPToGateway(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(ServerConfig{Listen: ln.Addr().String(), Peers: map[string]Peer{
		"gw-1": {ConnectorToken: "connector-token-012345678901234567890123", AccessToken: "access-token-012345678901234567890123"},
	}})
	go func() { _ = server.ServeListener(ctx, ln) }()
	dial := func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	}
	agent := New(config.Relay{URL: "ws://127.0.0.1:1", ID: "gw-1", Token: "connector-token-012345678901234567890123"})
	agent.Dial = dial
	agentSession, err := agent.ConnectAuthenticated(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer agentSession.Close()
	go func() {
		_ = ServeHTTP(ctx, agentSession, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("relay-ok"))
		}))
	}()
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ServeHTTPListener(ctx, httpLn) }()
	unauthReq, err := http.NewRequest(http.MethodGet, "http://"+httpLn.Addr().String()+"/v1/gateways/gw-1/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthResp.StatusCode)
	}
	httpReq, err := http.NewRequest(http.MethodGet, "http://"+httpLn.Addr().String()+"/v1/gateways/gw-1/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	httpReq.Header.Set("Authorization", "Bearer access-token-012345678901234567890123")
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	httpBody, err := io.ReadAll(httpResp.Body)
	_ = httpResp.Body.Close()
	if err != nil || httpResp.StatusCode != http.StatusOK || string(httpBody) != "relay-ok" {
		t.Fatalf("HTTP ingress status=%d body=%q err=%v", httpResp.StatusCode, httpBody, err)
	}
	clientTransport := NewGatewayTransport("ws://127.0.0.1:1", "gw-1", "access-token-012345678901234567890123")
	clientTransport.Dial = dial
	defer clientTransport.Close()
	client := &http.Client{Transport: clientTransport}
	var response *http.Response
	for attempt := 0; attempt < 20; attempt++ {
		response, err = client.Get("http://gateway.local/healthz")
		if err == nil && response.StatusCode == http.StatusOK {
			break
		}
		if response != nil {
			_ = response.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "relay-ok" {
		t.Fatalf("unexpected body %q", body)
	}
}

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
