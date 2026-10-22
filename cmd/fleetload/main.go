// fleetload runs bounded authenticated READ-only mixed load against a test platform.
// It does not create clusters, run scans, open terminals or mutate resources.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "test platform URL")
	users := flag.Int("users", 10, "concurrent virtual browser sessions")
	duration := flag.Duration("duration", time.Minute, "test duration")
	interval := flag.Duration("interval", 500*time.Millisecond, "think time between requests per user")
	flag.Parse()
	if *users < 1 || *users > 100 || *duration <= 0 || *duration > 24*time.Hour || *interval < 100*time.Millisecond {
		fmt.Fprintln(os.Stderr, "invalid load bounds")
		os.Exit(2)
	}
	target, err := url.Parse(*base)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		fmt.Fprintln(os.Stderr, "invalid URL")
		os.Exit(2)
	}
	// One Cookie header per line. Never print session cookies or request URLs.
	// Separate authenticated cookies model distinct users; do not reuse admin credentials.
	cookiePath := os.Getenv("FLEETLOAD_COOKIES_FILE")
	data, err := os.ReadFile(cookiePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "set FLEETLOAD_COOKIES_FILE to a private file with one session Cookie header per user")
		os.Exit(2)
	}
	var cookies []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			cookies = append(cookies, line)
		}
	}
	if len(cookies) < *users {
		fmt.Fprintln(os.Stderr, "not enough independent session cookies")
		os.Exit(2)
	}
	paths := []string{"/api/v1/clusters", "/api/v1/capacity", "/api/v1/search/resources?q=demo", "/api/v1/events?range=24h&view=issues"}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	var mu sync.Mutex
	counts := map[int]int{}
	total, failed := 0, 0
	// Fixed histogram keeps memory bounded during 24h tests.
	buckets := make([]int64, 10001)
	var wg sync.WaitGroup
	for u := 0; u < *users; u++ {
		wg.Add(1)
		go func(user int) {
			defer wg.Done()
			for n := 0; ctx.Err() == nil; n++ {
				req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(*base, "/")+paths[(n+user)%len(paths)], nil)
				if e != nil {
					return
				}
				req.Header.Set("Cookie", cookies[user])
				started := time.Now()
				res, e := client.Do(req)
				if ctx.Err() != nil {
					if res != nil {
						res.Body.Close()
					}
					return
				}
				status := 0
				bad := e != nil
				if res != nil {
					status = res.StatusCode
					_, readErr := io.Copy(io.Discard, io.LimitReader(res.Body, 4<<20))
					res.Body.Close()
					bad = bad || readErr != nil || status >= 400
				}
				elapsed := float64(time.Since(started).Microseconds()) / 1000
				mu.Lock()
				total++
				if bad {
					failed++
				}
				counts[status]++
				bucket := int(elapsed)
				if bucket > 10000 {
					bucket = 10000
				}
				buckets[bucket]++
				mu.Unlock()
				timer := time.NewTimer(*interval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}(u)
	}
	wg.Wait()
	percentile := func(p float64) int {
		if total == 0 {
			return 0
		}
		threshold := int64(float64(total) * p)
		if threshold < 1 {
			threshold = 1
		}
		var sum int64
		for ms, count := range buckets {
			sum += count
			if sum >= threshold {
				return ms
			}
		}
		return 10000
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"users": *users, "duration": duration.String(), "requests": total, "failed": failed, "status_counts": counts, "latency_ms_histogram_resolution": 1, "p50_ms": percentile(.5), "p95_ms": percentile(.95), "p99_ms": percentile(.99), "scope": "read-only HTTP smoke load; not 100-cluster, browser, terminal or 24h capacity certification"})
	if total == 0 || failed > 0 {
		os.Exit(1)
	}
}
