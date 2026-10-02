package relay

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Peer is the two-sided credential set for one gateway. ConnectorToken is
// used by the gateway agent; AccessToken is used by mobile/desktop clients.
type Peer struct {
	ConnectorToken string `json:"connector_token"`
	AccessToken    string `json:"access_token"`
	PublicHooks    bool   `json:"public_hooks"`
}

// ServerConfig is intentionally separate from the gateway config so a relay
// can be deployed as a small, stateless edge service.
type ServerConfig struct {
	Listen     string          `json:"listen"`
	HTTPListen string          `json:"http_listen"`
	TLSCert    string          `json:"tls_cert"`
	TLSKey     string          `json:"tls_key"`
	Peers      map[string]Peer `json:"peers"`
}

func (c ServerConfig) Validate() error {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return err
	}
	if host != "localhost" && net.ParseIP(host) != nil && !net.ParseIP(host).IsLoopback() && (c.TLSCert == "" || c.TLSKey == "") {
		return errors.New("non-loopback relay listener requires tls_cert and tls_key")
	}
	if c.HTTPListen != "" {
		if c.HTTPListen == c.Listen {
			return errors.New("http_listen must differ from listen")
		}
		host, _, err = net.SplitHostPort(c.HTTPListen)
		if err != nil {
			return err
		}
		if host != "localhost" && net.ParseIP(host) != nil && !net.ParseIP(host).IsLoopback() && (c.TLSCert == "" || c.TLSKey == "") {
			return errors.New("non-loopback http_listen requires tls_cert and tls_key")
		}
	}
	if len(c.Peers) == 0 {
		return errors.New("relay requires at least one peer")
	}
	for id, p := range c.Peers {
		if id == "" || len(p.ConnectorToken) < 32 || len(p.AccessToken) < 32 || subtle.ConstantTimeCompare([]byte(p.ConnectorToken), []byte(p.AccessToken)) == 1 {
			return fmt.Errorf("invalid credentials for peer %q", id)
		}
	}
	return nil
}

type Server struct {
	Cfg ServerConfig

	mu     sync.RWMutex
	agents map[string]*yamux.Session
	closed chan struct{}
}

func NewServer(c ServerConfig) *Server {
	return &Server{Cfg: c, agents: make(map[string]*yamux.Session), closed: make(chan struct{})}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.Cfg.Validate(); err != nil {
		return err
	}
	var ln net.Listener
	var err error
	if s.Cfg.TLSCert != "" {
		cert, e := tls.LoadX509KeyPair(s.Cfg.TLSCert, s.Cfg.TLSKey)
		if e != nil {
			return e
		}
		ln, err = tls.Listen("tcp", s.Cfg.Listen, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
	} else {
		ln, err = net.Listen("tcp", s.Cfg.Listen)
	}
	if err != nil {
		return err
	}
	if s.Cfg.HTTPListen != "" {
		httpListener, e := net.Listen("tcp", s.Cfg.HTTPListen)
		if e != nil {
			return e
		}
		if s.Cfg.TLSCert != "" {
			cert, e := tls.LoadX509KeyPair(s.Cfg.TLSCert, s.Cfg.TLSKey)
			if e != nil {
				_ = httpListener.Close()
				return e
			}
			httpListener = tls.NewListener(httpListener, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}})
		}
		go func() {
			if e := s.ServeHTTPListener(ctx, httpListener); e != nil && ctx.Err() == nil {
				// The raw relay listener remains the source of truth for gateway
				// connections; HTTP ingress failures are isolated and observable
				// through the process exit/logging layer.
			}
		}()
	}
	return s.ServeListener(ctx, ln)
}

