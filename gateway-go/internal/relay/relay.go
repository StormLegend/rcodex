package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/hashicorp/yamux"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Manager struct {
	Cfg  config.Relay
	Dial func(context.Context, string) (net.Conn, error)
}

func New(c config.Relay) *Manager {
	return &Manager{Cfg: c, Dial: func(ctx context.Context, u string) (net.Conn, error) {
		x, e := url.Parse(u)
		if e != nil {
			return nil, e
		}
		if x.Scheme != "wss" && !(x.Scheme == "ws" && strings.HasPrefix(x.Host, "127.0.0.1")) {
			return nil, errors.New("relay transport must use wss off loopback")
		}
		d := &net.Dialer{Timeout: 10 * time.Second}
		if x.Scheme == "wss" {
			return tls.DialWithDialer(d, "tcp", x.Host, &tls.Config{MinVersion: tls.VersionTLS13, ServerName: x.Hostname()})
		}
		return d.DialContext(ctx, "tcp", x.Host)
	}}
}
func (m *Manager) Connect(ctx context.Context) (*yamux.Session, error) {
	if m.Cfg.URL == "" {
		return nil, errors.New("relay disabled")
	}
	c, e := m.Dial(ctx, m.Cfg.URL)
	if e != nil {
		return nil, e
	}
	return yamux.Client(c, nil)
}

type Hello struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Token string `json:"token"`
}
type HelloResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Authenticate establishes the relay control stream. The stream is closed
// after the handshake; subsequent application streams use the authenticated
// yamux session. Relay servers must validate the token in constant time.
func (m *Manager) Authenticate(ctx context.Context, session *yamux.Session) error {
	if session == nil {
		return errors.New("nil relay session")
	}
	stream, err := session.Open()
	if err != nil {
		return err
	}
	defer stream.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	} else {
		_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if err = json.NewEncoder(stream).Encode(Hello{Type: "rcodex-relay/hello", ID: m.Cfg.ID, Token: m.Cfg.Token}); err != nil {
		return err
	}
	var result HelloResult
	if err = json.NewDecoder(bufio.NewReader(stream)).Decode(&result); err != nil {
		return err
	}
	if !result.OK {
		if result.Error == "" {
			result.Error = "relay authentication rejected"
		}
		return errors.New(result.Error)
	}
	return nil
}

func (m *Manager) ConnectAuthenticated(ctx context.Context) (*yamux.Session, error) {
	session, err := m.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.Authenticate(ctx, session); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

// ConnectRetry keeps reconnect policy in one place and never spins faster
// than the configured backoff when a relay is unavailable.
func (m *Manager) ConnectRetry(ctx context.Context) (*yamux.Session, error) {
	delay := 250 * time.Millisecond
	var last error
	for {
		s, err := m.ConnectAuthenticated(ctx)
		if err == nil {
			return s, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, last
		case <-time.After(delay):
		}
		if delay < 10*time.Second {
			delay *= 2
		}
	}
}

type sessionListener struct{ session *yamux.Session }

func (l sessionListener) Accept() (net.Conn, error) { return l.session.Accept() }
func (l sessionListener) Close() error              { return l.session.Close() }
func (l sessionListener) Addr() net.Addr            { return relayAddr("yamux") }

type relayAddr string

func (a relayAddr) Network() string { return "rcodex-relay" }
func (a relayAddr) String() string  { return string(a) }

// ServeHTTP exposes the supplied authenticated gateway handler to incoming
// relay streams. The relay server is responsible for mapping its public
// endpoint to yamux streams; each stream carries a normal HTTP/1.1 exchange.
func ServeHTTP(ctx context.Context, session *yamux.Session, handler http.Handler) error {
	if session == nil {
		return errors.New("nil relay session")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	err := server.Serve(sessionListener{session})
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
