package config

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Channel struct {
	Token      string   `json:"token"`
	Secret     string   `json:"secret"`
	AppID      string   `json:"app_id"`
	AppSecret  string   `json:"app_secret"`
	EncryptKey string   `json:"encrypt_key"`
	PublicKey  string   `json:"public_key"`
	Users      []string `json:"users"`
	Chats      []string `json:"chats"`
	Runtime    string   `json:"runtime"`
	Model      string   `json:"model"`
	Workspace  string   `json:"workspace"`
	Mode       string   `json:"mode"`
	APIBaseURL string   `json:"api_base_url,omitempty"`
}
type RelayPeer struct {
	ConnectorToken string `json:"connector_token"`
	AccessToken    string `json:"access_token"`
	PublicHooks    bool   `json:"public_hooks"`
}
type Relay struct {
	URL   string               `json:"url"`
	ID    string               `json:"id"`
	Token string               `json:"token"`
	Peers map[string]RelayPeer `json:"peers"`
}
type Provider struct {
	Runtime string   `json:"runtime"`
	Command string   `json:"command,omitempty"`
	Models  []string `json:"models,omitempty"`
	Enabled bool     `json:"enabled"`
}
type Config struct {
	Listen                 string              `json:"listen"`
	DataDir                string              `json:"data_dir"`
	Token                  string              `json:"token"`
	ReadToken              string              `json:"read_token"`
	Roots                  []string            `json:"roots"`
	Workers                int                 `json:"workers"`
	QueueLimit             int                 `json:"queue_limit"`
	TurnSeconds            int                 `json:"turn_seconds"`
	CodexCommand           string              `json:"codex_command"`
	ClaudeCommand          string              `json:"claude_command"`
	CodexHome              string              `json:"codex_home"`
	ClaudeHome             string              `json:"claude_home"`
	AllowFull              bool                `json:"allow_full"`
	TLSCert                string              `json:"tls_cert"`
	TLSKey                 string              `json:"tls_key"`
	TrustedProxyTLS        bool                `json:"trusted_proxy_tls"`
	RateLimitPerMinute     int                 `json:"rate_limit_per_minute"`
	ReadRateLimitPerMinute int                 `json:"read_rate_limit_per_minute"`
	Channels               map[string]Channel  `json:"channels"`
	Providers              map[string]Provider `json:"providers"`
	Relay                  Relay               `json:"relay"`
}

