package main

import (
	"bufio"
	"context"
	"daddy-cull/next/internal/addon"
	"daddy-cull/next/internal/api"
	"daddy-cull/next/internal/catalog"
	"daddy-cull/next/mac"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	// The image carries no zoneinfo, so without this TZ is ignored and the
	// clock silently runs on UTC: "today" on the calendar then turns over at
	// 02:00 in Germany in summer, and a review mark near midnight lands on the
	// wrong side of a year.
	_ "time/tzdata"
)

// version is the release, set when a release is built:
// go build -ldflags "-X main.version=0.12.3".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the release this is and exit")
	configFile := flag.String("config", "", "config.json written by install.sh and changed from the Setup page; it fills in every flag not given here, and Cull stops when it changes so launchd starts it again")
	writer := flag.Bool("writer", false, "run only the private Bin filesystem service")
	writerRoot := flag.String("archive-root", "/archive", "writer archive root")
	disksRoot := flag.String("disks-root", "", "guarded physical-disk root for imported legacy Bin records")
	screenshotsRoot := flag.String("screenshots-root", "", "guarded screenshot holding-area root")
	upgradesRoot := flag.String("upgrades-root", "", "guarded read-only Takeout upgrade staging root")
	takeoutInbox := flag.String("takeout-inbox", "", "read-only folder that Google Takeout exports of Google Photos are dropped into; both processes need it")
	importDir := flag.String("import-dir", "", "folder new photos are dropped into; the writer files each under the day it was taken in -archive-root and catalogues it")
	var downloadDirs folderList
	flag.Var(&downloadDirs, "download-dir", "folder a downloader such as icloudpd fills, outside the import folder and the library; the writer files its photos like the import folder's and leaves an empty placeholder of each, so it is not downloaded again; may be given more than once; needs -import-dir")
	folderMode := flag.String("folder-mode", "0777", "octal mode the writer gives the day folders it makes; 0777 suits a shared NAS, 0755 a library only its owner uses")
	binUpstream := flag.String("bin-upstream", "", "private Bin service URL")
	checkWriter := flag.Bool("check-writer", false, "check local private Bin service")
	db := flag.String("db", "state/scale.db", "isolated synthetic database")
	importFile := flag.String("import", "", "import a real metadata snapshot into an empty isolated catalogue")
	importEvidence := flag.String("import-evidence", "", "import cached hash evidence into an existing isolated catalogue")
	importLegacy := flag.String("import-legacy-db", "", "import durable state from a read-only legacy database snapshot")
	importScreenshots := flag.String("import-screenshots", "", "index screenshot holding-area metadata without opening media")
	importUpgrades := flag.String("import-upgrades", "", "import the confirmed Takeout upgrade TSV")
	scanArchive := flag.String("scan-archive", "", "add media in this archive directory's day folders that the catalogue does not hold yet, then exit")
	phoneDeletions := flag.String("phone-deletions", "", "read photos deleted on a phone from stdin, as host paths under this archive directory, mark for the Bin the ones nobody decided on, then exit")
	screenshotFilter := flag.Bool("screenshot-filter", false, "read paths on stdin, print \"<path>\\t<rule>\" for each one that is a screenshot, then exit")
	importSocial := flag.String("import-social", "", "import the social-video detection report TSV")
	socialArchivePrefix := flag.String("social-archive-prefix", "", "host archive prefix recorded in the social report; needed with -import-social")
	socialPosters := flag.String("social-posters", "", "read-only directory of poster frames captured during social detection")
	upgradeSourcePrefix := flag.String("upgrade-source-prefix", "", "host path prefix recorded in the upgrade report; needed with -import-upgrades")
	upgradeArchivePrefix := flag.String("upgrade-archive-prefix", "", "host archive prefix recorded in the upgrade report; needed with -import-upgrades")
	upstream := flag.String("media-upstream", "", "trusted legacy read-only media service URL")
	archiveMedia := flag.String("archive-media", "", "read-only archive mount served directly for previews and video playback")
	screenshotsMedia := flag.String("screenshots-media", "", "read-only mount of the screenshot holding area, served for previews")
	upgradesMedia := flag.String("upgrades-media", "", "read-only mount of the Takeout upgrade staging area, served for previews")
	disksMedia := flag.String("disks-media", "", "read-only mount holding the physical disk roots behind the share, served for Shadowed previews")
	reviewMedia := flag.String("review-media", "", "flat directory of hardlinks named by asset id, for files the share cannot expose under their own names")
	previewCache := flag.String("preview-cache", "state/preview-cache", "writable directory for generated gallery thumbnails; never inside a media mount (default: preview-cache beside -db)")
	frameTool := flag.String("frame-tool", "ffmpeg", "frame extractor used for videos with no captured poster; empty disables video previews")
	rawTool := flag.String("raw-tool", "exiftool", "reader for the JPEG a camera embeds in a RAW file; empty disables RAW previews")
	seed := flag.Int("seed", 0, "seed an empty catalogue with synthetic metadata, then exit")
	addr := flag.String("listen", "127.0.0.1:8830", "loopback address only; prototype has no authentication")
	demoNetwork := flag.Bool("demo-network", false, "allow private-network access; media access uses fixed read-only proxy routes")
	check := flag.Bool("check", false, "check a running local prototype and exit")
	web := flag.String("web", "web/dist", "compiled React directory")
	addonsDir := flag.String("addons-dir", "state/addons", "folder of addons of your own, one folder each holding an addon.json; created if missing (default: addons beside -db)")
	// The Immich key is taken from IMMICH_KEY only, never from a flag, so it
	// does not appear in a process listing or in the container's command line.
	immichURL := flag.String("immich-url", os.Getenv("IMMICH_URL"), "Immich base URL that archive favourites are mirrored to; empty disables the sync")
	immichPrefix := flag.String("immich-path-prefix", os.Getenv("IMMICH_PATH_PREFIX"), "archive path as Immich's external library recorded it; needed with -immich-url")
	// Like Immich's, the Jellyfin key comes from JELLYFIN_KEY only.
	jellyfinURL := flag.String("jellyfin-url", os.Getenv("JELLYFIN_URL"), "Jellyfin base URL that is asked to scan again when files leave or return to the library; empty disables it")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	var setupConfig catalog.SetupConfig
	if *configFile != "" {
		setupConfig = applyConfig(*configFile, *writer, &downloadDirs)
	}
	besideCatalogue()
	imports := 0
	for _, value := range []string{*importFile, *importEvidence, *importLegacy, *importScreenshots, *importUpgrades, *importSocial, *scanArchive, *phoneDeletions} {
		if value != "" {
			imports++
		}
	}
	if imports > 1 {
		log.Fatal("choose one import operation")
	}
	// The graduation script on the host asks this before it deletes anything,
	// so it answers from the name alone and needs no catalogue.
	if *screenshotFilter {
		matched, filterErr := filterScreenshots(os.Stdin, os.Stdout)
		if filterErr != nil {
			log.Fatal(filterErr)
		}
		fmt.Fprintf(os.Stderr, "screenshot-filter: %d matched\n", matched)
		return
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
	if *scanArchive != "" {
		result, scanErr := s.ScanArchive(ctx, *scanArchive)
		if scanErr != nil {
			log.Fatal(scanErr)
		}
		if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
			log.Fatal(encodeErr)
		}
		return
	}
	if *phoneDeletions != "" {
		result, markErr := s.MarkPhoneDeletions(ctx, *phoneDeletions, os.Stdin)
		if markErr != nil {
			log.Fatal(markErr)
		}
		fmt.Println(result)
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
		if *upgradeSourcePrefix == "" || *upgradeArchivePrefix == "" {
			log.Fatal("-import-upgrades needs -upgrade-source-prefix and -upgrade-archive-prefix: the host paths the report was written with")
		}
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
		if *socialArchivePrefix == "" {
			log.Fatal("-import-social needs -social-archive-prefix: the host archive path the report was written with")
		}
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
		// Live Photo clips leave the indexes below, so they are paired first.
		// Without the archive to read, the pairs the last scan found stand.
		if *archiveMedia != "" {
			if _, err := s.IndexLiveClips(ctx, *archiveMedia); err != nil {
				log.Printf("live photos: %v", err)
			}
		}
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
		mode, modeErr := strconv.ParseUint(*folderMode, 8, 32)
		if modeErr != nil || mode > 0o777 || mode&0o700 != 0o700 {
			log.Fatalf("-folder-mode %q: give an octal mode such as 0755 that lets the owner read, write and enter", *folderMode)
		}
		catalog.ArchiveFolderMode = os.FileMode(mode)
		engine, e := catalog.NewBinEngine(s, *writerRoot)
		if e != nil {
			log.Fatal(e)
		}
		defer engine.Close()
		// Batches made before the Bin took Live Photo videos with their photos
		// left the videos behind; they join their photos here.
		go func() {
			n, err := engine.AdoptAllLiveClips(ctx)
			if n > 0 {
				log.Printf("live photos: %d videos joined their photos in the Bin", n)
			}
			if err != nil {
				log.Printf("live photos: %v", err)
			}
		}()
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
		if *takeoutInbox != "" {
			googleWriter, googleErr := catalog.NewGooglePhotosWriter(s, *takeoutInbox, *writerRoot)
			if googleErr != nil {
				log.Fatal(googleErr)
			}
			defer googleWriter.Close()
			mux.Handle("/google-photos/", googleWriter.Handler(secret))
		}
		if *importDir != "" {
			dateTool := resolveTool(*rawTool)
			if dateTool == "" {
				log.Printf("without %q, photos in the import folder are dated by their names and modification times only", *rawTool)
			}
			intake, intakeErr := catalog.NewIntakeWriter(s, *importDir, *writerRoot, dateTool)
			if intakeErr != nil {
				log.Fatal(intakeErr)
			}
			defer intake.Close()
			for _, dir := range downloadDirs {
				if mirrorErr := intake.Mirror(dir); mirrorErr != nil {
					log.Fatal(mirrorErr)
				}
			}
			mux.Handle("/intake/", intake.Handler(secret))
			go intake.Keep(ctx)
		} else if len(downloadDirs) > 0 {
			log.Fatal("-download-dir needs -import-dir: its photos are filed by the same writer")
		} else if forgetErr := s.ForgetIntake(ctx); forgetErr != nil {
			log.Fatal(forgetErr)
		}
		// The Bin page acts on everything it lists at once, through the same
		// engines as above; each source is still moved only by its own engine.
		trash := catalog.NewTrashWriter(s, engine, legacyEngine, screenshotWriter)
		mux.Handle("/trash/", trash.Handler(secret))
		// Files deleted from the Bin wait out their grace period on disk; only
		// this process may delete them, so the reaper runs here.
		trash.StartReaper(ctx)
		if *configFile != "" {
			go catalog.Watch(ctx, *configFile, stop)
		}
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
		// Every API route is described where it is served, so the reference at
		// /developers and /api/openapi.json is always the API as it runs. A
		// request passes the same-origin check, then the addons' check: an
		// addon's key, and whether the addon a route belongs to is on.
		book := api.NewBook()
		apiMux := api.NewMux(book)
		apiMux.Guard(api.SameOrigin)
		fixed := catalog.SetupConfig{Library: *archiveMedia, TakeoutInbox: *takeoutInbox, Immich: catalog.SetupImmich{URL: *immichURL, PathPrefix: *immichPrefix}, Jellyfin: catalog.SetupJellyfin{URL: *jellyfinURL}}
		if *configFile != "" {
			fixed = setupConfig
		}
		// A change on the Setup page stops Cull, and launchd starts it again.
		catalog.NewSetup(*configFile, fixed, stop, version).Routes(apiMux)
		// The Mac helper that carries culling across to Apple Photos talks to
		// this process, which is also where its jobs live. Its key is normally
		// handed out by the setup command on the Apple Photos page and only its
		// hash kept; PHOTOS_AGENT_KEY, from the environment only, pins one
		// instead. Neither is ever logged.
		agentKey := os.Getenv("PHOTOS_AGENT_KEY")
		photos := catalog.NewPhotosHub(s, agentKey)
		photos.SetHelper(mac.CullSync)
		if agentKey != "" && !catalog.ValidPhotosAgentKey(agentKey) {
			log.Printf("PHOTOS_AGENT_KEY ignored: it needs %d or more printable characters and no spaces; Cull Sync is set up from the Apple Photos page instead", catalog.PhotosAgentKeyMin)
		} else if !photos.Enabled() {
			log.Print("Apple Photos helper not set up yet: the Apple Photos page offers the setup command")
		}
		googlePhotos, err := s.NewGooglePhotos(*takeoutInbox, mediaRoots.Archive, *previewCache)
		if err != nil {
			log.Fatal(err)
		}
		defer googlePhotos.Close()
		immich := catalog.ImmichConfig{URL: *immichURL, Key: os.Getenv("IMMICH_KEY"), PathPrefix: *immichPrefix}
		jellyfin := catalog.JellyfinConfig{URL: *jellyfinURL, Key: os.Getenv("JELLYFIN_KEY")}
		if err := os.MkdirAll(*addonsDir, 0o755); err != nil {
			log.Printf("addons folder %s could not be made, so only Cull's own addons are available: %v", *addonsDir, err)
		}
		addons, err := addon.NewRegistry(s, book, *addonsDir, s.BuiltInAddons(catalog.AddonNeeds{
			ScreenshotsMounted: mediaRoots.Screenshots != "",
			DisksMounted:       mediaRoots.Disks != "",
			UpgradesMounted:    mediaRoots.Upgrades != "",
			ImmichConfigured:   immich.URL != "" && immich.Key != "",
			JellyfinConfigured: jellyfin.URL != "" && jellyfin.Key != "",
			Photos:             photos,
			GooglePhotos:       googlePhotos,
		})...)
		if err != nil {
			log.Fatal(err)
		}
		apiMux.Guard(addons.Guard)
		s.Routes(apiMux)
		s.MediaRoutes(apiMux, mediaRoots, *upstream, *socialPosters)
		go s.WarmSidecarFacts(ctx)
		s.WriterRoutes(apiMux, *binUpstream, secret)
		s.IntakeRoutes(apiMux, *binUpstream, secret)
		photos.Routes(apiMux)
		addons.Routes(apiMux)
		s.EventRoutes(apiMux, addons.Changed)
		go addons.Watch(ctx)
		// Long file operations queue here and run one batch at a time
		// through the private writer, so a page never waits on them.
		tasks := s.NewTaskRunner(*binUpstream, secret)
		tasks.Routes(apiMux)
		googlePhotos.Routes(apiMux, tasks, mediaRoots)
		// What a task added from Google Photos is catalogued as soon as it
		// ends, rather than at the next scan of the archive.
		tasks.OnFinished(catalog.TaskGooglePhotosAdd, func(ctx context.Context) {
			if mediaRoots.Archive == "" {
				return
			}
			if result, scanErr := s.ScanArchive(ctx, mediaRoots.Archive); scanErr != nil {
				log.Printf("google photos: cataloguing what was added: %v", scanErr)
			} else {
				log.Printf("google photos: catalogued %d new files", result.Added)
			}
			googlePhotos.Wake()
		})
		go tasks.Run(ctx)
		apiMux.HandleFunc(api.Route{
			Method: "GET", Path: "/api/openapi.json", Tag: "Reference", Needs: api.Read,
			Summary: "Get this reference",
			Doc:     "Every route above as an OpenAPI 3.1 document, generated from the routes as they are served, for a client generator or another viewer.",
			Returns: map[string]any{},
		}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-cache")
			json.NewEncoder(w).Encode(book.OpenAPI(api.Info{Title: "Daddy Cull", Version: api.Version,
				Description: "The API Cull's own pages use, open to addons. Everything a page can do, an addon can do, within the permissions it asks for."}))
		})
		book.Tag("Reference", "The API described by itself.")
		mux.Handle("/api/", apiMux)
		mux.Handle("/", webApp(*web, []string{"/year", "/today", "/duplicates", "/upgrades", "/shadows", "/screenshots", "/social", "/log", "/bin", "/photos", "/google-photos", "/settings", "/addons", "/developers", "/setup", "/addons/{id}/{page}"}))
		// Photos is checked again by itself while Cull Sync is online, so the
		// Apple Photos page never shows a stale answer.
		go addons.While(ctx, catalog.AddonApplePhotos, photos.KeepChecking)
		// The Takeout inbox is read every few minutes while its addon is on.
		go addons.While(ctx, catalog.AddonGooglePhotos, googlePhotos.Keep)
		// Favourites are saved by this process, so the worker that mirrors them
		// to Immich runs here too; the private writer has no reason to reach it.
		// Video tiles show how long each clip runs, read once per file.
		if mediaRoots.Archive != "" && mediaRoots.FFmpeg != "" {
			go s.KeepDurations(ctx, mediaRoots)
		}
		// Grids lay photos out at their own shapes, read once per file.
		if mediaRoots.Archive != "" && mediaRoots.RawTool != "" {
			go s.KeepShapes(ctx, mediaRoots)
		}
		// Files that share a size are hashed until Duplicates can prove copies.
		if mediaRoots.Archive != "" {
			go s.KeepHashes(ctx, mediaRoots)
		}
		startImmichSync(ctx, s, immich, addons)
		startJellyfinRefresh(ctx, s, jellyfin, addons)
	}
	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(c)
	}()
	role := "Daddy Cull " + version
	if *writer {
		role += ", writer,"
	}
	log.Printf("%s listening on http://%s", role, *addr)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// besideCatalogue puts the preview cache and the addons folder next to the
