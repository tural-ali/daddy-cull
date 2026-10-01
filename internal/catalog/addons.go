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
	AddonScreenshots  = "screenshots"
	AddonSocial       = "social"
	AddonShadows      = "shadows"
	AddonUpgrades     = "upgrades"
	AddonApplePhotos  = "apple-photos"
	AddonGooglePhotos = "google-photos"
	AddonImmich       = "immich"
	AddonJellyfin     = "jellyfin"
	AddonLibrary      = "library-totals"
	AddonClassic      = "classic"
)

// Stats is what the frame of the app shows about the library.
type Stats struct {
	// Total counts the files in the library, as the Library totals do: the
	// archive, less what is in the Bin, deleted from it or missing from disk,
	// and less Live Photo videos, which go with their photo.
	Total int64 `json:"total"`
	// Synthetic is true for a catalogue made up for testing, with no real files.
	Synthetic bool `json:"synthetic"`
	// SnapshotAt is when the catalogue was last indexed, as YYYY-MM-DD
	// HH:MM:SS in UTC, or empty before the first index.
	SnapshotAt string `json:"snapshotAt"`
	// Candidates counts files with another version of the same picture.
	Candidates int `json:"candidates"`
	// CalendarDays counts days of particular years with files, such as 3 June
	// 2019 and 3 June 2021 as two.
	CalendarDays int `json:"calendarDays"`
	// ReviewedDays counts the days of particular years marked reviewed.
	ReviewedDays int `json:"reviewedDays"`
	// Decisions counts files with a choice or a heart.
	Decisions int `json:"decisions"`
	// Favourites counts files with a heart.
	Favourites int `json:"favourites"`
	// Evidence counts files with a quick fingerprint.
	Evidence int `json:"evidence"`
	// FullHashes counts files whose every byte has been hashed.
	FullHashes int `json:"fullHashes"`
	// Marked counts files removed while reviewing that are not in the Bin yet.
	Marked int `json:"marked"`
	// Bin counts what the Bin page shows, from every source.
	Bin int `json:"bin"`
	// LegacyBin counts files in the earlier app's Bin that are neither
	// restored nor deleted, whether the Daddy Cull classic addon is on or not.
	LegacyBin int `json:"legacyBin"`
	// ShadowGroups counts paths held by more than one disk, whether the
	// Shadowed copies addon is on or not.
	ShadowGroups int `json:"shadowGroups"`
	// Screenshots counts screenshots waiting in the holding area, whether the
	// Screenshots addon is on or not.
	Screenshots int `json:"screenshots"`
	// Social counts videos probably saved from social apps that still wait
	// for a choice, whether the Saved from social addon is on or not.
	Social int `json:"social"`
	// UpgradesAccepted counts better copies from Takeout already added to the
	// archive.
	UpgradesAccepted int `json:"upgradesAccepted"`
	// UpgradeCandidates counts archive photos with a better copy from Takeout
	// waiting.
	UpgradeCandidates int `json:"upgradeCandidates"`
	// ImmichSynced counts hearts Immich shows as favourites.
	ImmichSynced int `json:"immichSynced"`
	// ImmichPending counts hearts, or hearts taken away, still on their way to
	// Immich, including those being retried.
	ImmichPending int `json:"immichPending"`
	// ImmichFailed counts hearts Immich could not take, such as for a file it
	// has no asset for; they are tried again once a day.
	ImmichFailed int `json:"immichFailed"`
	// ImmichRefused counts hearts Immich will not let Cull change, as the
	// photo belongs to another Immich user; they are not tried again.
	ImmichRefused int `json:"immichRefused"`
	// CalendarDates counts calendar dates with files in any year, as the Year
	// page does.
	CalendarDates int `json:"calendarDates"`
	// ReviewedDates counts calendar dates reviewed in every year filed under
	// them.
	ReviewedDates int `json:"reviewedDates"`
	// Streak is how many days in a row something was reviewed, in the
	// viewer's time zone.
	Streak int `json:"streak"`
	// ReviewedToday is whether something was reviewed today, which the streak
	// counts from; without it the streak counts back from yesterday.
	ReviewedToday bool `json:"reviewedToday"`
	// VideoMuted is whether videos start muted.
	VideoMuted bool `json:"videoMuted"`
	// RawTogether is whether a RAW and its exports show as one photo.
	RawTogether bool `json:"rawTogether"`
	// HiddenGuides lists the pages whose guide is hidden, such as "upgrades",
	// or is null when that could not be read.
	HiddenGuides []string `json:"hiddenGuides"`
	// Notifications counts unread notifications.
	Notifications int `json:"notifications"`
	// Library counts what the library holds, as photos and videos, while the
	// Library totals addon is on, and is left out while it is off.
	Library *LibraryStats `json:"library,omitempty"`
}

