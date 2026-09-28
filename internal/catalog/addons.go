package catalog

//go:generate go run ../apidoc/cmd/apidoc daddy-cull/next/internal/catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"daddy-cull/next/internal/addon"
	"daddy-cull/next/internal/api"
)

// The ids of Cull's own addons.
const (
	AddonScreenshots = "screenshots"
	AddonSocial      = "social"
	AddonShadows     = "shadows"
	AddonUpgrades    = "upgrades"
	AddonApplePhotos = "apple-photos"
	AddonImmich      = "immich"
	AddonClassic     = "classic"
)

// Stats is what the frame of the app shows about the library.
type Stats struct {
	// Total counts every file in the catalogue.
	Total int64 `json:"total"`
	// Synthetic is true for a catalogue made up for testing, with no real files.
	Synthetic bool `json:"synthetic"`
	// SnapshotAt is when the catalogue was last indexed.
	SnapshotAt string `json:"snapshotAt"`
	// Candidates counts files with another version of the same picture.
	Candidates int `json:"candidates"`
	// CalendarDays and ReviewedDays count days of particular years; CalendarDates
	// and ReviewedDates count calendar dates, as the Year page does.
	CalendarDays int `json:"calendarDays"`
	ReviewedDays int `json:"reviewedDays"`
	// Decisions counts files with a choice or a heart.
	Decisions  int `json:"decisions"`
	Favourites int `json:"favourites"`
	// Evidence counts files with a quick fingerprint, FullHashes those whose
	// every byte has been hashed.
	Evidence   int `json:"evidence"`
	FullHashes int `json:"fullHashes"`
	// Marked counts files removed while reviewing that are not in the Bin yet.
	Marked int `json:"marked"`
	// Bin counts what the Bin page shows, from every source.
	Bin int `json:"bin"`
	// LegacyBin, ShadowGroups, Screenshots, Social, UpgradesAccepted and
	// UpgradeCandidates are the addons' counts, whether they are on or not.
	LegacyBin         int `json:"legacyBin"`
	ShadowGroups      int `json:"shadowGroups"`
	Screenshots       int `json:"screenshots"`
	Social            int `json:"social"`
	UpgradesAccepted  int `json:"upgradesAccepted"`
	UpgradeCandidates int `json:"upgradeCandidates"`
	// ImmichSynced, ImmichPending, ImmichFailed and ImmichRefused count hearts
	// by how far they are on their way to Immich.
	ImmichSynced  int `json:"immichSynced"`
	ImmichPending int `json:"immichPending"`
	ImmichFailed  int `json:"immichFailed"`
	ImmichRefused int `json:"immichRefused"`
	CalendarDates int `json:"calendarDates"`
	ReviewedDates int `json:"reviewedDates"`
	// Streak is how many days in a row something was reviewed, and
	// ReviewedToday whether today is one of them.
	Streak        int  `json:"streak"`
	ReviewedToday bool `json:"reviewedToday"`
	// VideoMuted is whether videos start muted.
	VideoMuted bool `json:"videoMuted"`
	// Notifications counts unread notifications.
	Notifications int `json:"notifications"`
}