// catalogue when neither is given, rather than under the working directory,
// which a container may not be able to write.
func besideCatalogue() {
	given := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })
	state := filepath.Dir(flag.Lookup("db").Value.String())
	for name, folder := range map[string]string{"preview-cache": "preview-cache", "addons-dir": "addons"} {
		if !given[name] {
			_ = flag.Set(name, filepath.Join(state, folder))
		}
	}
}

// applyConfig fills every flag not given on the command line from the config
// file, makes the folders it names, and takes the writer's key, Immich's and
// Jellyfin's from secrets.env beside it when the environment has none.
func applyConfig(file string, writer bool, downloads *folderList) catalog.SetupConfig {
	config, err := catalog.ReadSetup(file)
	if err != nil {
		log.Fatal(err)
	}
	given := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })
	use := func(name, value string) {
		if value == "" || given[name] {
			return
		}
		if err := flag.Set(name, value); err != nil {
			log.Fatalf("%s: %s: %v", file, name, err)
		}
	}
	state := catalog.StateDir(file)
	use("db", filepath.Join(state, "library.db"))
	use("preview-cache", filepath.Join(state, "preview-cache"))
	use("addons-dir", filepath.Join(state, "addons"))
	use("takeout-inbox", config.TakeoutInbox)
	use("immich-url", config.Immich.URL)
	use("immich-path-prefix", config.Immich.PathPrefix)
	use("jellyfin-url", config.Jellyfin.URL)
	if writer {
		use("listen", "127.0.0.1:8831")
		use("archive-root", config.Library)
		use("import-dir", config.Import)
		use("folder-mode", fmt.Sprintf("%#o", config.FolderMode()))
		if config.ICloud.On && !given["download-dir"] {
			*downloads = append(*downloads, catalog.ICloudDownloads(file))
		}
		for _, folder := range append([]string{config.Library, config.Import}, *downloads...) {
			if folder == "" {
				continue
			}
			if err := os.MkdirAll(folder, 0o755); err != nil {
				log.Fatal(err)
			}
		}
	} else {
		use("archive-media", config.Library)
		use("bin-upstream", "http://127.0.0.1:8831")
	}
	if body, err := os.ReadFile(filepath.Join(state, "secrets.env")); err == nil {
		for _, line := range strings.Split(string(body), "\n") {
			name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && (name == "CULL_BIN_KEY" || name == "IMMICH_KEY" || name == "JELLYFIN_KEY") && os.Getenv(name) == "" && value != "" {
				os.Setenv(name, value)
			}
		}
	}
	return config
}

