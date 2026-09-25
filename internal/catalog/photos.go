package catalog

import (
	"context"
	"database/sql"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Apple Photos holds its own copy of most of the archive, because the archive
// was filled from iCloud. Removing a file here does not remove it there, so
// without a handoff every photograph would have to be culled twice. This file
// decides what Photos should be told; the Mac helper, Cull Sync, finds those
// photographs in the Photos library and acts, and photos_hub.go carries the
// conversation between the two.
//
// Two lists come out of it, because they are two different claims. A deletion
// says "the reviewer took this out of the archive, so take it out of Photos
// too"; a favourite says "this one matters, mark it". Both are matched in Photos
// on the name Photos knows the file by, its extension and the day it was taken.
//
// The rule that matters most is the one that withholds deletions. The archive
// often holds one photograph twice - IMG_2866.MOV beside IMG_2866 (2).MOV from
// an ingest collision, or an original beside the Takeout copy that upgraded it -
// and Photos holds it once. Removing either archive copy leaves the photograph
// on disk, so offering the Photos asset for deletion would throw away the last
// cloud copy of something still being kept. A removal is therefore only offered
// once nothing live in the archive still has the same normalised name on the
// same day.

// photosSuffix must stay identical to normalise() in the Mac helper, and to
// SUFFIX_RE in the retired cull-sync.py it replaces. It strips the suffixes the
// archive's own duplicate factories add: " (2)" from an ingest collision,
// "-1234567" from a size disambiguation, " (2023-08-25)" from a re-download. If
// the two sides drift, the join quietly returns fewer matches and it looks as
// though Photos simply does not have the file.
var photosSuffix = regexp.MustCompile(`(\s\(\d+\)|\((?:r\s*)\d+\)|\s\(\d{4}-\d{2}-\d{2}\)|-\d{3,})$`)

// photosDay is the only day format Photos can be matched on.
var photosDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// PhotosNormalise returns a filename stem with any import suffix removed. An
// underscore before digits is left alone: that is how every camera here names
// its files, so stripping it would turn IMG_2866 into IMG.
func PhotosNormalise(stem string) string {
	s := stem
	// Repeated, because " (2023-08-25) (2)" happens.
	for range 3 {
		next := photosSuffix.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	return strings.TrimSpace(s)
}

// photosSplit separates a filename into its stem and lower-case extension the
// way Python's pathlib did for the old script, which the helper copies too: a
// leading dot or a trailing one does not start an extension.
func photosSplit(name string) (stem, ext string) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return name, ""
	}
	return name[:dot], strings.ToLower(name[dot+1:])
}

// photosName is the name Apple Photos knows a file by, and the day it was taken.
//
// A screenshot pulled out of the archive is renamed on the way into the holding
// area - IMG_4938.PNG becomes 2022-06-22_IMG_4938.PNG - so the name on disk no
// longer carries the name Photos has. Matching on it would look right and match
// nothing at all, silently, for every screenshot ever removed. The prefix is
// stripped back off here, and its date is also the more reliable one: it was
// read from the file's own day folder at the moment it was moved.
func photosName(file, day string) (name, photosDay string) {
	name = path.Base(file)
	if strings.HasPrefix(file, "/screenshots/") {
		if m := screenshotDatedName.FindStringSubmatch(name); m != nil {
			return m[4], m[1] + "-" + m[2] + "-" + m[3]
		}
	}
	return name, day
}

// photosNeverOffer lists cull reasons that say nothing about whether the
// photograph still exists. Resolving a shadowed pair in the earlier tool
// removed one of two physical files backing the same archive path: the
// photograph is exactly as present afterwards as before. The surviving-copy
// check would withhold these anyway, but that check infers "same photograph"
// from a filename, and it should not be the only thing standing between a bulk
// disk-hygiene operation and Apple Photos. A reason recorded at cull time is a
// fact, not an inference.
var photosNeverOffer = []string{"shadowed:"}

