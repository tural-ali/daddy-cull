// Scale measures synthetic metadata pagination, including HTTP routing and JSON
// encoding in-process. It does not measure the network, browser, or media IO.
package main

import (
	"context"
	"daddy-cull/next/internal/catalog"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

func main() {
	n := flag.Int("assets", 100000, "synthetic metadata rows")
	requests := flag.Int("requests", 2000, "HTTP handler requests")
	workers := flag.Int("workers", 8, "concurrent callers")
	flag.Parse()
	if *n < 10 || *n > 10000000 || *requests < 10 || *workers < 1 || *workers > 64 {
		log.Fatal("invalid benchmark parameters")
	}
	dir, err := os.MkdirTemp("", "cull-scale-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	s, err := catalog.Open(filepath.Join(dir, "scale.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	seedStart := time.Now()
	if err = s.Seed(context.Background(), *n); err != nil {
		log.Fatal(err)
	}
	seedSeconds := time.Since(seedStart).Seconds()
	h := s.Handler()
	latency := make([]float64, *requests)
	failures := make([]bool, *requests)
	work := make(chan int)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				// Distributed seeks through the catalogue, including filtered queries.
				id := int64(1 + (i*7919)%(*n-1))
				kind := ""
				if i%2 == 0 {
					kind = "video"
				}
				b, _ := json.Marshal(catalog.Cursor{Version: 1, Time: 1577836800 + id/4, ID: id, MaxID: int64(*n), Kind: kind})
				url := "/api/assets?limit=80&kind=" + kind + "&after=" + base64.RawURLEncoding.EncodeToString(b)
				r := httptest.NewRecorder()
				t := time.Now()
				h.ServeHTTP(r, httptest.NewRequest("GET", url, nil))
				latency[i] = float64(time.Since(t).Microseconds()) / 1000
				failures[i] = r.Code != 200
			}
		}()
	}
	for i := 0; i < *requests; i++ {
		work <- i
	}
	close(work)
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	failed := 0
	for _, f := range failures {
		if f {
			failed++
		}
	}
	sort.Float64s(latency)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var bytes int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if st, e := os.Stat(filepath.Join(dir, "scale.db") + suffix); e == nil {
			bytes += st.Size()
		}
	}
	out := map[string]any{"scope": "synthetic metadata, in-process HTTP handler; no media or network", "go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH, "assets": *n, "requests": *requests, "workers": *workers, "seed_seconds": seedSeconds, "p50_ms": latency[(*requests-1)*50/100], "p95_ms": latency[(*requests-1)*95/100], "p99_ms": latency[(*requests-1)*99/100], "requests_per_second": float64(*requests) / elapsed, "failures": failed, "database_including_wal_mib": float64(bytes) / (1024 * 1024), "go_heap_mib_excludes_native": float64(m.HeapAlloc) / (1024 * 1024)}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	if failed > 0 {
		os.Exit(1)
	}
}
