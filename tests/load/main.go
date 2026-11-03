// Command load is a basic load test for the Jenderal Cloud OS API: it logs in once
// and then runs concurrent authenticated read requests against the most
// frequently polled endpoints, reporting throughput, latency percentiles and
// errors. It never creates workloads.
//
//	go run ./tests/load -base http://127.0.0.1:8080 -user U -password P -c 50 -d 30s
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("base", "http://127.0.0.1:8080", "API base URL")
	user := flag.String("user", "", "username (account without MFA)")
	pass := flag.String("password", "", "password")
	conc := flag.Int("c", 50, "concurrent clients")
	dur := flag.Duration("d", 30*time.Second, "duration")
	maxP95 := flag.Duration("max-p95", 500*time.Millisecond, "fail if p95 latency exceeds this")
	flag.Parse()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: *conc * 2}}
	body, _ := json.Marshal(map[string]string{"username": *user, "password": *pass})
	req, _ := http.NewRequest("POST", *base+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		fmt.Fprintln(os.Stderr, "login failed:", err, resp)
		os.Exit(1)
	}
	resp.Body.Close()

	paths := []string{"/api/v1/dashboard", "/api/v1/workspaces?per_page=20", "/api/v1/notifications/unread-count",
		"/api/v1/auth/me", "/api/v1/templates", "/api/v1/projects", "/api/v1/containers?status=running&mine=true&per_page=5"}
	var mu sync.Mutex
	latencies := []time.Duration{}
	var ok, failed atomic.Int64
	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			local := []time.Duration{}
			for n := i; time.Now().Before(deadline); n++ {
				start := time.Now()
				r, err := client.Get(*base + paths[n%len(paths)])
				if err == nil {
					_, _ = io.Copy(io.Discard, r.Body)
					r.Body.Close()
				}
				local = append(local, time.Since(start))
				if err != nil || r.StatusCode != 200 {
					failed.Add(1)
				} else {
					ok.Add(1)
				}
			}
			mu.Lock()
			latencies = append(latencies, local...)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	sort.Slice(latencies, func(a, b int) bool { return latencies[a] < latencies[b] })
	pct := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*p)]
	}
	total := ok.Load() + failed.Load()
	fmt.Printf("requests: %d (%.0f req/s), errors: %d (%.2f%%)\n", total, float64(total)/dur.Seconds(), failed.Load(), 100*float64(failed.Load())/float64(max(total, 1)))
	fmt.Printf("latency p50=%v p95=%v p99=%v max=%v\n", pct(0.50), pct(0.95), pct(0.99), pct(1))
	if failed.Load() > 0 || pct(0.95) > *maxP95 {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS")
}
