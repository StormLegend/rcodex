// Package ops monitors independent gateways and records an operator's choice.
// It never forwards or retries application requests between gateways.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Target struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"` // go or legacy; APIs and sessions are independent
	URL        string `json:"url"`
	ConsoleURL string `json:"console_url,omitempty"`
	HealthPath string `json:"health_path"`
	TokenEnv   string `json:"token_env,omitempty"`
}
type Config struct {
	Targets           []Target `json:"targets"`
	StateFile         string   `json:"state_file"`
	TimeoutSeconds    int      `json:"timeout_seconds"`
	FailureThreshold  int      `json:"failure_threshold"`
	RecoveryThreshold int      `json:"recovery_threshold"`
}
type Health struct {
	Name       string    `json:"name"`
	Healthy    bool      `json:"healthy"`
	HTTPStatus int       `json:"http_status,omitempty"`
	LatencyMS  int64     `json:"latency_ms"`
	CheckedAt  time.Time `json:"checked_at"`
	Error      string    `json:"error,omitempty"`
}
type Selection struct {
	Target     string    `json:"target"`
	Kind       string    `json:"kind"`
	URL        string    `json:"url"`
	ConsoleURL string    `json:"console_url,omitempty"`
	SelectedAt time.Time `json:"selected_at"`
	Reason     string    `json:"reason"`
}

func Load(path string) (Config, error) {
	c := Config{TimeoutSeconds: 5, FailureThreshold: 3, RecoveryThreshold: 2}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 60 || c.FailureThreshold < 1 || c.RecoveryThreshold < 1 || len(c.Targets) < 2 || c.StateFile == "" {
		return c, errors.New("invalid targets configuration")
	}
	names := map[string]bool{}
	for i, t := range c.Targets {
		u, err := url.Parse(t.URL)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("invalid target URL")
		}
		if t.Name == "" || names[t.Name] || (t.Kind != "go" && t.Kind != "legacy") {
			return c, errors.New("invalid or duplicate target identity")
		}
		names[t.Name] = true
		if t.HealthPath == "" {
			c.Targets[i].HealthPath = "/healthz"
		} else if !strings.HasPrefix(t.HealthPath, "/") || strings.HasPrefix(t.HealthPath, "//") {
			return c, errors.New("health_path must be an absolute URL path")
		}
	}
	if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(filepath.Dir(path), c.StateFile)
	}
	return c, nil
}
func (c Config) Target(name string) (Target, error) {
	for _, t := range c.Targets {
		if t.Name == name {
			return t, nil
		}
	}
	return Target{}, errors.New("unknown target")
}
func Probe(ctx context.Context, t Target, timeout time.Duration) Health {
	h := Health{Name: t.Name, CheckedAt: time.Now().UTC()}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(t.URL, "/")+t.HealthPath, nil)
	if err != nil {
		h.Error = "invalid health URL"
		return h
	}
	if t.TokenEnv != "" {
		token := os.Getenv(t.TokenEnv)
		if token == "" {
			h.Error = "missing health credential environment variable"
			return h
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	h.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		h.Error = "health connection failed or timed out"
		return h
	}
	defer resp.Body.Close()
	h.HTTPStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		h.Error = "health returned non-200 status"
		return h
	}
	// A login page or reverse proxy's generic 200 is not a healthy gateway.
	var body struct {
		OK bool `json:"ok"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body) != nil || !body.OK {
		h.Error = "health response must contain ok=true"
		return h
	}
	h.Healthy = true
	return h
}
func (c Config) Check(ctx context.Context) []Health {
	out := make([]Health, len(c.Targets))
	done := make(chan int, len(out))
	for i, t := range c.Targets {
		go func(i int, t Target) { out[i] = Probe(ctx, t, time.Duration(c.TimeoutSeconds)*time.Second); done <- i }(i, t)
	}
	for range out {
		<-done
	}
	return out
}
func (c Config) Current() (Selection, error) {
	var s Selection
	b, err := os.ReadFile(c.StateFile)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	if err != nil {
		return s, err
	}
	t, err := c.Target(s.Target)
	if err != nil {
		return s, err
	}
	// Resolve from current config rather than trusting stale URLs in saved state.
	s.URL, s.Kind, s.ConsoleURL = t.URL, t.Kind, t.ConsoleURL
	return s, nil
}
func (c Config) Select(ctx context.Context, name, reason string) (Selection, error) {
	t, err := c.Target(name)
	if err != nil {
		return Selection{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return Selection{}, errors.New("selection reason is required")
	}
	h := Probe(ctx, t, time.Duration(c.TimeoutSeconds)*time.Second)
	if !h.Healthy {
		return Selection{}, fmt.Errorf("refusing unhealthy target %s: %s", name, h.Error)
	}
	s := Selection{Target: t.Name, Kind: t.Kind, URL: t.URL, ConsoleURL: t.ConsoleURL, SelectedAt: time.Now().UTC(), Reason: reason}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return s, err
	}
	if err = os.MkdirAll(filepath.Dir(c.StateFile), 0700); err != nil {
		return s, err
	}
	f, err := os.CreateTemp(filepath.Dir(c.StateFile), ".selection-*")
	if err != nil {
		return s, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return s, err
	}
	if closeErr != nil {
		return s, closeErr
	}
	if err = os.Rename(f.Name(), c.StateFile); err != nil {
		return s, err
	}
	return s, nil
}

// Tracker debounces transient failures. Selection is always an explicit action.
type Tracker struct {
	Failures, Successes int
	Degraded            bool
}

func (t *Tracker) Observe(healthy bool, failures, recoveries int) bool {
	old := t.Degraded
	if healthy {
		t.Successes++
		t.Failures = 0
		if t.Successes >= recoveries {
			t.Degraded = false
		}
	} else {
		t.Failures++
		t.Successes = 0
		if t.Failures >= failures {
			t.Degraded = true
		}
	}
	return old != t.Degraded
}