// PhotosEntry is one thing to do in Photos. Keys names every catalogue item it
// stands for: two removed copies of one photograph are one Photos asset, so
// they are one entry and are recorded together.
type PhotosEntry struct {
	ID       string   `json:"id"`
	Action   string   `json:"action"`
	Keys     []string `json:"keys"`
	Name     string   `json:"name"`
	Stem     string   `json:"stem"`
	Ext      string   `json:"ext"`
	Day      string   `json:"day"`
	Original string   `json:"original"`
	Kind     string   `json:"kind"`
	State    string   `json:"state,omitempty"`
	Preview  string   `json:"preview,omitempty"`
}

// PhotosHeld is a removal withheld because the archive still holds the
// photograph under another file.
type PhotosHeld struct {
	Name string `json:"name"`
	Day  string `json:"day"`
	Kept string `json:"kept"`
}

// PhotosRestored is a photograph deleted from Photos and later put back in the
// archive. PhotoKit cannot undelete, so all Cull can do is say so.
type PhotosRestored struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Day      string `json:"day"`
	SyncedAt string `json:"syncedAt"`
}

// PhotosPlan is everything Photos has not yet been told.
type PhotosPlan struct {
	Delete    []PhotosEntry    `json:"delete"`
	Favourite []PhotosEntry    `json:"favourite"`
	Held      []PhotosHeld     `json:"held"`
	Undated   int              `json:"undated"`
	Restored  []PhotosRestored `json:"restored"`
}

type photosItem struct {
	key, file, day, kind, state, preview string
	capturedAt                           int64
}

// PhotosPlan works out what Photos should be told now.
//
// A deletion is every file the reviewer took out of the archive through this
// app and has not restored: whatever is in the Bin from any source, and
// whatever was emptied from it for good. Files emptied for good are included
// deliberately. They are the most certain deletions in the system, and leaving
// them out would mean the only way to lose the Photos copy is to sync before
// emptying the Bin.
func (s *Store) PhotosPlan(ctx context.Context) (PhotosPlan, error) {
	plan := PhotosPlan{Delete: []PhotosEntry{}, Favourite: []PhotosEntry{}, Held: []PhotosHeld{}, Restored: []PhotosRestored{}}
	synced, err := s.photosSynced(ctx)
	if err != nil {
		return plan, err
	}
	removed, err := s.photosRemoved(ctx)
	if err != nil {
		return plan, err
	}
	live, err := s.photosLive(ctx)
	if err != nil {
		return plan, err
	}

	removedKeys := make(map[string]bool, len(removed))
	for _, item := range removed {
		removedKeys[item.key] = true
	}
	plan.Delete, plan.Held, plan.Undated = photosEntries("delete", removed, synced, live)
	favourites, err := s.photosFavourites(ctx)
	if err != nil {
		return plan, err
	}
	var undated int
	plan.Favourite, _, undated = photosEntries("favourite", favourites, synced, nil)
	plan.Undated += undated

	// A photograph deleted from Photos and back in the archive now is one
	// Photos no longer has. That cannot be undone from here, only reported.
	for _, row := range synced {
		if row.action == "delete" && !removedKeys[row.key] {
			plan.Restored = append(plan.Restored, PhotosRestored{Key: row.key, Name: row.name, Day: row.day, SyncedAt: row.syncedAt})
		}
	}
	sort.Slice(plan.Restored, func(i, j int) bool {
		a, b := plan.Restored[i], plan.Restored[j]
		if a.SyncedAt != b.SyncedAt {
			return a.SyncedAt > b.SyncedAt
		}
		return a.Key < b.Key
	})
	return plan, nil
}