// Stats counts the library, with the day of review ending at midnight in loc.
func (s *Store) Stats(ctx context.Context, loc *time.Location) (Stats, error) {
	n, err := s.Count(ctx)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	st.Total = n
	var library string
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='library'").Scan(&library)
	st.Synthetic = library != "real"
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='snapshot_at'").Scan(&st.SnapshotAt)
	st.Candidates = countQuery(ctx, s.read, "SELECT count(*) FROM assets WHERE anchor_id IS NOT NULL")
	st.CalendarDays = countQuery(ctx, s.read, "SELECT count(DISTINCT day) FROM asset_days")
	st.ReviewedDays = countQuery(ctx, s.read, "SELECT count(*) FROM day_progress WHERE status='done'")
	st.Decisions = countQuery(ctx, s.read, "SELECT count(*) FROM decisions WHERE status!='unreviewed' OR favourite=1")
	st.Favourites = countQuery(ctx, s.read, "SELECT count(*) FROM decisions WHERE favourite=1")
	st.Evidence = countQuery(ctx, s.read, "SELECT count(*) FROM asset_evidence")
	st.FullHashes = countQuery(ctx, s.read, "SELECT count(*) FROM asset_evidence WHERE full_hash IS NOT NULL")
	st.Marked = countQuery(ctx, s.read, "SELECT count(*) FROM decisions d WHERE d.status='cull' AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=d.asset_id AND fs.state!='restored')")
	st.LegacyBin = countQuery(ctx, s.read, "SELECT count(*) FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL")
	st.ShadowGroups = countQuery(ctx, s.read, "SELECT count(*) FROM (SELECT 1 FROM shadow_entries GROUP BY kind,group_key)")
	st.Screenshots = countQuery(ctx, s.read, "SELECT count(*) FROM screenshot_items WHERE state='waiting'")
	st.Social = countQuery(ctx, s.read, "SELECT count(*) FROM social_items s LEFT JOIN decisions d ON d.asset_id=s.asset_id WHERE s.state='waiting'"+socialPending)
	st.UpgradesAccepted = countQuery(ctx, s.read, "SELECT count(*) FROM upgrade_history")
	st.UpgradeCandidates = countQuery(ctx, s.read, "SELECT count(DISTINCT archive_asset_id) FROM upgrade_candidates")
	// The nav's Bin count is the number of cards the Bin page shows, from
	// every source, so the two never disagree.
	if items, err := s.Trash(ctx); err == nil {
		st.Bin = len(items)
	}
	st.ImmichSynced, st.ImmichPending, st.ImmichFailed, st.ImmichRefused = s.ImmichQueueCounts(ctx)
	// Progress is counted in calendar dates, as on the Year page: a date is
	// reviewed when every year filed under it is.
	if calendar, err := s.Calendar(ctx, time.Now()); err == nil {
		st.CalendarDates = calendar.Progress.Dates
		st.ReviewedDates = calendar.Progress.Done
	}
	activity, _ := s.Activity(ctx, loc, time.Now())
	st.Streak = activity.Streak
	st.ReviewedToday = activity.Today
	st.VideoMuted, _ = s.VideoMuted(ctx)
	st.Notifications, _ = s.UnreadNotifications(ctx)
	return st, nil
}

func addonSetting(id string) string { return "addon." + id }

// AddonChoice says whether an addon was turned on, and whether anyone ever
// chose.
func (s *Store) AddonChoice(ctx context.Context, id string) (on, chosen bool, err error) {
	var value string
	err = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", addonSetting(id)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return value == "on", true, nil
}

// SetAddonChoice saves that an addon was turned on or off.
func (s *Store) SetAddonChoice(ctx context.Context, id string, on bool) error {
	value := "off"
	if on {
		value = "on"
	}
	_, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", addonSetting(id), value)
	return err
}

// fromParam reads the optional from parameter, an offset into a list.
func fromParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	from, ok := intParam(w, r, "from", 0)
	if ok && from < 0 {
		api.Fail(w, 400, "from should be 0 or more.")
		return 0, false
	}
	return from, ok
}

