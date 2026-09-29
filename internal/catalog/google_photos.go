package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"daddy-cull/next/internal/takeout"
)

// Google stopped letting apps read a whole Google Photos library through its
// API on 31 March 2025, so nothing can keep a copy in step the way icloudpd
// does for iCloud. What remains is Google Takeout: an export of every photo,
// as .zip files, which anyone can ask for by hand or have made every two
// months for a year.
//
// The exports are dropped into an inbox. Cull reads them where they are,
// pairs each photo with the JSON Google wrote beside it, and works out which
// photos the library already holds, which it holds a different copy of, and
// which it does not have at all. Nothing is added until someone asks: each
// photo asked for is copied into the archive by the private writer, under the
// date it was taken, and then catalogued like any other file.

// The outcomes a Google Photos item can come to.
const (
	TakeoutPending     = "pending"
	TakeoutMissing     = "missing"
	TakeoutAlternative = "alternative"
	TakeoutUncertain   = "uncertain"
	TakeoutRepresented = "represented"
	TakeoutRemoved     = "removed"
)

// takeoutFacts is what Google's JSON said about a photo, kept small.
type takeoutFacts struct {
	Title       string   `json:"title,omitempty"`
	Taken       int64    `json:"taken,omitempty"`
	Description string   `json:"description,omitempty"`
	Latitude    float64  `json:"latitude,omitempty"`
	Longitude   float64  `json:"longitude,omitempty"`
	People      []string `json:"people,omitempty"`
	Favourite   bool     `json:"favourite,omitempty"`
}

func factsFrom(s takeout.Sidecar) takeoutFacts {
	f := takeoutFacts{Title: s.Title, Description: strings.TrimSpace(s.Description), People: s.PeopleNames(), Favourite: s.Favorited}
	if taken, ok := s.Taken(); ok {
		f.Taken = taken.Unix()
	}
	if place, ok := s.Place(); ok {
		f.Latitude, f.Longitude = place.Latitude, place.Longitude
	}
	return f
}

// sidecarLimit is the most of a JSON file that is read. Google's are about a
// kilobyte; anything much bigger is not one of them.
const sidecarLimit = 256 << 10

// GooglePhotos reads the Takeout inbox and keeps what it found in the
// catalogue.
type GooglePhotos struct {
	s       *Store
	inbox   *takeout.Inbox
	dir     string
	archive string
	cache   string

	scan sync.Mutex
	wake chan struct{}

	mu        sync.Mutex
	scanning  bool
	scannedAt string
	problem   string
}

// NewGooglePhotos reads exports from the inbox at dir. archive is the
// archive's read-only mount, used to compare a photo with a library file of
// the same size under another name, and cache is where photos inside a zip
// are unpacked to be shown. Either may be empty.
func (s *Store) NewGooglePhotos(dir, archive, cache string) (*GooglePhotos, error) {
	if dir == "" {
		return &GooglePhotos{s: s, wake: make(chan struct{}, 1)}, nil
	}
	inbox, err := takeout.OpenInbox(dir)
	if err != nil {
		return nil, err
	}
	return &GooglePhotos{s: s, inbox: inbox, dir: dir, archive: archive, cache: cache, wake: make(chan struct{}, 1)}, nil
}

// Configured reports whether an inbox was given.
func (g *GooglePhotos) Configured() bool { return g != nil && g.inbox != nil }

// Close closes the inbox.
func (g *GooglePhotos) Close() error {
	if g.inbox == nil {
		return nil
	}
	return g.inbox.Close()
}

