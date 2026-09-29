package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GooglePhotosPage is the Google Photos page: the exports in the inbox, how
// many photos each tab holds, and one tab's photos.
type GooglePhotosPage struct {
	// Inbox is false when Cull was started without a Takeout inbox, so there
	// is nowhere to drop exports yet.
	Inbox bool `json:"inbox"`
	// Scanning is true while the inbox is being read and its photos checked
	// against the library.
	Scanning bool `json:"scanning"`
	// ScannedAt is when the inbox was last read, in RFC 3339 and UTC, or
	// empty before the first time since Cull started.
	ScannedAt string `json:"scannedAt"`
	// Problem says why the inbox could not be read the last time, or is
	// empty.
	Problem string `json:"problem,omitempty"`
	// Archives are the exports found in the inbox, by name.
	Archives []TakeoutArchive `json:"archives"`
	// Counts counts the photos in each tab.
	Counts GooglePhotosCounts `json:"counts"`
	// Tab is the tab listed: missing, alternative, uncertain, represented,
	// removed, added or skipped.
	Tab string `json:"tab"`
	// Items are the tab's photos, oldest first, 120 at a time.
	Items []GooglePhotosItem `json:"items"`
	// Next is the from to ask for to read on, or 0 when this is the end.
	Next int `json:"next"`
}

// TakeoutArchive is one export, or one zip of it, in the inbox.
type TakeoutArchive struct {
	// Name is where it is in the inbox, such as takeout-20240101T000000Z-001.zip.
	Name string `json:"name"`
	// Kind is zip, folder for an export unpacked into a folder, or
	// unsupported for one Cull cannot read, such as a .tgz.
	Kind string `json:"kind"`
	// Size is its size in bytes.
	Size int64 `json:"size"`
	// Media counts the photos and videos in it.
	Media int `json:"media"`
	// ScannedAt is when it was last read, in RFC 3339 and UTC.
	ScannedAt string `json:"scannedAt"`
	// Problem says why it could not be read, or is empty.
	Problem string `json:"problem,omitempty"`
}

// GooglePhotosCounts counts the photos in the inbox by what they came to.
type GooglePhotosCounts struct {
	// Missing counts photos the library has no copy of.
	Missing int `json:"missing"`
	// Alternative counts photos the library holds a different copy of,
	// under the same name from the same day.
	Alternative int `json:"alternative"`
	// Uncertain counts photos Cull cannot place: with no date, or like a
	// file it could not compare.
	Uncertain int `json:"uncertain"`
	// Represented counts photos already in the library.
	Represented int `json:"represented"`
	// Removed counts photos removed in Cull, which are not offered again.
	Removed int `json:"removed"`
	// Added counts photos added to the library from Google Photos.
	Added int `json:"added"`
	// Skipped counts photos someone chose not to add.
	Skipped int `json:"skipped"`
	// Checking counts photos not checked against the library yet.
	Checking int `json:"checking"`
}

// GooglePhotosItem is one photo or video from Google Photos.
type GooglePhotosItem struct {
	// ID is the photo's id in the routes below.
	ID int64 `json:"id"`
	// Name is the name it goes into the library under: the one it was
	// uploaded with.
	Name string `json:"name"`
	// Kind is image, raw or video.
	Kind string `json:"kind"`
	// Size is its size in bytes.
	Size int64 `json:"size"`
	// Taken is when it was taken, as the clock where it was taken showed
	// it, as YYYY-MM-DDTHH:MM:SS, or empty when nobody knows.
	Taken string `json:"taken"`
	// TakenFrom says where Taken came from: google for the time Google
	// Photos kept, name for a date in the file's name.
	TakenFrom string `json:"takenFrom,omitempty"`
	// Outcome is what checking it against the library came to: missing,
	// alternative, uncertain, represented, removed, or pending before it
	// has been checked.
	Outcome string `json:"outcome"`
	// Reason says why, in a sentence.
	Reason string `json:"reason"`
	// Match is the library file it was matched with, when there is one.
	Match *GooglePhotosMatch `json:"match,omitempty"`
	// State is waiting, added or skipped.
	State string `json:"state"`
	// AddedAs is where it went in the archive, when it was added.
	AddedAs string `json:"addedAs,omitempty"`
	// AddedAt is when it was added, in RFC 3339 and UTC.
	AddedAt string `json:"addedAt,omitempty"`
	// Favourite is true for a photo starred in Google Photos.
	Favourite bool `json:"favourite"`
	// Description is the caption typed in Google Photos.
	Description string `json:"description,omitempty"`
	// People are the faces named in it in Google Photos.
	People []string `json:"people"`
	// Archive is the export it is read from.
	Archive string `json:"archive"`
	// Copies counts the places in the inbox that hold it, such as the same
	// photo in two albums or two exports.
	Copies int `json:"copies"`
}

