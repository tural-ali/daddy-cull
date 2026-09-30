package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// An iPhone's Live Photo is two files: the still, and a clip of the moments
// around it, which the phone plays when the photo is pressed. The archive
// keeps the clip out of the day folder, in .live-photos/ under the same
// YYYY/YYYY-MM/YYYY-MM-DD path and name, so that everything reading the day
// folders sees the photo once. A clip that graduated beside its photo,
// IMG_1234_HEVC.MOV next to IMG_1234.HEIC, is the same thing filed in the
// wrong place. Either way Cull shows the photo with a Live badge that plays
// the clip, and never offers the clip as a file to decide on: it goes to the
// Bin with its photo, comes back with it, and is deleted with it.
//
// The pairing is by name alone, so the Bin can check it again on disk
// whatever the index says: the photo's folder, or the same folder under
// .live-photos; the photo's name before its extension, then _HEVC; and .MOV
// or .MP4. In .live-photos the _HEVC may be left out, since nothing but clips
// is filed there. Beside the photo it may not: a plain IMG_1234.MOV next to
// IMG_1234.HEIC is as often a video of its own. A clip whose name matches the
// HEIC and the JPEG of one exposure is the Live Photo of both, and leaves only
// with the last of them.

// liveFolder is where the archive files Live Photo clips, under the archive
// root.
const liveFolder = ".live-photos"

var liveClipExt = map[string]bool{".mov": true, ".mp4": true}

// ErrLiveClip refuses a choice on the video of a Live Photo, which is only
// ever decided with its photo.
var ErrLiveClip = fmt.Errorf("%w: the video of a Live Photo", ErrConflict)

// livePhotoOf is the folder and the name before the extension, in lower case,
// that a clip's photo has. Both paths are relative to the archive root. ok is
// false for a file that is not a Live Photo's clip.
func livePhotoOf(clip string) (dir, stem string, ok bool) {
	if !liveClipExt[strings.ToLower(path.Ext(clip))] {
		return "", "", false
	}
	dir = path.Dir(clip)
	stem = strings.ToLower(strings.TrimSuffix(path.Base(clip), path.Ext(clip)))
	stem, hevc := strings.CutSuffix(stem, "_hevc")
	if inside, filed := strings.CutPrefix(dir, liveFolder+"/"); filed {
		dir = inside
	} else if !hevc {
		return "", "", false
	}
	if stem == "" || dir == "." || strings.HasPrefix(dir, liveFolder) {
		return "", "", false
	}
	return dir, stem, true
}

// photoKey is the folder and the name before the extension, in lower case, of
// a photo relative to the archive root, as livePhotoOf gives them for its
// clips.
func photoKey(photo string) string {
	return path.Dir(photo) + "/" + strings.ToLower(strings.TrimSuffix(path.Base(photo), path.Ext(photo)))
}

// safeLiveClip reports whether p is a path under .live-photos that the Bin
// may move, by the same rules as any other archive path below it.
func safeLiveClip(p string) bool {
	inside, ok := strings.CutPrefix(p, liveFolder+"/")
	return ok && safeRelative(inside)
}

type liveClipRow struct {
	photo  int64
	clip   string
	clipID int64
	size   int64
}

