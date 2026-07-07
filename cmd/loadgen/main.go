// Command loadgen is a tiny concurrent HTTP load generator used to benchmark
// Fulcrum. It reports throughput, latency percentiles and a status-code
// breakdown. Standard library only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/", "target URL")
	concurrency := flag.Int("c", 50, "number of concurrent workers")
	duration := flag.Duration("d", 10*time.Second, "test duration")
	flag.Parse()

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var (
		total   atomic.Int64
		errors  atomic.Int64
		mu      sync.Mutex
		codes   = map[int]int64{}
		latency []time.Duration
	)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				t0 := time.Now()
				resp, err := doGet(ctx, client, *url)
				d := time.Since(t0)
				if err != nil {
					errors.Add(1)
					total.Add(1)
					continue
				}
				total.Add(1)
				mu.Lock()
				codes[resp]++
				latency = append(latency, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
	fmt.Printf("target:       %s\n", *url)
	fmt.Printf("concurrency:  %d\n", *concurrency)
	fmt.Printf("duration:     %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("requests:     %d\n", total.Load())
	fmt.Printf("errors:       %d\n", errors.Load())
	fmt.Printf("throughput:   %.0f req/s\n", float64(total.Load())/elapsed.Seconds())
	if len(latency) > 0 {
		fmt.Printf("latency p50:  %s\n", latency[len(latency)*50/100].Round(time.Microsecond))
		fmt.Printf("latency p90:  %s\n", latency[len(latency)*90/100].Round(time.Microsecond))
		fmt.Printf("latency p99:  %s\n", latency[min(len(latency)*99/100, len(latency)-1)].Round(time.Microsecond))
	}
	fmt.Printf("status codes: ")
	for code, n := range codes {
		fmt.Printf("%d=%d ", code, n)
	}
	fmt.Println()

	if errors.Load() > 0 {
		os.Exit(1)
	}
}

func doGet(ctx context.Context, client *http.Client, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