// GooglePhotosMatch is the library file a photo was matched with.
type GooglePhotosMatch struct {
	// AssetID is its id in the catalogue.
	AssetID int64 `json:"assetId"`
	// Path is where it is in the archive.
	Path string `json:"path"`
	// Size is its size in bytes.
	Size int64 `json:"size"`
}

// GooglePhotosSelection names photos for one action.
type GooglePhotosSelection struct {
	// IDs are the photos' ids from GET /api/google-photos, at most 20,000.
	IDs []int64 `json:"ids"`
}

// GooglePhotosSkip sets photos aside, or takes them back.
type GooglePhotosSkip struct {
	// IDs are the photos' ids from GET /api/google-photos, at most 20,000.
	IDs []int64 `json:"ids"`
	// Skip is true to set them aside, false to have them waiting again.
	Skip bool `json:"skip"`
}

// googlePhotosTabs maps each tab to the photos it lists.
var googlePhotosTabs = map[string]string{
	"missing":     "i.state='waiting' AND i.outcome='missing'",
	"alternative": "i.state='waiting' AND i.outcome='alternative'",
	"uncertain":   "i.state='waiting' AND i.outcome IN ('uncertain','pending')",
	"represented": "i.state='waiting' AND i.outcome='represented'",
	"removed":     "i.state='waiting' AND i.outcome='removed'",
	"added":       "i.state='added'",
	"skipped":     "i.state='skipped'",
}

// inInbox holds for a photo an export in the inbox still holds, and for one
// already added, which the Added tab lists whether its export is kept or not.
const inInbox = `(i.state='added' OR EXISTS(SELECT 1 FROM takeout_entries e JOIN takeout_archives a ON a.id=e.archive_id WHERE e.item_id=i.id AND a.present=1))`

const takeoutQueued = `EXISTS(SELECT 1 FROM task_items ti WHERE ti.trash_key='takeout:'||i.id AND ti.state='queued')`

// GooglePhotosPage lists one tab of the Google Photos page.
func (g *GooglePhotos) Page(ctx context.Context, tab string, from, limit int) (GooglePhotosPage, error) {
	if tab == "" {
		tab = "missing"
	}
	where, ok := googlePhotosTabs[tab]
	if !ok || from < 0 {
		return GooglePhotosPage{}, ErrInvalid
	}
	s := g.s
	page := GooglePhotosPage{Inbox: g.Configured(), Tab: tab, Archives: []TakeoutArchive{}, Items: []GooglePhotosItem{}}
	page.Scanning, page.ScannedAt, page.Problem = g.status()
	rows, err := s.read.QueryContext(ctx, "SELECT name,kind,size_bytes,media,scanned_at,error FROM takeout_archives WHERE present=1 ORDER BY name")
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var a TakeoutArchive
		if err := rows.Scan(&a.Name, &a.Kind, &a.Size, &a.Media, &a.ScannedAt, &a.Problem); err != nil {
			rows.Close()
			return page, err
		}
		page.Archives = append(page.Archives, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return page, err
	}
	counts, err := s.read.QueryContext(ctx, "SELECT i.state,i.outcome,count(*) FROM takeout_items i WHERE "+inInbox+" AND NOT "+takeoutQueued+" GROUP BY i.state,i.outcome")
	if err != nil {
		return page, err
	}
	for counts.Next() {
		var state, outcome string
		var n int
		if err := counts.Scan(&state, &outcome, &n); err != nil {
			counts.Close()
			return page, err
		}
		c := &page.Counts
		switch {
		case state == "added":
			c.Added += n
		case state == "skipped":
			c.Skipped += n
		case outcome == TakeoutMissing:
			c.Missing += n
		case outcome == TakeoutAlternative:
			c.Alternative += n
		case outcome == TakeoutUncertain:
			c.Uncertain += n
		case outcome == TakeoutRepresented:
			c.Represented += n
		case outcome == TakeoutRemoved:
			c.Removed += n
		default:
			c.Checking += n
			c.Uncertain += n
		}
	}
	counts.Close()
	if err := counts.Err(); err != nil {
		return page, err
	}
	// A photo a task is adding has left the tab it was in, as a screenshot
	// being moved has; the Added tab shows it once it is in.
	order := "COALESCE(i.local_at,9e18),i.name,i.id"
	if tab == "added" {
		order = "i.added_at DESC,i.id DESC"
	}
	items, err := s.read.QueryContext(ctx, `SELECT i.id,i.name,i.kind,i.size_bytes,i.local_at,i.taken_from,i.outcome,i.reason,i.state,i.added_as,COALESCE(i.added_at,''),i.facts,
		COALESCE(m.id,0),COALESCE(m.relative_path,''),COALESCE(m.size_bytes,0),
		COALESCE((SELECT a.name FROM takeout_entries e JOIN takeout_archives a ON a.id=e.archive_id WHERE e.item_id=i.id AND a.present=1 ORDER BY a.kind='folder' DESC,a.id LIMIT 1),''),
		(SELECT count(*) FROM takeout_entries e JOIN takeout_archives a ON a.id=e.archive_id WHERE e.item_id=i.id AND a.present=1)
		FROM takeout_items i LEFT JOIN assets m ON m.id=i.match_asset_id
		WHERE `+where+` AND `+inInbox+` AND NOT `+takeoutQueued+` ORDER BY `+order+` LIMIT ? OFFSET ?`, limit+1, from)
	if err != nil {
		return page, err
	}
	defer items.Close()
	for items.Next() {
		var item GooglePhotosItem
		var local sql.NullInt64
		var facts string
		var match GooglePhotosMatch
		if err := items.Scan(&item.ID, &item.Name, &item.Kind, &item.Size, &local, &item.TakenFrom, &item.Outcome, &item.Reason, &item.State, &item.AddedAs, &item.AddedAt, &facts,
			&match.AssetID, &match.Path, &match.Size, &item.Archive, &item.Copies); err != nil {
			return page, err
		}
		if local.Valid {
			item.Taken = time.Unix(local.Int64, 0).UTC().Format("2006-01-02T15:04:05")
		}
		if match.AssetID != 0 {
			match.Path = displayPath(match.Path)
			item.Match = &match
		}
		item.AddedAs = displayPath(item.AddedAs)
		var f takeoutFacts
		if json.Unmarshal([]byte(facts), &f) == nil {
			item.Favourite, item.Description, item.People = f.Favourite, f.Description, f.People
		}
		if item.People == nil {
			item.People = []string{}
		}
		page.Items = append(page.Items, item)
	}
	if err := items.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Next = from + limit
	}
	return page, nil
}