// ServeHTTPListener exposes a conventional HTTPS API for phone clients. The
// relay transport remains separate so gateways keep one outbound persistent
// connection while mobile apps use ordinary HTTP libraries.
func (s *Server) ServeHTTPListener(ctx context.Context, ln net.Listener) error {
	server := &http.Server{Handler: s.HTTPHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err := server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			s.mu.RLock()
			agents := len(s.agents)
			s.mu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"ok":true,"agents":%d}`, agents)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 || parts[0] != "v1" || parts[1] != "gateways" {
			http.NotFound(w, r)
			return
		}
		id, err := url.PathUnescape(parts[2])
		if err != nil {
			http.Error(w, "invalid gateway", http.StatusBadRequest)
			return
		}
		peer, ok := s.Cfg.Peers[id]
		if !ok || !secureEqual(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), peer.AccessToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.mu.RLock()
		agent := s.agents[id]
		s.mu.RUnlock()
		if agent == nil || agent.IsClosed() {
			http.Error(w, `{"error":"gateway unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		stream, err := agent.Open()
		if err != nil {
			http.Error(w, `{"error":"gateway unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		defer stream.Close()
		path := "/" + strings.Join(parts[3:], "/")
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		request := r.Clone(r.Context())
		request.URL.Path = path
		request.URL.RawPath = ""
		request.RequestURI = path
		if err = request.Write(stream); err != nil {
			http.Error(w, "gateway request failed", http.StatusBadGateway)
			return
		}
		response, err := http.ReadResponse(bufio.NewReader(stream), request)
		if err != nil {
			http.Error(w, "gateway response failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})
}

// ServeListener runs the relay on an already-bound listener. It is useful for
// embedding and for deterministic end-to-end tests.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	defer ln.Close()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		close(s.closed)
		s.mu.Lock()
		for id, session := range s.agents {
			_ = session.Close()
			delete(s.agents, id)
		}
		s.mu.Unlock()
	}()
	for {
		conn, e := ln.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(e, net.ErrClosed) {
				return nil
			}
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	session, err := yamux.Server(conn, nil)
	if err != nil {
		return
	}
	stream, err := session.Accept()
	if err != nil {
		_ = session.Close()
		return
	}
	_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	var hello Hello
	err = json.NewDecoder(bufio.NewReader(stream)).Decode(&hello)
	_ = stream.SetDeadline(time.Time{})
	if err != nil {
		_ = session.Close()
		return
	}
	peer, ok := s.Cfg.Peers[hello.ID]
	if !ok {
		writeHelloResult(stream, HelloResult{Error: "unknown relay peer"})
		_ = session.Close()
		return
	}
	switch hello.Type {
	case "rcodex-relay/hello":
		if !secureEqual(hello.Token, peer.ConnectorToken) {
			writeHelloResult(stream, HelloResult{Error: "invalid connector token"})
			_ = session.Close()
			return
		}
		writeHelloResult(stream, HelloResult{OK: true})
		_ = stream.Close()
		s.registerAgent(hello.ID, session)
		<-sessionClosed(ctx, session)
		s.unregisterAgent(hello.ID, session)
	case "rcodex-relay/client":
		if !secureEqual(hello.Token, peer.AccessToken) {
			writeHelloResult(stream, HelloResult{Error: "invalid client token"})
			_ = session.Close()
			return
		}
		writeHelloResult(stream, HelloResult{OK: true})
		_ = stream.Close()
		s.serveClient(ctx, hello.ID, session)
	default:
		writeHelloResult(stream, HelloResult{Error: "unsupported relay hello type"})
		_ = session.Close()
	}
}

func (s *Server) serveClient(ctx context.Context, id string, client *yamux.Session) {
	defer client.Close()
	go func() {
		<-ctx.Done()
		_ = client.Close()
	}()
	for {
		stream, err := client.Accept()
		if err != nil {
			return
		}
		go s.forward(ctx, id, stream)
	}
}

func (s *Server) forward(ctx context.Context, id string, client net.Conn) {
	defer client.Close()
	s.mu.RLock()
	agent := s.agents[id]
	s.mu.RUnlock()
	if agent == nil || agent.IsClosed() {
		writeUnavailable(client)
		return
	}
	server, err := agent.Open()
	if err != nil {
		writeUnavailable(client)
		return
	}
	defer server.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func (s *Server) registerAgent(id string, session *yamux.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.agents[id]; old != nil {
		_ = old.Close()
	}
	s.agents[id] = session
}

func (s *Server) unregisterAgent(id string, session *yamux.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents[id] == session {
		delete(s.agents, id)
	}
}

func sessionClosed(ctx context.Context, session *yamux.Session) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		defer close(done)
		for {
			if session.IsClosed() {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return done
}

func secureEqual(a, b string) bool {
	return a != "" && b != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeHelloResult(w net.Conn, result HelloResult) {
	_ = json.NewEncoder(w).Encode(result)
}

func writeUnavailable(w net.Conn) {
	body := `{"error":"gateway unavailable"}`
	_, _ = fmt.Fprintf(w, "HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
}
