package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/StormLegend/rcodex/gateway-go/internal/config"
	"github.com/hashicorp/yamux"
	"net"
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