// photosEntries turns catalogue items into Photos entries: undated items are
// counted and skipped, because Photos is keyed on the day and there is no safe
// match without one; items already synced are skipped; removals the archive
// still holds another copy of are withheld; and items that are one Photos
// asset are folded into one entry.
func photosEntries(action string, items []photosItem, synced map[string]photosSyncRow, live map[string]string) ([]PhotosEntry, []PhotosHeld, int) {
	entries := make([]PhotosEntry, 0)
	held := make([]PhotosHeld, 0)
	undated := 0
	index := map[string]int{}
	for _, item := range items {
		name, day := photosName(item.file, item.day)
		if !photosDay.MatchString(day) {
			undated++
			continue
		}
		if _, done := synced[item.key+"\x00"+action]; done {
			continue
		}
		stem, ext := photosSplit(name)
		stem = PhotosNormalise(stem)
		if live != nil {
			// The extension is deliberately left out of this test, unlike the
			// match in Photos. A RAW beside its JPEG, or an original beside
			// Google's re-encode of it, is the same photograph under another
			// extension, and Photos may hold them as one asset. Withholding
			// one removal too many costs a second sync; one too few costs a
			// photograph.
			if kept, ok := live[stem+"\x00"+day]; ok {
				held = append(held, PhotosHeld{Name: name, Day: day, Kept: kept})
				continue
			}
		}
		group := stem + "\x00" + ext + "\x00" + day
		if at, seen := index[group]; seen {
			entries[at].Keys = append(entries[at].Keys, item.key)
			continue
		}
		index[group] = len(entries)
		entries = append(entries, PhotosEntry{
			ID: action + ":" + item.key, Action: action, Keys: []string{item.key},
			Name: name, Stem: stem, Ext: ext, Day: day,
			Original: item.file, Kind: item.kind, State: item.state, Preview: item.preview,
		})
	}
	return entries, held, undated
}