// addonRoutes serves the routes of the addons whose records the catalogue
// keeps. Each answers only while its addon is on.
func (s *Store) addonRoutes(m *api.Mux) {
	book := m.Book()
	book.Tag("Screenshots", "Screenshots and screen recordings set aside from the archive, to keep or remove as a batch.")
	book.Tag("Saved from social", "Videos in the archive that were probably saved from a social app rather than filmed.")
	book.Tag("Shadowed copies", "Files hidden behind another file of the same name on another disk of the array.")
	book.Tag("Takeout upgrades", "Higher-resolution copies from Google Takeout of photos the archive holds smaller.")
	book.Tag("Daddy Cull classic", "What the earlier Daddy Cull left in its Bin.")

	from := api.Query("from", "integer", "How many to skip, for the next page. Pages hold 120.")
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/screenshots", Addon: AddonScreenshots, Tag: "Screenshots", Needs: api.Read,
		Summary: "List screenshots waiting",
		Doc:     "Screenshots and screen recordings in the holding area, 120 at a time, with counts for each filter.",
		Params: []api.Param{
			{Name: "kind", In: "query", Type: "string", Doc: "Only stills (image) or only recordings (video).", Enum: []string{"image", "video"}},
			{Name: "review", In: "query", Type: "string", Doc: "Which to list: those not reviewed yet by default, reviewed, or all.", Enum: []string{"reviewed", "all"}},
			from,
		},
		Returns: ScreenshotPage{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		offset, ok := fromParam(w, r)
		if !ok {
			return
		}
		items, err := s.ScreenshotPage(ctx, r.URL.Query().Get("kind"), r.URL.Query().Get("review"), offset, 120)
		if err != nil {
			failFor(w, err, "kind should be image or video, and review reviewed or all.")
			return
		}
		writeJSON(w, items)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/screenshot-bin", Addon: AddonScreenshots, Tag: "Screenshots", Needs: api.Read,
		Summary: "List batches of screenshots in the Bin",
		Doc:     "Each batch of screenshots moved into the Bin, newest first, with its files.",
		Returns: []ScreenshotPlan{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		plans, err := s.ScreenshotBin(ctx, 500)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, plans)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/social", Addon: AddonSocial, Tag: "Saved from social", Needs: api.Read,
		Summary: "List videos probably saved from social apps",
		Doc:     "Videos still waiting for a choice, 120 at a time, most likely first, with counts for each band.",
		Params: []api.Param{
			{Name: "band", In: "query", Type: "string", Doc: "Only this band: social (likely from a social app) or unsure, or the finer likely, possible, watch and letterboxed.", Enum: []string{"social", "unsure", "likely", "possible", "watch", "letterboxed"}},
			from,
		},
		Returns: SocialPage{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		offset, ok := fromParam(w, r)
		if !ok {
			return
		}
		page, err := s.SocialCandidates(ctx, r.URL.Query().Get("band"), offset, 120)
		if err != nil {
			failFor(w, err, "band should be one of social, unsure, likely, possible, watch or letterboxed.")
			return
		}
		writeJSON(w, page)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/shadows", Addon: AddonShadows, Tag: "Shadowed copies", Needs: api.Read,
		Summary: "List shadowed copies",
		Doc:     "Groups of files that share a path on different disks, where the array shows only one of them.",
		Returns: []ShadowGroup{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		groups, err := s.ShadowGroups(ctx, 500)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, groups)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/upgrades", Addon: AddonUpgrades, Tag: "Takeout upgrades", Needs: api.Read,
		Summary: "List Takeout upgrades",
		Doc:     "Archive photos with a confirmed higher-resolution copy in Google Takeout, each with its candidates.",
		Returns: UpgradePage{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		data, err := s.Upgrades(ctx)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, data)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/legacy-bin", Addon: AddonClassic, Tag: "Daddy Cull classic", Needs: api.Read,
		Summary: "List what the earlier Daddy Cull put in its Bin",
		Doc:     "Files the earlier app moved to its Bin that are neither restored nor deleted. They are also on the Bin page with everything else.",
		Returns: []LegacyBinItem{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, err := s.LegacyBin(ctx, 5000)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, items)
	})
}

// AddonNeeds is what the app knows about how it was started, which decides
// whether some of Cull's own addons have what they need.
type AddonNeeds struct {
	// ScreenshotsMounted says the screenshot holding area is mounted.
	ScreenshotsMounted bool
	// DisksMounted says the array's disks are mounted, which shadowed copies need.
	DisksMounted bool
	// UpgradesMounted says the Takeout staging area is mounted.
	UpgradesMounted bool
	// ImmichConfigured says Immich's address and key are both set.
	ImmichConfigured bool
	// Photos is the Apple Photos helper's hub.
	Photos *PhotosHub
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmtCount(n) + " " + many
}

func fmtCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// BuiltInAddons are the addons that come with Cull. An addon with something
// to show is on until someone turns it off; one waiting for setup is off
// until someone turns it on.
func (s *Store) BuiltInAddons(needs AddonNeeds) []addon.BuiltIn {
	count := func(query string) func(context.Context) int {
		return func(ctx context.Context) int { return countQuery(ctx, s.read, query) }
	}
	screenshots := count("SELECT count(*) FROM screenshot_items WHERE state='waiting'")
	anyScreenshots := count("SELECT count(*) FROM (SELECT 1 FROM screenshot_items LIMIT 1)")
	social := count("SELECT count(*) FROM social_items s LEFT JOIN decisions d ON d.asset_id=s.asset_id WHERE s.state='waiting'" + socialPending)
	anySocial := count("SELECT count(*) FROM (SELECT 1 FROM social_items LIMIT 1)")
	shadows := count("SELECT count(*) FROM (SELECT 1 FROM shadow_entries GROUP BY kind,group_key)")
	upgrades := count("SELECT count(DISTINCT archive_asset_id) FROM upgrade_candidates")
	anyUpgrades := count("SELECT (SELECT count(*) FROM (SELECT 1 FROM upgrade_candidates LIMIT 1))+(SELECT count(*) FROM (SELECT 1 FROM upgrade_history LIMIT 1))")
	classic := count("SELECT count(*) FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL")
	anyClassic := count("SELECT count(*) FROM (SELECT 1 FROM legacy_culled LIMIT 1)")
	has := func(n func(context.Context) int) func(context.Context) bool {
		return func(ctx context.Context) bool { return n(ctx) > 0 }
	}
	always := func(context.Context) bool { return true }
	return []addon.BuiltIn{
		{
			Manifest: addon.Manifest{
				ID: AddonScreenshots, Name: "Screenshots", Version: "1", Icon: "screenshot_region",
				Summary:     "Review screenshots and screen recordings apart from the photos, and remove them in batches.",
				Description: "Screenshots are set aside into a holding area when they are downloaded, so they never clutter a date. This page lists them to keep or remove many at a time.",
				Pages:       []addon.Page{{ID: "screenshots", Label: "Screenshots", Icon: "screenshot_region", Section: addon.Collections, Path: "/screenshots"}},
				Needs:       []string{"A holding area that screenshots are moved into as they are downloaded"},
			},
			Status: func(ctx context.Context) addon.Status {
				n := screenshots(ctx)
				if n == 0 && !needs.ScreenshotsMounted && anyScreenshots(ctx) == 0 {
					return addon.Status{State: addon.Setup, Detail: "No holding area is set up, so no screenshots have been found."}
				}
				return addon.Status{State: addon.Ready, Detail: counted(n, "screenshot waits", "screenshots wait") + " for review."}
			},
			Default: func(ctx context.Context) bool { return needs.ScreenshotsMounted || anyScreenshots(ctx) > 0 },
		},
		{
			Manifest: addon.Manifest{
				ID: AddonSocial, Name: "Saved from social", Version: "1", Icon: "forum",
				Summary:     "Find videos saved from social apps rather than filmed, and clear them out.",
				Description: "A scan scores each video by its shape, its sound, its encoder and the app that wrote it. The page lists the likely ones first, with a poster frame of each.",
				Pages:       []addon.Page{{ID: "social", Label: "Saved from social", Icon: "forum", Section: addon.Collections, Path: "/social"}},
				Needs:       []string{"A detection report imported with -import-social"},
			},
			Status: func(ctx context.Context) addon.Status {
				if anySocial(ctx) == 0 {
					return addon.Status{State: addon.Setup, Detail: "No detection report has been imported yet."}
				}
				return addon.Status{State: addon.Ready, Detail: counted(social(ctx), "video waits", "videos wait") + " for a choice."}
			},
			Default: has(anySocial),
		},
		{
			Manifest: addon.Manifest{
				ID: AddonShadows, Name: "Shadowed copies", Version: "1", Icon: "layers",
				Summary:     "Show files hidden behind a file of the same name on another disk.",
				Description: "On an array that merges its disks into one share, two disks can hold a file at the same path, and the share shows only one. This lists both so the hidden one is not lost.",
				Pages:       []addon.Page{{ID: "shadows", Label: "Shadowed", Icon: "layers", Section: addon.Collections, Path: "/shadows"}},
				Needs:       []string{"The array's disks mounted read-only", "A scan of the disks for shadowed paths"},
			},
			Status: func(ctx context.Context) addon.Status {
				n := shadows(ctx)
				if n == 0 {
					if !needs.DisksMounted {
						return addon.Status{State: addon.Setup, Detail: "The array's disks are not mounted, so nothing can be shadowed."}
					}
					return addon.Status{State: addon.Ready, Detail: "No shadowed copies were found."}
				}
				return addon.Status{State: addon.Ready, Detail: counted(n, "shadowed path", "shadowed paths") + "."}
			},
			Default: has(shadows),
		},
		{
			Manifest: addon.Manifest{
				ID: AddonUpgrades, Name: "Takeout upgrades", Version: "1", Icon: "high_quality",
				Summary:     "Swap archive photos for the higher-resolution copies Google Takeout holds.",
				Description: "Some photos reached the archive smaller than they were taken. When a Google Takeout export holds the original, this shows the two side by side and swaps in the better one, keeping the smaller in the Bin.",
				Pages:       []addon.Page{{ID: "upgrades", Label: "Upgrades", Icon: "high_quality", Section: addon.Collections, Path: "/upgrades"}},
				Needs:       []string{"A Takeout staging area mounted read-only", "A confirmed upgrade report imported with -import-upgrades"},
			},
			Status: func(ctx context.Context) addon.Status {
				if anyUpgrades(ctx) == 0 {
					if !needs.UpgradesMounted {
						return addon.Status{State: addon.Setup, Detail: "No Takeout staging area is mounted."}
					}
					return addon.Status{State: addon.Setup, Detail: "No upgrade report has been imported yet."}
				}
				return addon.Status{State: addon.Ready, Detail: counted(upgrades(ctx), "photo has", "photos have") + " a better copy waiting."}
			},
			Default: has(anyUpgrades),
		},
		{
			Manifest: addon.Manifest{
				ID: AddonApplePhotos, Name: "Apple Photos", Version: "1", Icon: "cloud_sync",
				Summary:     "Carry what you remove in Cull across to Apple Photos on a Mac, and so to iCloud.",
				Description: "Cull Sync, a small helper in the Mac's menu bar, finds each file Cull removed in the Photos library and, once you agree, puts it in an album to delete from. Photos is only ever changed through its own interface.",
				Pages:       []addon.Page{{ID: "photos", Label: "Apple Photos", Icon: "cloud_sync", Section: addon.Sync, Path: "/photos"}},
				Needs:       []string{"A Mac with the Photos library, running Cull Sync"},
				Work:        []string{"Checks Photos again whenever a check is due while Cull Sync is online"},
			},
			Status: func(ctx context.Context) addon.Status {
				if needs.Photos == nil || !needs.Photos.Enabled() {
					return addon.Status{State: addon.Setup, Detail: "Cull Sync is not set up yet. Its page gives the one command that installs it."}
				}
				return addon.Status{State: addon.Ready, Detail: "Cull Sync is set up."}
			},
			Default: always,
		},
		{
			Manifest: addon.Manifest{
				ID: AddonImmich, Name: "Immich", Version: "1", Icon: "favorite",
				Summary:     "Mirror the hearts you give in Cull to the same photos in Immich.",
				Description: "Immich reads the archive as an external library. Each heart given or taken away in Cull is sent to Immich through its API, retried until it lands.",
				Needs:       []string{"IMMICH_URL and IMMICH_KEY set in the environment"},
				Work:        []string{"Sends hearts to Immich as they are given"},
			},
			Status: func(ctx context.Context) addon.Status {
				if !needs.ImmichConfigured {
					return addon.Status{State: addon.Setup, Detail: "IMMICH_URL and IMMICH_KEY are not both set, so hearts are kept until they are."}
				}
				synced, pending, failed, _ := s.ImmichQueueCounts(ctx)
				detail := fmt.Sprintf("%s in Immich, %s on the way.", counted(synced, "heart", "hearts"), fmtCount(pending))
				if failed > 0 {
					return addon.Status{State: addon.Problem, Detail: detail + " " + counted(failed, "heart", "hearts") + " could not be sent."}
				}
				return addon.Status{State: addon.Ready, Detail: detail}
			},
			Default: func(context.Context) bool { return needs.ImmichConfigured },
		},
		{
			Manifest: addon.Manifest{
				ID: AddonClassic, Name: "Daddy Cull classic", Version: "1", Icon: "history",
				Summary:     "Bring across what the earlier Daddy Cull recorded, including its Bin.",
				Description: "Progress, choices and the Bin of the earlier app are imported once with -import-legacy-db. Its Bin's files show on the Bin page with everything else, whether this is on or not.",
				Needs:       []string{"A copy of the earlier app's database, imported with -import-legacy-db"},
			},
			Status: func(ctx context.Context) addon.Status {
				if anyClassic(ctx) == 0 {
					return addon.Status{State: addon.Setup, Detail: "Nothing has been imported from the earlier app."}
				}
				return addon.Status{State: addon.Ready, Detail: counted(classic(ctx), "file", "files") + " from its Bin wait."}
			},
			Default: has(anyClassic),
		},
	}
}
