package main

import (
	"context"
	"daddy-cull/next/internal/catalog"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	writer := flag.Bool("writer", false, "run only the private Bin filesystem service")
	writerRoot := flag.String("archive-root", "/archive", "writer archive root")
	binUpstream := flag.String("bin-upstream", "", "private Bin service URL")
	checkWriter := flag.Bool("check-writer", false, "check local private Bin service")
	db := flag.String("db", "state/scale.db", "isolated synthetic database")
	importFile := flag.String("import", "", "import a real metadata snapshot into an empty isolated catalogue")
	upstream := flag.String("media-upstream", "", "trusted legacy read-only media service URL")
	seed := flag.Int("seed", 0, "seed an empty catalogue with synthetic metadata, then exit")
	addr := flag.String("listen", "127.0.0.1:8830", "loopback address only; prototype has no authentication")
	demoNetwork := flag.Bool("demo-network", false, "allow private-network access; media access uses fixed read-only proxy routes")
	check := flag.Bool("check", false, "check a running local prototype and exit")
	web := flag.String("web", "web/dist", "compiled React directory")
	flag.Parse()
	if *check || *checkWriter {
		client := http.Client{Timeout: 2 * time.Second}
		healthURL := "http://127.0.0.1:8830/api/stats"
		if *checkWriter {
			healthURL = "http://127.0.0.1:8831/health"
		}
		r, e := client.Get(healthURL)
		if e != nil {
			log.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			log.Fatal("unhealthy")
		}
		return
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || net.ParseIP(host) == nil || (!net.ParseIP(host).IsLoopback() && !*demoNetwork) {
		log.Fatal("prototype must listen on a loopback IP")
	}
	s, err := catalog.Open(*db)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *importFile != "" {
		f, e := os.Open(*importFile)
		if e != nil {
			log.Fatal(e)
		}
		defer f.Close()
		if e = s.ImportSnapshot(ctx, f); e != nil {
			log.Fatal(e)
		}
		log.Print("real metadata snapshot imported; originals untouched")
		return
	}
	if *seed != 0 {
		if err = s.Seed(ctx, *seed); err != nil {
			log.Fatal(err)
		}
		log.Printf("created %d synthetic metadata records; no media files", *seed)
		return
	}
	if !*writer {
		if err := s.IndexRelated(ctx); err != nil {
			log.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	secret := os.Getenv("CULL_BIN_KEY")
	if *writer {
		if len(secret) < 32 {
			log.Fatal("private writer secret required")
		}
		engine, e := catalog.NewBinEngine(s, *writerRoot)
		if e != nil {
			log.Fatal(e)
		}
		defer engine.Close()
		mux.Handle("/", engine.Handler(secret))
	} else {
		if *upstream != "" {
			mux.Handle("/api/media/{id}/{mode}", s.MediaHandler(*upstream))
		}
		mux.Handle("/api/bin", catalog.BinGateway(*binUpstream, secret))
		mux.Handle("/api/bin/", catalog.BinGateway(*binUpstream, secret))
		mux.Handle("/api/", s.Handler())
		mux.Handle("/", http.FileServer(http.Dir(*web)))
	}
	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	log.Printf("review app: http://%s", *addr)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