// LibraryStats counts the files in the library: the archive, less what is in
// the Bin, deleted from it or missing from disk.
type LibraryStats struct {
	// Photos counts photos, RAW files included.
	Photos MediaTotal `json:"photos"`
	// Videos counts videos.
	Videos MediaTotal `json:"videos"`
}

// MediaTotal is how many files there are and how much space they take.
type MediaTotal struct {
	// Files counts the files.
	Files int64 `json:"files"`
	// Bytes is their size added up, in bytes.
	Bytes int64 `json:"bytes"`
}

// LibraryStats counts the library's photos and videos and their sizes.
func (s *Store) LibraryStats(ctx context.Context) (LibraryStats, error) {
	var st LibraryStats
	rows, err := s.read.QueryContext(ctx, `SELECT a.kind='video',count(*),coalesce(sum(a.size_bytes),0) FROM assets a
		WHERE a.source_id='archive'
		  AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
		  AND a.id NOT IN (`+liveClipAssets+`)
		  AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
		GROUP BY a.kind='video'`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var video bool
		var total MediaTotal
		if err := rows.Scan(&video, &total.Files, &total.Bytes); err != nil {
			return st, err
		}
		if video {
			st.Videos = total
		} else {
			st.Photos = total
		}
	}
	return st, rows.Err()
}