func Load(file string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8790", DataDir: "./data", Workers: 4, QueueLimit: 100, TurnSeconds: 0, CodexCommand: "codex", ClaudeCommand: "claude"}
	b, err := os.ReadFile(file)
	if err != nil {
		return c, err
	}
	var missing string
	text := os.Expand(string(b), func(k string) string {
		v, ok := os.LookupEnv(k)
		if !ok {
			missing = k
		}
		raw, _ := json.Marshal(v)
		return string(raw[1 : len(raw)-1])
	})
	if missing != "" {
		return c, fmt.Errorf("missing environment variable %s", missing)
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return c, err
	}
	if c.Workers < 1 || c.Workers > 64 || c.QueueLimit < 1 || c.TurnSeconds < 0 {
		return c, errors.New("invalid capacity configuration")
	}
	if c.RateLimitPerMinute == 0 {
		c.RateLimitPerMinute = 120
	}
	if c.ReadRateLimitPerMinute == 0 {
		c.ReadRateLimitPerMinute = 600
	}
	if c.RateLimitPerMinute < 1 || c.RateLimitPerMinute > 100000 || c.ReadRateLimitPerMinute < 1 || c.ReadRateLimitPerMinute > 100000 {
		return c, errors.New("invalid rate limit configuration")
	}
	if len(c.Token) < 32 {
		return c, errors.New("token must contain at least 32 characters")
	}
	if c.ReadToken != "" && (len(c.ReadToken) < 32 || Equal(c.Token, c.ReadToken)) {
		return c, errors.New("read_token must be distinct and at least 32 characters")
	}
	if len(c.Roots) == 0 {
		return c, errors.New("at least one workspace root is required")
	}
	for i, p := range c.Roots {
		v, e := filepath.EvalSymlinks(p)
		if e != nil {
			return c, e
		}
		v, e = filepath.Abs(v)
		if e != nil {
			return c, e
		}
		st, e := os.Stat(v)
		if e != nil || !st.IsDir() {
			return c, fmt.Errorf("invalid workspace root %q", p)
		}
		c.Roots[i] = v
	}
	c.DataDir, err = filepath.Abs(c.DataDir)
	if err != nil {
		return c, err
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return c, err
	}
	if !Loopback(host) && c.TLSCert == "" && !c.TrustedProxyTLS {
		return c, errors.New("non-loopback listener requires TLS or trusted_proxy_tls")
	}
	for name, ch := range c.Channels {
		if name != "telegram" && name != "discord" && name != "feishu" {
			return c, fmt.Errorf("unsupported channel %s", name)
		}
		if len(ch.Users) == 0 || len(ch.Chats) == 0 {
			return c, fmt.Errorf("%s requires users and chats allowlists", name)
		}
		if ch.Runtime == "" {
			ch.Runtime = "codex"
		}
		if ch.Mode == "" {
			ch.Mode = "readonly"
		}
		if ch.Workspace == "" {
			ch.Workspace = c.Roots[0]
		}
		if ch.Runtime != "codex" && ch.Runtime != "claude" {
			return c, errors.New("invalid channel runtime")
		}
		if ch.Mode != "readonly" && ch.Mode != "ask" && ch.Mode != "auto" && ch.Mode != "full" {
			return c, errors.New("invalid channel permission mode")
		}
		if ch.Mode == "full" && !c.AllowFull {
			return c, errors.New("full access requires allow_full")
		}
		if ch.APIBaseURL != "" {
			u, e := url.Parse(strings.TrimRight(ch.APIBaseURL, "/"))
			if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !Loopback(u.Hostname())) {
				return c, fmt.Errorf("invalid %s api_base_url", name)
			}
			ch.APIBaseURL = strings.TrimRight(ch.APIBaseURL, "/")
		}
		if name == "telegram" && (len(ch.Secret) < 32 || ch.Token == "") {
			return c, errors.New("telegram requires token and >=32 character secret")
		}
		if name == "discord" && (len(ch.PublicKey) != 64 || ch.AppID == "") {
			return c, errors.New("discord requires application ID and public key")
		}
		if name == "feishu" && (ch.EncryptKey == "" || ch.Secret == "" || ch.AppID == "" || ch.AppSecret == "") {
			return c, errors.New("feishu requires app credentials, verification secret and encrypt key")
		}
		if _, e := c.Workspace(ch.Workspace); e != nil {
			return c, e
		}
		c.Channels[name] = ch
	}
	for name, p := range c.Providers {
		if !Identifier(name) || (p.Runtime != "codex" && p.Runtime != "claude") {
			return c, fmt.Errorf("invalid provider %s", name)
		}
		if p.Command != "" && filepath.IsAbs(p.Command) == false && strings.ContainsAny(p.Command, "\n\r") {
			return c, errors.New("invalid provider command")
		}
	}
	if c.Relay.URL != "" {
		u, e := url.Parse(c.Relay.URL)
		if e != nil {
			return c, e
		}
		if u.Scheme != "wss" && !(u.Scheme == "ws" && Loopback(u.Hostname())) {
			return c, errors.New("relay requires wss off loopback")
		}
		if len(c.Relay.Token) < 32 || !Identifier(c.Relay.ID) {
			return c, errors.New("invalid relay identity")
		}
	}
	for id, p := range c.Relay.Peers {
		if !Identifier(id) || len(p.ConnectorToken) < 32 || len(p.AccessToken) < 32 || Equal(p.AccessToken, p.ConnectorToken) {
			return c, errors.New("relay peers need independent connector/access secrets >=32 chars")
		}
	}
	return c, nil
}
func Equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func Loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
func Identifier(v string) bool {
	if len(v) < 1 || len(v) > 100 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func (c Config) Workspace(p string) (string, error) {
	if p == "" && len(c.Roots) > 0 {
		p = c.Roots[0]
	}
	p, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	for _, root := range c.Roots {
		rel, e := filepath.Rel(root, p)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			st, e := os.Stat(p)
			if e == nil && st.IsDir() {
				return p, nil
			}
		}
	}
	return "", errors.New("workspace outside allowed roots")
}