// photosRemoved lists every file taken out of the archive and not restored,
// from all four places a removal is recorded.
func (s *Store) photosRemoved(ctx context.Context) ([]photosItem, error) {
	// Previews come from the Bin's own listing, so a card here shows exactly
	// what the Bin shows. A file emptied for good has none.
	trash, err := s.Trash(ctx)
	if err != nil {
		return nil, err
	}
	byAsset, byLegacy, byShot := map[int64]string{}, map[int64]string{}, map[string]string{}
	for _, item := range trash {
		switch item.Source {
		case "marked", "bin":
			byAsset[item.assetID] = item.Preview
		case "legacy":
			byLegacy[item.legacyID] = item.Preview
		case "screenshot":
			byShot[item.planID] = item.Preview
		}
	}

	items := make([]photosItem, 0)
	seen := map[string]bool{}
	add := func(item photosItem) {
		if !seen[item.key] {
			seen[item.key] = true
			items = append(items, item)
		}
	}
	assetKey := func(id int64) string { return "asset:" + strconv.FormatInt(id, 10) }

	// Marked in review and not yet moved: still on disk, but in the Bin.
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind FROM assets a JOIN decisions d ON d.asset_id=a.id
		WHERE d.status='cull' AND a.source_id='archive' AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')`)
	if err != nil {
		return nil, err
	}
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var item photosItem
		var id int64
		if err := scan(&id, &item.file, &item.capturedAt, &item.kind); err != nil {
			return err
		}
		item.key, item.state, item.preview = assetKey(id), "marked", byAsset[id]
		item.day, _ = archiveDay(item.file, item.capturedAt)
		add(item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Moved by this app's writer, whether still in the Bin or emptied for good.
	rows, err = s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind,fs.state FROM file_state fs JOIN assets a ON a.id=fs.asset_id WHERE fs.state!='restored'`)
	if err != nil {
		return nil, err
	}
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var item photosItem
		var id int64
		var state string
		if err := scan(&id, &item.file, &item.capturedAt, &item.kind, &state); err != nil {
			return err
		}
		item.key, item.state = assetKey(id), photosRemovalState(state)
		if item.state == "bin" {
			item.preview = byAsset[id]
		}
		item.day, _ = archiveDay(item.file, item.capturedAt)
		add(item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Screenshots removed from the holding area.
	rows, err = s.read.QueryContext(ctx, `SELECT sp.id,a.id,a.relative_path,a.captured_at,a.kind,json_extract(sp.body,'$.state') FROM screenshot_plans sp JOIN assets a ON a.id=sp.asset_id
		WHERE json_extract(sp.body,'$.action')='remove' AND json_extract(sp.body,'$.state') IN ('bin','restoring','purging','purged','purged_recovered') ORDER BY sp.rowid`)
	if err != nil {
		return nil, err
	}
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var item photosItem
		var planID, state string
		var id int64
		if err := scan(&planID, &id, &item.file, &item.capturedAt, &item.kind, &state); err != nil {
			return err
		}
		item.key, item.state = assetKey(id), photosRemovalState(state)
		if item.state == "bin" {
			item.preview = byShot[planID]
		}
		item.day, _ = archiveDay(item.file, item.capturedAt)
		add(item)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The earlier tool's history. A row it already told Photos about carries
	// photos_deleted_at and is done; so is anything its sidecar rows describe,
	// since a sidecar is never an asset in Photos.
	rows, err = s.read.QueryContext(ctx, `SELECT legacy_id,original_path,COALESCE(day,''),COALESCE(reason,''),purged_at IS NOT NULL FROM legacy_culled
		WHERE kind!='sidecar' AND restored_at IS NULL AND photos_deleted_at IS NULL ORDER BY legacy_id`)
	if err != nil {
		return nil, err
	}
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var item photosItem
		var id int64
		var reason string
		var purged bool
		if err := scan(&id, &item.file, &item.day, &reason, &purged); err != nil {
			return err
		}
		for _, prefix := range photosNeverOffer {
			if strings.HasPrefix(reason, prefix) {
				return nil
			}
		}
		item.key, item.kind, item.state = "legacy:"+strconv.FormatInt(id, 10), mediaKind(item.file), "bin"
		if purged {
			item.state = "purged"
		} else {
			item.preview = byLegacy[id]
		}
		if item.day == "" {
			item.day, _ = archiveDay(item.file, 0)
		}
		add(item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// photosRemovalState folds the writers' many states into the two a reviewer
// cares about: still in the Bin, or gone for good.
func photosRemovalState(state string) string {
	if state == "purged" || state == "purged_recovered" {
		return "purged"
	}
	return "bin"
}

// photosLive indexes every photograph the archive still holds, by normalised
// stem and day, naming one file that holds it. Files in the Bin do not count;
// neither does a screenshot removed from the holding area. Google Takeout and
// the physical disk copies are staging and plumbing, not the archive, so they
// do not count either.
func (s *Store) photosLive(ctx context.Context) (map[string]string, error) {
	live := map[string]string{}
	add := func(file string, capturedAt int64) {
		day, _ := archiveDay(file, capturedAt)
		name, day := photosName(file, day)
		if !photosDay.MatchString(day) {
			return
		}
		stem, _ := photosSplit(name)
		key := PhotosNormalise(stem) + "\x00" + day
		if _, ok := live[key]; !ok {
			live[key] = name
		}
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.relative_path,a.captured_at FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id
		WHERE a.source_id='archive' AND COALESCE(d.status,'unreviewed')!='cull' AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
		UNION ALL
		SELECT a.relative_path,a.captured_at FROM screenshot_items si JOIN assets a ON a.id=si.asset_id WHERE si.state IN ('waiting','kept')`)
	if err != nil {
		return nil, err
	}
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var file string
		var capturedAt int64
		if err := scan(&file, &capturedAt); err != nil {
			return err
		}
		add(file, capturedAt)
		return nil
	})
	return live, err
}