// Stats counts the library, with the day of review ending at midnight in loc.
func (s *Store) Stats(ctx context.Context, loc *time.Location) (Stats, error) {
	library, err := s.LibraryStats(ctx)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	st.Total = library.Photos.Files + library.Videos.Files
	var kind string
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='library'").Scan(&kind)
	st.Synthetic = kind != "real"
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
	st.Screenshots = countQuery(ctx, s.read, "SELECT count(*) FROM screenshot_items shots JOIN assets a ON a.id=shots.asset_id WHERE shots.state='waiting'"+screenshotNotQueued)
	st.Social = countQuery(ctx, s.read, "SELECT count(*) FROM social_items s LEFT JOIN decisions d ON d.asset_id=s.asset_id WHERE s.state='waiting'"+socialPending)
	st.UpgradesAccepted = countQuery(ctx, s.read, "SELECT count(*) FROM upgrade_history")
	st.UpgradeCandidates = countQuery(ctx, s.read, "SELECT count(DISTINCT archive_asset_id) FROM upgrade_candidates")
	// The nav's Bin count is the number of cards the Bin page shows, from
	// every source, so the two never disagree.
	if items, err := s.Trash(ctx); err == nil {
		if items, err = s.notQueued(ctx, items); err == nil {
			st.Bin = len(items)
		}
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
	st.RawTogether, _ = s.RawTogether(ctx)
	// Left null when it cannot be read, so a browser keeps what it knows
	// rather than showing every guide again.
	st.HiddenGuides, _ = s.HiddenGuides(ctx)
	st.Notifications, _ = s.UnreadNotifications(ctx)
	// The addon is on unless someone turned it off, as its Default is always.
	if on, chosen, err := s.AddonChoice(ctx, AddonLibrary); err == nil && (on || !chosen) {
		st.Library = &library
	}
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
		Method: "GET", Path: "/api/social-copies", Addon: AddonSocial, Tag: "Saved from social", Needs: api.Read,
		Summary: "List copies of videos saved from social apps",
		Doc:     "Groups of copies, proven as /api/duplicates proves them, that hold a video still waiting on the Saved from social page, with every copy wherever it is filed.",
		Params: []api.Param{
			api.Query("limit", "integer", "How many groups to return, 1 to 1000. 100 by default."),
		},
		Returns: []DuplicateGroup{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		limit, ok := intParam(w, r, "limit", 100)
		if !ok {
			return
		}
		groups, err := s.SocialDuplicates(ctx, limit)
		if err != nil {
			failFor(w, err, "limit should be 1 to 1000.")
			return
		}
		s.withSidecarFacts(ctx, groups)
		writeJSON(w, groups)
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
	// JellyfinConfigured says Jellyfin's address and key are both set.
	JellyfinConfigured bool
	// Photos is the Apple Photos helper's hub.
	Photos *PhotosHub
	// GooglePhotos reads the Takeout inbox, or is nil without one.
	GooglePhotos *GooglePhotos
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
				Summary:     "Add the higher-resolution copies Google Takeout holds beside the archive photos.",
				Description: "Some photos reached the archive smaller than they were taken. When a Google Takeout export holds the original, this shows the two side by side and adds the better one beside the photo, named with (hi-res). Nothing already in the archive is moved or replaced.",
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
				ID: AddonGooglePhotos, Name: "Google Photos", Version: "1", Icon: "photo_library",
				Summary:     "Bring into the library the photos Google Photos has and it does not.",
				Description: "Google no longer lets an app download a whole Google Photos library, so the photos come through Google Takeout: export Google Photos as .zip files and drop them into the inbox. Cull reads them where they are, checks each photo against the library, and adds the ones you choose under the day they were taken. The exports are only read, and nothing already in the archive is moved or replaced.",
				Pages:       []addon.Page{{ID: "google-photos", Label: "Google Photos", Icon: "photo_library", Section: addon.Sync, Path: "/google-photos"}},
				Needs:       []string{"A Takeout inbox folder, given with -takeout-inbox, mounted read-only"},
				Work:        []string{"Reads the inbox every five minutes and checks new photos against the library"},
			},
			Status: func(ctx context.Context) addon.Status {
				g := needs.GooglePhotos
				if !g.Configured() {
					return addon.Status{State: addon.Setup, Detail: "No Takeout inbox is set up. Its page says how to add one."}
				}
				if _, _, problem := g.status(); problem != "" {
					return addon.Status{State: addon.Problem, Detail: problem}
				}
				missing := countQuery(ctx, s.read, "SELECT count(*) FROM takeout_items i WHERE i.state='waiting' AND i.outcome IN ('missing','alternative') AND "+inInbox+" AND NOT "+takeoutQueued)
				if missing == 0 {
					return addon.Status{State: addon.Ready, Detail: "Nothing in the inbox is missing from the library."}
				}
				return addon.Status{State: addon.Ready, Detail: counted(missing, "photo in the inbox is", "photos in the inbox are") + " not in the library yet."}
			},
			Default: func(context.Context) bool { return needs.GooglePhotos.Configured() },
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
				ID: AddonJellyfin, Name: "Jellyfin", Version: "1", Icon: "videocam",
				Summary:     "Have Jellyfin scan the library again whenever files leave it or come back.",
				Description: "Jellyfin reads the library's folders as a media library, and on its own notices a video has gone only at its next scan. Once files have been moved to the Bin, put back, deleted from it or imported, and the library has held still for a minute, Cull asks Jellyfin to scan again through its API.",
				Needs:       []string{"JELLYFIN_URL and JELLYFIN_KEY set in the environment, or Jellyfin's address and an API key in Settings"},
				Work:        []string{"Asks Jellyfin to scan again after files leave or return to the library"},
			},
			Status: func(ctx context.Context) addon.Status {
				if !needs.JellyfinConfigured {
					return addon.Status{State: addon.Setup, Detail: "JELLYFIN_URL and JELLYFIN_KEY are not both set."}
				}
				last, problem := s.JellyfinStatus(ctx)
				if problem != "" {
					return addon.Status{State: addon.Problem, Detail: problem + ". Cull tries again on its own."}
				}
				if last.IsZero() {
					return addon.Status{State: addon.Ready, Detail: "Jellyfin has not been asked to scan yet."}
				}
				return addon.Status{State: addon.Ready, Detail: "Jellyfin was last asked to scan at " + last.Local().Format("15:04 on 2 January") + "."}
			},
			Default: func(context.Context) bool { return needs.JellyfinConfigured },
		},
		{
			Manifest: addon.Manifest{
				ID: AddonLibrary, Name: "Library totals", Version: "1", Icon: "perm_media",
				Summary:     "Show in the sidebar how many photos and videos the library holds and how much space they take.",
				Description: "The sidebar counts the library's photos, RAW files included, and its videos under the review meter, with their sizes and a total. Files in the Bin or missing from disk are not counted.",
			},
			Status: func(ctx context.Context) addon.Status {
				library, err := s.LibraryStats(ctx)
				if err != nil {
					return addon.Status{State: addon.Problem, Detail: "The library could not be counted."}
				}
				return addon.Status{State: addon.Ready, Detail: fmt.Sprintf("%s and %s.", counted(int(library.Photos.Files), "photo", "photos"), counted(int(library.Videos.Files), "video", "videos"))}
			},
			Default: always,
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
