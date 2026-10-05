// rcg-loadtest performs a bounded read-only gateway soak. It intentionally
// exercises the authenticated sessions endpoint and never creates turns.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type counters struct {
	total, failed int64
	mu            sync.Mutex
	buckets       [10]int64
}

func (c *counters) add(d time.Duration, ok bool) {
	atomic.AddInt64(&c.total, 1)
	if !ok {
		atomic.AddInt64(&c.failed, 1)
	}
	ms := d.Milliseconds()
	i := 0
	for _, v := range []int64{1, 5, 10, 25, 50, 100, 250, 500, 1000} {
		if ms <= v {
			break
		}
		i++
	}
	c.mu.Lock()
	c.buckets[i]++
	c.mu.Unlock()
}
func main() {
	url := flag.String("url", "http://127.0.0.1:18890/api/sessions", "authenticated read-only endpoint")
	token := flag.String("token", "", "bearer token")
	duration := flag.Duration("duration", time.Minute, "test duration")
	concurrency := flag.Int("concurrency", 4, "workers")
	flag.Parse()
	if *duration <= 0 || *concurrency < 1 || *concurrency > 1000 {
		fail("invalid duration or concurrency")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	var c counters
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				start := time.Now()
				req, e := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
				if e == nil && *token != "" {
					req.Header.Set("Authorization", "Bearer "+*token)
				}
				ok := false
				if e == nil {
					resp, err := client.Do(req)
					if err == nil {
						ok = resp.StatusCode >= 200 && resp.StatusCode < 300
						_, _ = io.Copy(io.Discard, resp.Body)
						_ = resp.Body.Close()
					}
				}
				c.add(time.Since(start), ok)
			}
		}()
	}
	wg.Wait()
	c.mu.Lock()
	b := c.buckets
	c.mu.Unlock()
	fmt.Printf("duration=%s concurrency=%d requests=%d failed=%d p95_bucket=%s\n", *duration, *concurrency, atomic.LoadInt64(&c.total), atomic.LoadInt64(&c.failed), bucket(b, 0.95))
}
func bucket(b [10]int64, q float64) string {
	total := int64(0)
	for _, n := range b {
		total += n
	}
	if total == 0 {
		return "n/a"
	}
	want := int64(float64(total) * q)
	if want < 1 {
		want = 1
	}
	seen := int64(0)
	labels := []string{"<=1ms", "<=5ms", "<=10ms", "<=25ms", "<=50ms", "<=100ms", "<=250ms", "<=500ms", "<=1000ms", ">1000ms"}
	for i, n := range b {
		seen += n
		if seen >= want {
			return labels[i]
		}
	}
	return labels[len(labels)-1]
}
func fail(s string) { fmt.Fprintln(os.Stderr, strings.TrimSpace(s)); os.Exit(2) }