// Wake asks for the inbox to be read again now. The page shows it being
// read from now on.
func (g *GooglePhotos) Wake() {
	if !g.Configured() {
		return
	}
	g.mu.Lock()
	g.scanning = true
	g.mu.Unlock()
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

// takeoutEvery is how often the inbox is read while the addon is on. Reading
// an unchanged inbox only lists it, and checking the library again is quick.
const takeoutEvery = 5 * time.Minute

// Keep reads the inbox now and every few minutes until ctx ends, and when
// woken.
func (g *GooglePhotos) Keep(ctx context.Context) {
	if !g.Configured() {
		return
	}
	for ctx.Err() == nil {
		if err := g.Scan(ctx); err != nil && ctx.Err() == nil {
			log.Printf("google photos: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-g.wake:
		case <-time.After(takeoutEvery):
		}
	}
}

func (g *GooglePhotos) status() (scanning bool, scannedAt, problem string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.scanning, g.scannedAt, g.problem
}

// Scan reads what changed in the inbox, then checks every photo still waiting
// against the library as it is now.
func (g *GooglePhotos) Scan(ctx context.Context) (err error) {
	if !g.Configured() {
		return nil
	}
	g.scan.Lock()
	defer g.scan.Unlock()
	g.mu.Lock()
	g.scanning = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.scanning = false
		g.scannedAt = nowUTC()
		g.problem = ""
		if err != nil {
			g.problem = "The inbox could not be read: " + err.Error()
		}
		g.mu.Unlock()
	}()
	changed, err := g.readInbox(ctx)
	if err != nil {
		return err
	}
	if changed {
		if err = g.s.buildTakeoutItems(ctx); err != nil {
			return err
		}
	}
	return g.resolve(ctx)
}

// readInbox records each archive in the inbox, reading the files of those
// that are new or changed, and reports whether anything did.
func (g *GooglePhotos) readInbox(ctx context.Context) (bool, error) {
	found, err := g.inbox.List()
	if err != nil {
		return false, err
	}
	type known struct {
		id, size, modified int64
		kind, problem      string
		present            bool
	}
	before := map[string]known{}
	rows, err := g.s.read.QueryContext(ctx, "SELECT id,name,kind,size_bytes,modified,error,present FROM takeout_archives")
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var k known
		var name string
		if err := rows.Scan(&k.id, &name, &k.kind, &k.size, &k.modified, &k.problem, &k.present); err != nil {
			rows.Close()
			return false, err
		}
		before[name] = k
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	changed := false
	seen := map[string]bool{}
	for _, f := range found {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		seen[f.Name] = true
		k, ok := before[f.Name]
		if ok && k.present && k.kind == f.Kind && k.size == f.Size && k.modified == f.Modified.Unix() && k.problem == "" {
			continue
		}
		changed = true
		if err := g.readArchive(ctx, f); err != nil {
			return changed, err
		}
	}
	for name, k := range before {
		if !seen[name] && k.present {
			changed = true
			// Its files are forgotten; the photos found in it are kept, with
			// whatever became of them.
			if _, err := g.s.write.ExecContext(ctx, "BEGIN IMMEDIATE; DELETE FROM takeout_entries WHERE archive_id=?1; UPDATE takeout_archives SET present=0,media=0 WHERE id=?1; COMMIT;", k.id); err != nil {
				g.s.write.ExecContext(context.Background(), "ROLLBACK")
				return changed, err
			}
		}
	}
	return changed, nil
}

type takeoutEntryRow struct {
	path, innerDir, name, kind string
	size                       int64
	crc                        any
	facts                      string
}

// readArchive lists one archive's photos and reads its JSON files. An
// archive that cannot be read is recorded with why, and read again once it
// changes.
func (g *GooglePhotos) readArchive(ctx context.Context, f takeout.Found) error {
	var entries []takeoutEntryRow
	problem := ""
	media := 0
	switch f.Kind {
	case takeout.KindUnsupported:
		problem = "Cull reads .zip exports without unpacking them, and cannot read this kind. Ask Takeout for .zip files, or unpack this one into a folder in the inbox."
	default:
		archive, release, err := g.inbox.Archive(f.Name, f.Kind)
		if err != nil {
			problem = "It could not be opened: " + err.Error() + ". A zip still being copied is read again once it has arrived."
			break
		}
		for _, e := range archive.Entries() {
			if err := ctx.Err(); err != nil {
				release()
				return err
			}
			if !takeout.FromPhotos(e.Path) {
				continue
			}
			name := path.Base(e.Path)
			row := takeoutEntryRow{path: e.Path, innerDir: path.Dir(takeout.Inner(e.Path)), name: name, size: e.Size}
			if e.HasCRC {
				row.crc = int64(e.CRC32)
			}
			switch {
			case takeout.IsMedia(name) && e.Size > 0:
				row.kind = "media"
				media++
			case takeout.IsSidecar(name) && e.Size <= sidecarLimit:
				body, err := readEntry(archive, e.Path)
				if err != nil {
					continue
				}
				sidecar, ok := takeout.ParseSidecar(body)
				if !ok {
					continue
				}
				facts, _ := json.Marshal(factsFrom(sidecar))
				row.kind, row.facts = "sidecar", string(facts)
			default:
				continue
			}
			entries = append(entries, row)
		}
		release()
	}
	tx, err := g.s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO takeout_archives(name,kind,size_bytes,modified,media,scanned_at,error,present) VALUES(?,?,?,?,?,?,?,1)
		ON CONFLICT(name) DO UPDATE SET kind=excluded.kind,size_bytes=excluded.size_bytes,modified=excluded.modified,media=excluded.media,scanned_at=excluded.scanned_at,error=excluded.error,present=1
		RETURNING id`, f.Name, f.Kind, f.Size, f.Modified.Unix(), media, nowUTC(), problem).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM takeout_entries WHERE archive_id=?", id); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO takeout_entries(archive_id,path,inner_dir,name,kind,size_bytes,crc32,facts) VALUES(?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, e := range entries {
		if _, err := insert.ExecContext(ctx, id, e.path, e.innerDir, e.name, e.kind, e.size, e.crc, e.facts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readEntry(archive takeout.Archive, entry string) ([]byte, error) {
	r, err := archive.Open(entry)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, sidecarLimit+1))
}

// takeoutItemKey is what makes a photo one photo, however many exports and
// albums hold it: its name and its size.
type takeoutItemKey struct {
	name string
	size int64
}

type builtItem struct {
	key       takeoutItemKey
	kind      string
	takenAt   sql.NullInt64
	localAt   sql.NullInt64
	takenFrom string
	facts     takeoutFacts
	entries   [][2]any
}

// wallClock is the time as a clock on the wall showed it, stored as if it
// were UTC, as the catalogue stores the day of each file.
func wallClock(t time.Time) int64 {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC).Unix()
}

// libraryName is the name a photo goes into the library under: the one it
// was uploaded with, when Google cut the file's name short.
func libraryName(name string, facts takeoutFacts) string {
	title := facts.Title
	if title == "" || title == name || !takeout.SafeName(title) || !strings.EqualFold(path.Ext(title), path.Ext(name)) {
		return name
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	if len(title) > len(name) && strings.HasPrefix(strings.TrimSuffix(title, path.Ext(title)), stem) {
		return title
	}
	return name
}

// buildTakeoutItems pairs every photo in the inbox with its JSON, across the
// zips of an export, and records each photo once.
func (s *Store) buildTakeoutItems(ctx context.Context) error {
	rows, err := s.read.QueryContext(ctx, `SELECT e.archive_id,e.path,e.inner_dir,e.name,e.kind,e.size_bytes,e.facts
		FROM takeout_entries e JOIN takeout_archives a ON a.id=e.archive_id WHERE a.present=1 ORDER BY e.inner_dir,e.name,e.archive_id`)
	if err != nil {
		return err
	}
	type entry struct {
		archive                    int64
		path, name, kind, factsRaw string
		size                       int64
	}
	dirs := map[string][]entry{}
	var order []string
	for rows.Next() {
		var e entry
		var dir string
		if err := rows.Scan(&e.archive, &e.path, &dir, &e.name, &e.kind, &e.size, &e.factsRaw); err != nil {
			rows.Close()
			return err
		}
		if _, ok := dirs[dir]; !ok {
			order = append(order, dir)
		}
		dirs[dir] = append(dirs[dir], e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	items := map[takeoutItemKey]*builtItem{}
	var keys []takeoutItemKey
	for _, dir := range order {
		var names []string
		sidecars := map[string]string{}
		facts := map[string]takeoutFacts{}
		seen := map[string]bool{}
		for _, e := range dirs[dir] {
			if e.kind == "sidecar" {
				var f takeoutFacts
				if json.Unmarshal([]byte(e.factsRaw), &f) == nil {
					sidecars[e.name] = f.Title
					facts[e.name] = f
				}
			} else if !seen[e.name] {
				seen[e.name] = true
				names = append(names, e.name)
			}
		}
		paired := takeout.Pair(names, sidecars)
		for _, e := range dirs[dir] {
			if e.kind != "media" {
				continue
			}
			f, hasFacts := takeoutFacts{}, false
			if json, ok := paired[e.name]; ok {
				f, hasFacts = facts[json]
			}
			key := takeoutItemKey{name: libraryName(e.name, f), size: e.size}
			item, ok := items[key]
			if !ok {
				item = &builtItem{key: key, kind: takeout.Kind(e.name)}
				items[key] = item
				keys = append(keys, key)
			}
			item.entries = append(item.entries, [2]any{e.archive, e.path})
			// Google's own time is the surest; a time in the name comes next.
			switch {
			case hasFacts && f.Taken > 0 && item.takenFrom != "google":
				taken := time.Unix(f.Taken, 0)
				item.takenAt = sql.NullInt64{Int64: f.Taken, Valid: true}
				item.localAt = sql.NullInt64{Int64: wallClock(taken.In(time.Local)), Valid: true}
				item.takenFrom, item.facts = "google", f
			case item.takenFrom == "":
				if named, ok := takeout.NameTime(e.name); ok {
					local := time.Date(named.Year(), named.Month(), named.Day(), named.Hour(), named.Minute(), named.Second(), 0, time.Local)
					item.takenAt = sql.NullInt64{Int64: local.Unix(), Valid: true}
					item.localAt = sql.NullInt64{Int64: named.Unix(), Valid: true}
					item.takenFrom = "name"
				}
				if hasFacts {
					item.facts = f
				}
			}
		}
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	upsert, err := tx.PrepareContext(ctx, `INSERT INTO takeout_items(name,size_bytes,kind,taken_at,local_at,taken_from,facts,first_seen) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name,size_bytes) DO UPDATE SET
		 taken_at=CASE WHEN takeout_items.state='waiting' THEN excluded.taken_at ELSE takeout_items.taken_at END,
		 local_at=CASE WHEN takeout_items.state='waiting' THEN excluded.local_at ELSE takeout_items.local_at END,
		 taken_from=CASE WHEN takeout_items.state='waiting' THEN excluded.taken_from ELSE takeout_items.taken_from END,
		 facts=excluded.facts
		RETURNING id`)
	if err != nil {
		return err
	}
	defer upsert.Close()
	link, err := tx.PrepareContext(ctx, "UPDATE takeout_entries SET item_id=? WHERE archive_id=? AND path=?")
	if err != nil {
		return err
	}
	defer link.Close()
	now := nowUTC()
	for _, key := range keys {
		item := items[key]
		facts, _ := json.Marshal(item.facts)
		var id int64
		if err := upsert.QueryRowContext(ctx, key.name, key.size, item.kind, item.takenAt, item.localAt, item.takenFrom, string(facts), now).Scan(&id); err != nil {
			return err
		}
		for _, e := range item.entries {
			if _, err := link.ExecContext(ctx, id, e[0], e[1]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// copyMarks are what a name gains when a copy of a photo is saved beside
// it: by Cull, by Google Photos, or by a Mac or phone keeping two apart.
var copyMarks = regexp.MustCompile(`(?i)( \(hi-res\)| \(google photos\)| ?\(\d+\)|-edited|-bearbeitet)+$`)

// normalName is a name as it is compared: without copy marks, in lower case,
// and with .jpeg the same as .jpg.
func normalName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	stem := copyMarks.ReplaceAllString(strings.TrimSuffix(name, path.Ext(name)), "")
	return strings.ToLower(stem) + ext
}

type libraryFile struct {
	id      int64
	path    string
	size    int64
	day     int64
	removed bool
}

type waitingItem struct {
	id       int64
	name     string
	size     int64
	kind     string
	day      int64
	dated    bool
	innerDir string
	entry    string
	path     string
	archive  string
	akind    string
	crc      sql.NullInt64
}

type resolution struct {
	outcome, reason string
	match           sql.NullInt64
}

// resolve checks every photo still waiting against the library as it is now:
// a photo someone removed in Cull since stops being offered, and one the
// nightly download brought in stops being missing.
func (g *GooglePhotos) resolve(ctx context.Context) error {
	s := g.s
	library, err := s.libraryFiles(ctx)
	if err != nil {
		return err
	}
	byName := map[string][]*libraryFile{}
	bySize := map[int64][]*libraryFile{}
	for _, f := range library {
		n := normalName(path.Base(f.path))
		byName[n] = append(byName[n], f)
		bySize[f.size] = append(bySize[f.size], f)
	}
	legacy := map[takeoutItemKey]bool{}
	rows, err := s.read.QueryContext(ctx, "SELECT original_path,size_bytes FROM legacy_culled WHERE restored_at IS NULL")
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		var size int64
		if err := rows.Scan(&p, &size); err != nil {
			rows.Close()
			return err
		}
		legacy[takeoutItemKey{normalName(path.Base(p)), size}] = true
	}
	rows.Close()
	items, err := s.waitingTakeoutItems(ctx)
	if err != nil {
		return err
	}
	compared, err := s.takeoutCompared(ctx)
	if err != nil {
		return err
	}
	results := map[int64]resolution{}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		results[item.id] = g.resolveOne(ctx, item, byName, bySize, legacy, compared)
	}
	// The moving part of a Live Photo is a video named as its still. The
	// library keeps it apart from the day's files, so it is not found by
	// name, and follows its still instead.
	stills := map[string]resolution{}
	for _, item := range items {
		if item.kind != "video" {
			stills[item.innerDir+"/"+strings.ToLower(strings.TrimSuffix(item.entry, path.Ext(item.entry)))] = results[item.id]
		}
	}
	for _, item := range items {
		r := results[item.id]
		if item.kind != "video" || r.outcome != TakeoutMissing {
			continue
		}
		still, ok := stills[item.innerDir+"/"+strings.ToLower(strings.TrimSuffix(item.entry, path.Ext(item.entry)))]
		if ok && (still.outcome == TakeoutRepresented || still.outcome == TakeoutRemoved) {
			reason := "The moving part of a Live Photo whose still is already in the library."
			if still.outcome == TakeoutRemoved {
				reason = "The moving part of a Live Photo whose still you removed in Cull."
			}
			results[item.id] = resolution{outcome: still.outcome, reason: reason, match: still.match}
		}
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	update, err := tx.PrepareContext(ctx, "UPDATE takeout_items SET outcome=?,reason=?,match_asset_id=? WHERE id=? AND state='waiting' AND (outcome<>? OR reason<>? OR match_asset_id IS NOT ?)")
	if err != nil {
		return err
	}
	defer update.Close()
	for id, r := range results {
		if _, err := update.ExecContext(ctx, r.outcome, r.reason, r.match, id, r.outcome, r.reason, r.match); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (g *GooglePhotos) resolveOne(ctx context.Context, item waitingItem, byName map[string][]*libraryFile, bySize map[int64][]*libraryFile, legacy map[takeoutItemKey]bool, compared map[[2]int64]bool) resolution {
	name := normalName(item.name)
	near := func(f *libraryFile) bool { return !item.dated || abs(f.day-item.day) <= 1 }
	// kept picks the library file that still holds the photo, or reports
	// that every one was removed in Cull.
	kept := func(files []*libraryFile) (resolution, bool) {
		if len(files) == 0 {
			return resolution{}, false
		}
		for _, f := range files {
			if !f.removed {
				return resolution{outcome: TakeoutRepresented, reason: "The library holds this photo as " + displayPath(f.path) + ".", match: sql.NullInt64{Int64: f.id, Valid: true}}, true
			}
		}
		return resolution{outcome: TakeoutRemoved, reason: "You removed this photo in Cull, so it is not offered again.", match: sql.NullInt64{Int64: files[0].id, Valid: true}}, true
	}
	var same []*libraryFile
	for _, f := range byName[name] {
		if f.size == item.size {
			same = append(same, f)
		}
	}
	if r, ok := kept(same); ok {
		return r
	}
	if legacy[takeoutItemKey{name, item.size}] {
		return resolution{outcome: TakeoutRemoved, reason: "You removed this photo with the earlier Daddy Cull, so it is not offered again."}
	}
	// Google may have renamed it. A file of the same size from around the
	// same day is compared byte for byte.
	var identical []*libraryFile
	uncompared := false
	for _, f := range bySize[item.size] {
		if normalName(path.Base(f.path)) == name || !near(f) {
			continue
		}
		result, ok := compared[[2]int64{item.id, f.id}]
		if !ok {
			var err error
			result, err = g.compare(ctx, item, f)
			if err != nil {
				uncompared = true
				continue
			}
		}
		if result {
			identical = append(identical, f)
		}
	}
	if r, ok := kept(identical); ok {
		r.reason = strings.TrimSuffix(r.reason, ".") + ", under another name."
		if r.outcome == TakeoutRemoved {
			r.reason = "You removed this photo in Cull, under another name, so it is not offered again."
		}
		return r
	}
	var alternatives []*libraryFile
	for _, f := range byName[name] {
		if near(f) {
			alternatives = append(alternatives, f)
		}
	}
	if len(alternatives) > 0 {
		if r, ok := kept(alternatives); ok && r.outcome == TakeoutRemoved {
			r.reason = "You removed a copy of this photo in Cull, so this one is not offered either."
			return r
		}
		sort.Slice(alternatives, func(a, b int) bool { return abs(alternatives[a].size-item.size) < abs(alternatives[b].size-item.size) })
		for _, f := range alternatives {
			if !f.removed {
				return resolution{outcome: TakeoutAlternative, match: sql.NullInt64{Int64: f.id, Valid: true},
					reason: fmt.Sprintf("The library holds a photo of this name from the same day, %s against %s here. Google may have kept a smaller or edited copy.", humanBytes(f.size), humanBytes(item.size))}
			}
		}
	}
	if uncompared {
		return resolution{outcome: TakeoutUncertain, reason: "The library holds a file of the same size from around that day under another name, which could not be compared."}
	}
	if !item.dated {
		return resolution{outcome: TakeoutUncertain, reason: "Google did not say when this was taken, and its name has no date, so Cull cannot tell which day it belongs to."}
	}
	return resolution{outcome: TakeoutMissing, reason: "The library has no copy of this photo."}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func displayPath(p string) string { return strings.TrimPrefix(p, "/archive/") }

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// libraryFiles is every file of the archive, with whether it was removed in
// Cull: moved to the Bin, deleted, or marked to remove.
func (s *Store) libraryFiles(ctx context.Context) ([]*libraryFile, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.size_bytes,a.captured_at,
		(COALESCE(fs.state,'restored')<>'restored' OR COALESCE(d.status,'')='cull')
		FROM assets a LEFT JOIN file_state fs ON fs.asset_id=a.id LEFT JOIN decisions d ON d.asset_id=a.id WHERE a.source_id='archive'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []*libraryFile
	for rows.Next() {
		f := &libraryFile{}
		var captured int64
		if err := rows.Scan(&f.id, &f.path, &f.size, &captured, &f.removed); err != nil {
			return nil, err
		}
		f.day = floorDay(captured)
		files = append(files, f)
	}
	return files, rows.Err()
}

func floorDay(unix int64) int64 {
	if unix < 0 {
		return (unix - 86399) / 86400
	}
	return unix / 86400
}

// waitingTakeoutItems are the photos still waiting that an archive in the
// inbox holds, each with the file to read it from.
func (s *Store) waitingTakeoutItems(ctx context.Context) ([]waitingItem, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT i.id,i.name,i.size_bytes,i.kind,i.local_at,e.inner_dir,e.name,e.path,a.name,a.kind,e.crc32
		FROM takeout_items i JOIN takeout_entries e ON e.rowid=(
			SELECT e2.rowid FROM takeout_entries e2 JOIN takeout_archives a2 ON a2.id=e2.archive_id
			WHERE e2.item_id=i.id AND a2.present=1 ORDER BY a2.kind='folder' DESC,a2.id LIMIT 1)
		JOIN takeout_archives a ON a.id=e.archive_id
		WHERE i.state='waiting'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []waitingItem
	for rows.Next() {
		var item waitingItem
		var local sql.NullInt64
		if err := rows.Scan(&item.id, &item.name, &item.size, &item.kind, &local, &item.innerDir, &item.entry, &item.path, &item.archive, &item.akind, &item.crc); err != nil {
			return nil, err
		}
		item.dated = local.Valid
		item.day = floorDay(local.Int64)
		items = append(items, item)
	}
	return items, rows.Err()
}

// takeoutCompared is every comparison made before, by photo and library
// file. A library file whose size changed since is compared again.
func (s *Store) takeoutCompared(ctx context.Context) (map[[2]int64]bool, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT c.item_id,c.asset_id,c.same FROM takeout_compared c JOIN assets a ON a.id=c.asset_id AND a.size_bytes=c.asset_size")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	compared := map[[2]int64]bool{}
	for rows.Next() {
		var item, asset int64
		var same bool
		if err := rows.Scan(&item, &asset, &same); err != nil {
			return nil, err
		}
		compared[[2]int64{item, asset}] = same
	}
	return compared, rows.Err()
}

// compare reads a photo and a library file of the same size and reports
// whether they are the same bytes, remembering the answer. The zip's
// checksum is compared first, which needs only the library file read.
func (g *GooglePhotos) compare(ctx context.Context, item waitingItem, f *libraryFile) (bool, error) {
	if g.archive == "" || !strings.HasPrefix(f.path, "/archive/") {
		return false, errors.New("the archive is not mounted")
	}
	root, err := os.OpenRoot(g.archive)
	if err != nil {
		return false, err
	}
	defer root.Close()
	rel := strings.TrimPrefix(f.path, "/archive/")
	if _, err := guardedRegular(root, rel, false); err != nil {
		return false, err
	}
	sum := func(r io.Reader) (uint32, [32]byte, int64, error) {
		c, h := crc32.NewIEEE(), sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(c, h), &contextReader{ctx, r}, make([]byte, 1<<20))
		var digest [32]byte
		copy(digest[:], h.Sum(nil))
		return c.Sum32(), digest, n, err
	}
	file, err := root.Open(rel)
	if err != nil {
		return false, err
	}
	libraryCRC, libraryHash, n, err := sum(file)
	file.Close()
	if err != nil {
		return false, err
	}
	same := n == item.size
	if same && item.crc.Valid && uint32(item.crc.Int64) != libraryCRC {
		same = false
	}
	if same {
		r, err := g.inbox.ReadEntry(item.archive, item.akind, item.path)
		if err != nil {
			return false, err
		}
		_, photoHash, m, err := sum(r)
		r.Close()
		if err != nil {
			return false, err
		}
		same = m == item.size && photoHash == libraryHash
	}
	g.s.write.ExecContext(ctx, "INSERT INTO takeout_compared(item_id,asset_id,asset_size,same) VALUES(?,?,?,?) ON CONFLICT(item_id,asset_id) DO UPDATE SET asset_size=excluded.asset_size,same=excluded.same", item.id, f.id, f.size, same)
	return same, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