// photosFavourites lists every hearted photograph the archive still holds. A
// favourite that has since been removed is not a favourite any more; the
// deletion list speaks for it.
func (s *Store) photosFavourites(ctx context.Context) ([]photosItem, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind FROM decisions d JOIN assets a ON a.id=d.asset_id
		WHERE d.favourite=1 AND d.status!='cull' AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
		  AND (a.source_id='archive' OR (a.source_id='screenshots' AND EXISTS(SELECT 1 FROM screenshot_items si WHERE si.asset_id=a.id AND si.state IN ('waiting','kept'))))
		ORDER BY a.captured_at,a.id`)
	if err != nil {
		return nil, err
	}
	items := make([]photosItem, 0)
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var item photosItem
		var id int64
		if err := scan(&id, &item.file, &item.capturedAt, &item.kind); err != nil {
			return err
		}
		item.key = "asset:" + strconv.FormatInt(id, 10)
		item.preview = "/api/media/" + strconv.FormatInt(id, 10)
		item.day, _ = archiveDay(item.file, item.capturedAt)
		items = append(items, item)
		return nil
	})
	return items, err
}

func scanPhotosRows(rows *sql.Rows, each func(scan func(...any) error) error) error {
	defer rows.Close()
	for rows.Next() {
		if err := each(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

type photosSyncRow struct {
	key, action, syncedAt, name, day string
}

func (s *Store) photosSynced(ctx context.Context) (map[string]photosSyncRow, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT asset_key,action,synced_at,name,day FROM photos_sync")
	if err != nil {
		return nil, err
	}
	synced := map[string]photosSyncRow{}
	err = scanPhotosRows(rows, func(scan func(...any) error) error {
		var row photosSyncRow
		if err := scan(&row.key, &row.action, &row.syncedAt, &row.name, &row.day); err != nil {
			return err
		}
		synced[row.key+"\x00"+row.action] = row
		return nil
	})
	return synced, err
}

// PhotosDone is one entry Photos has confirmed, with the assets it touched.
type PhotosDone struct {
	Entry    PhotosEntry
	PhotosID []string
}

// RecordPhotosSync stamps entries Photos has confirmed, so they are never
// offered again. Only entries the server itself produced reach this, which is
// what stops a mistaken or replayed report from marking the whole Bin as done.
func (s *Store) RecordPhotosSync(ctx context.Context, done []PhotosDone, now time.Time) (int, error) {
	if len(done) == 0 {
		return 0, nil
	}
	stamp := now.UTC().Format(time.RFC3339)
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, item := range done {
		if item.Entry.Action != "delete" && item.Entry.Action != "favourite" {
			return 0, ErrInvalid
		}
		for _, key := range item.Entry.Keys {
			if _, err = tx.ExecContext(ctx, `INSERT INTO photos_sync(asset_key,action,synced_at,photos_id,name,day) VALUES(?,?,?,?,?,?)
				ON CONFLICT(asset_key,action) DO UPDATE SET synced_at=excluded.synced_at,photos_id=excluded.photos_id,name=excluded.name,day=excluded.day`,
				key, item.Entry.Action, stamp, strings.Join(item.PhotosID, " "), item.Entry.Name, item.Entry.Day); err != nil {
				return 0, err
			}
			n++
		}
	}
	return n, tx.Commit()
}

// ForgetPhotosDeletions drops the record that Photos deleted a photograph the
// archive has since taken back. The reviewer does this once they have
// recovered it from Recently Deleted, or decided not to; if the photograph is
// removed again later it is then offered to Photos again, which is right,
// because Photos holds it again. Only rows that really are restored can go.
func (s *Store) ForgetPhotosDeletions(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 || len(keys) > 10000 {
		return 0, ErrInvalid
	}
	plan, err := s.PhotosPlan(ctx)
	if err != nil {
		return 0, err
	}
	restored := map[string]bool{}
	for _, row := range plan.Restored {
		restored[row.Key] = true
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, key := range keys {
		if !restored[key] {
			continue
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM photos_sync WHERE asset_key=? AND action='delete'", key)
		if err != nil {
			return 0, err
		}
		affected, _ := result.RowsAffected()
		n += int(affected)
	}
	return n, tx.Commit()
}

// PhotosSyncCounts is the running total shown on the page.
func (s *Store) PhotosSyncCounts(ctx context.Context) (deleted, favourited int, last string) {
	deleted = countQuery(ctx, s.read, "SELECT count(*) FROM photos_sync WHERE action='delete'")
	favourited = countQuery(ctx, s.read, "SELECT count(*) FROM photos_sync WHERE action='favourite'")
	_ = s.read.QueryRowContext(ctx, "SELECT COALESCE(max(synced_at),'') FROM photos_sync").Scan(&last)
	return
}