// folderList is a flag that may be given more than once.
type folderList []string

func (f *folderList) String() string { return strings.Join(*f, ",") }

func (f *folderList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("a folder is needed")
	}
	*f = append(*f, value)
	return nil
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

// startImmichSync starts the favourite mirror when Immich is configured. A
// missing or malformed setting is reported once and leaves the review app
// running without it: hearts are still saved and queued, and reach Immich once
// the setting is fixed and the app restarted.
func startImmichSync(ctx context.Context, s *catalog.Store, cfg catalog.ImmichConfig, addons *addon.Registry) {
	sync, err := catalog.NewImmichSync(s, cfg)
	if err != nil {
		log.Printf("Immich favourite sync disabled: %v", err)
		return
	}
	if sync == nil {
		log.Print("Immich favourite sync disabled: IMMICH_URL or IMMICH_KEY is not set; favourites are queued until it is")
		return
	}
	log.Printf("Immich favourite sync enabled for %s", cfg.URL)
	// It runs only while the Immich addon is on; hearts given meanwhile wait
	// in the queue and are sent once it is back on.
	go addons.While(ctx, catalog.AddonImmich, sync.Run)
}

// startJellyfinRefresh starts asking Jellyfin to scan when it is configured.
// A malformed address is reported once and leaves the review app running
// without it.
func startJellyfinRefresh(ctx context.Context, s *catalog.Store, cfg catalog.JellyfinConfig, addons *addon.Registry) {
	refresh, err := catalog.NewJellyfinRefresh(s, cfg)
	if err != nil {
		log.Printf("Jellyfin scans disabled: %v", err)
		return
	}
	if refresh == nil {
		log.Print("Jellyfin scans disabled: JELLYFIN_URL or JELLYFIN_KEY is not set")
		return
	}
	log.Printf("Jellyfin scans enabled for %s", cfg.URL)
	go addons.While(ctx, catalog.AddonJellyfin, refresh.Run)
}

// filterScreenshots reads one path per line and writes "<path>\t<rule>" for
// each screenshot, and nothing for the rest, so stdout is pure data.
func filterScreenshots(in io.Reader, out io.Writer) (int, error) {
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 65536), 1048576)
	w := bufio.NewWriter(out)
	matched := 0
	for scan.Scan() {
		line := strings.TrimRight(scan.Text(), "\r")
		if line == "" {
			continue
		}
		if rule := catalog.ClassifyScreenshot(line); rule != "" {
			if _, err := fmt.Fprintf(w, "%s\t%s\n", line, rule); err != nil {
				return matched, err
			}
			matched++
		}
	}
	if err := scan.Err(); err != nil {
		return matched, err
	}
	return matched, w.Flush()
}
