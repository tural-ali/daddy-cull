package main

import (
	"context"
	"daddy-cull/next/internal/catalog"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	writer := flag.Bool("writer", false, "run only the private Bin filesystem service")
	writerRoot := flag.String("archive-root", "/archive", "writer archive root")
	disksRoot := flag.String("disks-root", "", "guarded physical-disk root for imported legacy Bin records")
	screenshotsRoot := flag.String("screenshots-root", "", "guarded screenshot holding-area root")
	upgradesRoot := flag.String("upgrades-root", "", "guarded read-only Takeout upgrade staging root")
	binUpstream := flag.String("bin-upstream", "", "private Bin service URL")
	checkWriter := flag.Bool("check-writer", false, "check local private Bin service")
	db := flag.String("db", "state/scale.db", "isolated synthetic database")
	importFile := flag.String("import", "", "import a real metadata snapshot into an empty isolated catalogue")
	importEvidence := flag.String("import-evidence", "", "import cached hash evidence into an existing isolated catalogue")
	importLegacy := flag.String("import-legacy-db", "", "import durable state from a read-only legacy database snapshot")
	importScreenshots := flag.String("import-screenshots", "", "index screenshot holding-area metadata without opening media")
	importUpgrades := flag.String("import-upgrades", "", "import the confirmed Takeout upgrade TSV")
	importSocial := flag.String("import-social", "", "import the social-video detection report TSV")
	socialArchivePrefix := flag.String("social-archive-prefix", "/mnt/user/family-archive", "host archive prefix recorded in the social report")
	socialPosters := flag.String("social-posters", "", "read-only directory of poster frames captured during social detection")
	upgradeSourcePrefix := flag.String("upgrade-source-prefix", "/mnt/disk1/takeout-upgrades", "host path prefix recorded in the upgrade report")
	upgradeArchivePrefix := flag.String("upgrade-archive-prefix", "/mnt/user/family-archive", "host archive prefix recorded in the upgrade report")
	upstream := flag.String("media-upstream", "", "trusted legacy read-only media service URL")
	archiveMedia := flag.String("archive-media", "", "read-only archive mount served directly for previews and video playback")
	screenshotsMedia := flag.String("screenshots-media", "", "read-only mount of the screenshot holding area, served for previews")
	upgradesMedia := flag.String("upgrades-media", "", "read-only mount of the Takeout upgrade staging area, served for previews")
	disksMedia := flag.String("disks-media", "", "read-only mount holding the physical disk roots behind the share, served for Shadowed previews")
	reviewMedia := flag.String("review-media", "", "flat directory of hardlinks named by asset id, for files the share cannot expose under their own names")
	previewCache := flag.String("preview-cache", "state/preview-cache", "writable directory for generated gallery thumbnails; never inside a media mount")
	frameTool := flag.String("frame-tool", "ffmpeg", "frame extractor used for videos with no captured poster; empty disables video previews")
	rawTool := flag.String("raw-tool", "exiftool", "reader for the JPEG a camera embeds in a RAW file; empty disables RAW previews")
	seed := flag.Int("seed", 0, "seed an empty catalogue with synthetic metadata, then exit")
	addr := flag.String("listen", "127.0.0.1:8830", "loopback address only; prototype has no authentication")
	demoNetwork := flag.Bool("demo-network", false, "allow private-network access; media access uses fixed read-only proxy routes")
	check := flag.Bool("check", false, "check a running local prototype and exit")
	web := flag.String("web", "web/dist", "compiled React directory")
	flag.Parse()
	imports := 0
	for _, value := range []string{*importFile, *importEvidence, *importLegacy, *importScreenshots, *importUpgrades, *importSocial} {
		if value != "" {
			imports++
		}
	}
	if imports > 1 {
		log.Fatal("choose one import operation")
	}
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
	if *importEvidence != "" {
		f, e := os.Open(*importEvidence)
		if e != nil {
			log.Fatal(e)
		}
		defer f.Close()
		if e = s.ImportEvidence(ctx, f); e != nil {
			log.Fatal(e)
		}
		log.Print("cached duplicate evidence imported; originals untouched")
		return
	}
	if *importLegacy != "" {
		if err = s.ImportLegacyDatabase(ctx, *importLegacy); err != nil {
			log.Fatal(err)
		}
		log.Print("legacy progress and workflow records imported; newer decisions preserved")
		return
	}
	if *importScreenshots != "" {
		result, importErr := s.ImportScreenshotDirectory(ctx, *importScreenshots)
		if importErr != nil {
			log.Fatal(importErr)
		}
		log.Printf("indexed %d screenshot-area files (%d bytes); originals untouched", result.Files, result.Bytes)
		return
	}
	if *importUpgrades != "" {
		f, openErr := os.Open(*importUpgrades)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer f.Close()
		count, importErr := s.ImportUpgradeReport(ctx, f, *upgradesRoot, *upgradeSourcePrefix, *upgradeArchivePrefix)
		if importErr != nil {
			log.Fatal(importErr)
		}
		log.Printf("indexed %d confirmed Takeout upgrade rows; originals untouched", count)
		return
	}
	if *importSocial != "" {
		f, openErr := os.Open(*importSocial)
		if openErr != nil {
			log.Fatal(openErr)
		}
		defer f.Close()
		count, importErr := s.ImportSocialReport(ctx, f, *socialArchivePrefix)
		if importErr != nil {
			log.Fatal(importErr)
		}
		log.Printf("indexed %d social-video candidates; originals untouched", count)
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
		if err := s.IndexCalendar(ctx); err != nil {
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
		var legacyEngine *catalog.LegacyBinEngine
		var screenshotWriter *catalog.ScreenshotWriter
		if *disksRoot != "" {
			var legacyErr error
			legacyEngine, legacyErr = catalog.NewLegacyBinEngine(s, *disksRoot)
			if legacyErr != nil {
				log.Fatal(legacyErr)
			}
			defer legacyEngine.Close()
			mux.Handle("/legacy-bin/", legacyEngine.Handler(secret))
		}
		if *screenshotsRoot != "" {
			var screenshotErr error
			screenshotWriter, screenshotErr = catalog.NewScreenshotWriter(s, *screenshotsRoot, *writerRoot)
			if screenshotErr != nil {
				log.Fatal(screenshotErr)
			}
			defer screenshotWriter.Close()
			mux.Handle("/screenshot/", screenshotWriter.Handler(secret))
		}
		if *upgradesRoot != "" {
			upgradeWriter, upgradeErr := catalog.NewUpgradeWriter(s, *upgradesRoot, *writerRoot)
			if upgradeErr != nil {
				log.Fatal(upgradeErr)
			}
			defer upgradeWriter.Close()
			mux.Handle("/upgrade/", upgradeWriter.Handler(secret))
		}
		// The Bin page acts on everything it lists at once, through the same
		// engines as above; each source is still moved only by its own engine.
		mux.Handle("/trash/", catalog.NewTrashWriter(s, engine, legacyEngine, screenshotWriter).Handler(secret))
		mux.Handle("/", engine.Handler(secret))
	} else {
		// A local read-only mount is preferred when one is given: it needs no
		// second service and, unlike the thumbnail proxy, streams byte ranges so
		// video can be played and seeked in place.
		mediaRoots := catalog.MediaRoots{
			Archive:     *archiveMedia,
			Screenshots: *screenshotsMedia,
			Upgrades:    *upgradesMedia,
			Disks:       *disksMedia,
			Review:      *reviewMedia,
			Posters:     *socialPosters,
			Cache:       *previewCache,
			FFmpeg:      resolveTool(*frameTool),
			RawTool:     resolveTool(*rawTool),
		}
		if mediaRoots.Archive != "" || mediaRoots.Screenshots != "" || mediaRoots.Upgrades != "" || mediaRoots.Disks != "" || mediaRoots.Review != "" {
			mux.Handle("/api/media/{id}/{mode}", s.LocalMediaHandler(mediaRoots))
			// The Bin's own files live inside the archive share, so they are
			// previewable wherever the archive is mounted.
			mux.Handle("/api/bin-media/{id}/{mode}", s.LegacyBinMediaHandler(mediaRoots))
			mux.Handle("/api/binned-media/{source}/{plan}/{index}/{mode}", s.BinnedMediaHandler(mediaRoots))
		} else if *upstream != "" {
			mux.Handle("/api/media/{id}/{mode}", s.MediaHandler(*upstream))
		}
		if *socialPosters != "" {
			mux.Handle("GET /api/social-poster/{id}", s.SocialPosterHandler(*socialPosters))
		}
		mux.Handle("/api/bin", catalog.BinGateway(*binUpstream, secret))
		mux.Handle("/api/bin/", catalog.BinGateway(*binUpstream, secret))
		mux.Handle("GET /api/legacy-bin", s.Handler())
		mux.Handle("/api/legacy-bin/", catalog.LegacyBinGateway(*binUpstream, secret))
		mux.Handle("/api/screenshot-actions/", catalog.ScreenshotGateway(*binUpstream, secret))
		mux.Handle("/api/upgrade-actions/", catalog.UpgradeGateway(*binUpstream, secret))
		mux.Handle("GET /api/trash", s.Handler())
		mux.Handle("/api/trash/", catalog.TrashGateway(*binUpstream, secret))
		mux.Handle("/api/", s.Handler())
		serveApp := func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(*web, "index.html"))
		}
		mux.HandleFunc("GET /on/{md}", serveApp)
		mux.HandleFunc("GET /day/{day}", serveApp)
		for _, route := range []string{"/year", "/duplicates", "/upgrades", "/shadows", "/screenshots", "/social", "/log", "/bin", "/settings"} {
			mux.HandleFunc("GET "+route, serveApp)
		}
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

// resolveTool turns a frame-extractor name into an absolute path once at start
// up, so nothing later resolves a bare command name against PATH. An empty name,
// or one that is not on this machine, simply disables video previews.
func resolveTool(name string) string {
	if name == "" {
		return ""
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		log.Printf("preview tool %q not found; the previews that need it will be unavailable", name)
		return ""
	}
	return resolved
}