// IndexLiveClips finds the clip of every catalogued Live Photo under root, the
// archive, in .live-photos and beside the photos, and rewrites live_clips when
// what it finds differs. It reads folder listings only. A clip whose photo is
// not catalogued, or is in the Bin, is left out. changed reports a rewrite, so
// the caller can rebuild the indexes that leave the clips out.
func (s *Store) IndexLiveClips(ctx context.Context, root string) (changed bool, err error) {
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return false, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.kind,a.size_bytes FROM assets a
		WHERE a.source_id='archive' AND a.kind IN ('image','raw','video')
		  AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
		  AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')`)
	if err != nil {
		return false, err
	}
	photos := map[string][]int64{}
	var inline []liveClipRow
	for rows.Next() {
		var id, size int64
		var p, kind string
		if err = rows.Scan(&id, &p, &kind, &size); err != nil {
			rows.Close()
			return false, err
		}
		rel, ok := strings.CutPrefix(p, "/archive/")
		if !ok {
			continue
		}
		if kind == "video" {
			inline = append(inline, liveClipRow{clip: rel, clipID: id, size: size})
		} else {
			photos[photoKey(rel)] = append(photos[photoKey(rel)], id)
		}
	}
	if err = rows.Close(); err != nil {
		return false, err
	}
	var found []liveClipRow
	pair := func(clip liveClipRow) {
		if dir, stem, ok := livePhotoOf(clip.clip); ok {
			for _, photo := range photos[dir+"/"+stem] {
				clip.photo = photo
				found = append(found, clip)
			}
		}
	}
	for _, clip := range inline {
		pair(clip)
	}
	filed := filepath.Join(resolved, liveFolder)
	if info, statErr := os.Stat(filed); statErr == nil && info.IsDir() {
		err = walkArchiveDays(filed, func(dayDir, _ string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(resolved, dayDir)
			if err != nil {
				return err
			}
			entries, err := os.ReadDir(dayDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				info, err := entry.Info()
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				pair(liveClipRow{clip: filepath.ToSlash(rel) + "/" + entry.Name(), size: info.Size()})
			}
			return nil
		})
		if err != nil {
			return false, err
		}
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].photo != found[j].photo {
			return found[i].photo < found[j].photo
		}
		return found[i].clip < found[j].clip
	})
	held, err := s.liveClipRows(ctx)
	if err != nil {
		return false, err
	}
	if equalLiveClips(held, found) {
		return false, nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM live_clips"); err != nil {
		return false, err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO live_clips(photo_id,clip,clip_id,size_bytes) VALUES(?,?,?,?)")
	if err != nil {
		return false, err
	}
	defer stmt.Close()
	for _, row := range found {
		clipID := sql.NullInt64{Int64: row.clipID, Valid: row.clipID > 0}
		if _, err = stmt.ExecContext(ctx, row.photo, row.clip, clipID, row.size); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (s *Store) liveClipRows(ctx context.Context) ([]liveClipRow, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT photo_id,clip,COALESCE(clip_id,0),size_bytes FROM live_clips ORDER BY photo_id,clip")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var held []liveClipRow
	for rows.Next() {
		var row liveClipRow
		if err = rows.Scan(&row.photo, &row.clip, &row.clipID, &row.size); err != nil {
			return nil, err
		}
		held = append(held, row)
	}
	return held, rows.Err()
}

func equalLiveClips(a, b []liveClipRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// liveClipAssets is the catalogue ids of the Live Photo clips that were
// catalogued as files of their own, which every list leaves out: they show
// as part of their photo.
const liveClipAssets = "SELECT clip_id FROM live_clips WHERE clip_id IS NOT NULL"

// markLive sets Live on every photo among assets that has a Live Photo clip.
func (s *Store) markLive(ctx context.Context, assets []*Asset) error {
	if len(assets) == 0 {
		return nil
	}
	byID := make(map[int64]*Asset, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
	}
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.read.QueryContext(ctx, "SELECT DISTINCT photo_id FROM live_clips WHERE photo_id IN (?"+strings.Repeat(",?", len(chunk)-1)+")", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			byID[id].Live = true
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// liveClipsOf maps the catalogue id of each Live Photo clip catalogued on its
// own to its photo, among the photos given, so a link to the clip can open
// the photo.
func (s *Store) liveClipsOf(ctx context.Context, assets []*Asset) (map[int64]int64, error) {
	clips := map[int64]int64{}
	for start := 0; start < len(assets); start += 500 {
		chunk := assets[start:min(start+500, len(assets))]
		args := make([]any, len(chunk))
		for i, a := range chunk {
			args[i] = a.ID
		}
		rows, err := s.read.QueryContext(ctx, "SELECT clip_id,photo_id FROM live_clips WHERE clip_id IS NOT NULL AND photo_id IN (?"+strings.Repeat(",?", len(chunk)-1)+") ORDER BY photo_id DESC", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var clip, photo int64
			if err = rows.Scan(&clip, &photo); err != nil {
				rows.Close()
				return nil, err
			}
			clips[clip] = photo
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	return clips, nil
}

// LiveClipHandler serves a Live Photo's clip by its photo's id, the original
// .MOV where there is one, converted where the browser cannot play it.
func (s *Store) LiveClipHandler(roots MediaRoots) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "read only", 405)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		var clip string
		var clipID int64
		err = s.read.QueryRowContext(ctx, `SELECT clip,COALESCE(clip_id,0) FROM live_clips WHERE photo_id=?
			ORDER BY lower(clip) LIKE '%\_hevc.mov' ESCAPE '\' DESC, lower(clip) LIKE '%.mov' DESC, clip LIMIT 1`, id).Scan(&clip, &clipID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.serveMedia(w, r, roots, "original", mediaFile{relative: "/archive/" + clip, kind: "video", id: clipID, subject: "live/" + clip})
	})
}