func validSelection(ids []int64) bool {
	if len(ids) == 0 || len(ids) > taskLimit {
		return false
	}
	for _, id := range ids {
		if id < 1 {
			return false
		}
	}
	return true
}

// SkipGooglePhotos sets photos aside so they stop being offered, or has
// them waiting again. A photo already added is left as it is.
func (s *Store) SkipGooglePhotos(ctx context.Context, request GooglePhotosSkip) (int, error) {
	if !validSelection(request.IDs) {
		return 0, ErrInvalid
	}
	from, to := "waiting", "skipped"
	if !request.Skip {
		from, to = "skipped", "waiting"
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	update, err := tx.PrepareContext(ctx, "UPDATE takeout_items SET state=? WHERE id=? AND state=? AND NOT EXISTS(SELECT 1 FROM task_items ti WHERE ti.trash_key='takeout:'||takeout_items.id AND ti.state='queued')")
	if err != nil {
		return 0, err
	}
	defer update.Close()
	changed := 0
	for _, id := range request.IDs {
		result, err := update.ExecContext(ctx, to, id, from)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		changed += int(n)
	}
	return changed, tx.Commit()
}

// QueueGooglePhotos queues adding photos to the library, one file at a time.
// Only photos the library has no copy of, or a different copy of, can be
// added.
func (s *Store) QueueGooglePhotos(ctx context.Context, request GooglePhotosSelection) (Task, error) {
	if !validSelection(request.IDs) {
		return Task{}, ErrInvalid
	}
	lookup, err := s.read.PrepareContext(ctx, "SELECT i.name,i.size_bytes,i.outcome,i.state FROM takeout_items i WHERE i.id=? AND "+inInbox)
	if err != nil {
		return Task{}, err
	}
	defer lookup.Close()
	seen := map[int64]bool{}
	items := make([]taskItem, 0, len(request.IDs))
	for _, id := range request.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		var name, outcome, state string
		var size int64
		if err := lookup.QueryRowContext(ctx, id).Scan(&name, &size, &outcome, &state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Task{}, fmt.Errorf("%w: a photo is no longer in the inbox; read the page again", ErrConflict)
			}
			return Task{}, err
		}
		if state != "waiting" || (outcome != TakeoutMissing && outcome != TakeoutAlternative) {
			return Task{}, fmt.Errorf("%w: %s can no longer be added; read the page again", ErrConflict, name)
		}
		items = append(items, taskItem{chunk: len(items), key: takeoutTaskKey(id), name: name, size: size})
	}
	return s.createTask(ctx, TaskGooglePhotosAdd, retryLabel(TaskGooglePhotosAdd, len(items)), "", items)
}

func takeoutTaskKey(id int64) string { return fmt.Sprintf("takeout:%d", id) }

func takeoutTaskID(key string) (int64, bool) {
	var id int64
	if !strings.HasPrefix(key, "takeout:") {
		return 0, false
	}
	_, err := fmt.Sscanf(key, "takeout:%d", &id)
	return id, err == nil && id > 0
}
