package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
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
// The pairing is by name, so the Bin can check it again on disk whatever the
// index says: the photo's folder, or the same folder under .live-photos; the
// photo's name before its extension, then _HEVC; and .MOV or .MP4. In
// .live-photos the _HEVC may be left out, since nothing but clips is filed
// there. Beside the photo it may not: a plain IMG_1234.MOV next to
// IMG_1234.HEIC is as often a video of its own. A clip whose name matches the
// HEIC and the JPEG of one exposure is the Live Photo of both, and leaves only
// with the last of them.
//
// A name is not proof. An iPhone's counter comes round again, two phones take
// IMG_3043 on one day, and a Google Takeout files the clip of one under the
// name of the other. Apple writes one identifier into both halves of a Live
// Photo, so where the clip and the photo both record one, they pair only if
// the two are the same. A clip of another exposure is left where it is, and
// never goes to the Bin, or is deleted, with a photo it is not part of. Where
// either records none, the name stands.

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

// LiveIDReader reads Apple's Live Photo identifier from files, by absolute
// path, in upper case: "" for a file that records none. A file it could not
// read is left out of the answer.
type LiveIDReader func(ctx context.Context, files []string) (map[string]string, error)

// ReadLiveIDsWith has Live Photo pairings confirmed by the identifier tool,
// exiftool, reads; with "" they are by name alone.
func (s *Store) ReadLiveIDsWith(tool string) {
	s.liveIDReader = nil
	if tool != "" {
		s.liveIDReader = exiftoolLiveIDs(tool)
	}
}

// exiftoolLiveIDs reads ContentIdentifier, which an iPhone writes into the
// still and the video of a Live Photo alike. exiftool answers every file it
// could open, with or without the tag, so a file missing from its answer is
// one it could not read.
func exiftoolLiveIDs(tool string) LiveIDReader {
	return func(ctx context.Context, files []string) (map[string]string, error) {
		cmd := exec.CommandContext(ctx, tool, append([]string{"-json", "-q", "-q", "-ContentIdentifier", "--"}, files...)...)
		var out bytes.Buffer
		cmd.Stdout = &out
		// exiftool exits 1 when a file cannot be read, and still answers the
		// others.
		if err := cmd.Run(); err != nil && out.Len() == 0 {
			if errors.Is(err, exec.ErrNotFound) {
				return nil, fmt.Errorf("%s is not installed, so Live Photos cannot be checked", tool)
			}
			return nil, fmt.Errorf("%s could not read Live Photo identifiers: %w", tool, err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
			return nil, fmt.Errorf("%s answered with something other than JSON", tool)
		}
		found := make(map[string]string, len(rows))
		for _, row := range rows {
			source, _ := row["SourceFile"].(string)
			id, _ := row["ContentIdentifier"].(string)
			if !liveIDPattern.MatchString(id) {
				id = ""
			}
			found[source] = strings.ToUpper(id)
		}
		return found, nil
	}
}

// liveIDs gives the Live Photo identifier of each of files, relative to
// root, "" for one that records none, and leaves out a file that is gone. It
// reads only what live_ids does not hold as the file is now, and remembers
// what it reads; with read false it reads nothing, and leaves out what it
// does not hold. Without a reader it gives nil, and pairs stand by name. A
// file the reader cannot read is an error: a pair is not trusted unchecked.
func (s *Store) liveIDs(ctx context.Context, root string, files []string, read bool) (map[string]string, error) {
	if s.liveIDReader == nil || len(files) == 0 {
		return nil, nil
	}
	type stamp struct{ size, mtime int64 }
	ids := make(map[string]string, len(files))
	now := map[string]stamp{}
	var unread []string
	held, err := s.read.PrepareContext(ctx, "SELECT size_bytes,mtime,content_id FROM live_ids WHERE path=?")
	if err != nil {
		return nil, err
	}
	defer held.Close()
	for _, rel := range files {
		if _, seen := now[rel]; seen {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		at := stamp{info.Size(), info.ModTime().UnixNano()}
		now[rel] = at
		var was stamp
		var id string
		err = held.QueryRowContext(ctx, rel).Scan(&was.size, &was.mtime, &id)
		if err == nil && was == at {
			ids[rel] = id
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if read {
			unread = append(unread, rel)
		}
	}
	for start := 0; start < len(unread); start += 200 {
		chunk := unread[start:min(start+200, len(unread))]
		abs := make([]string, len(chunk))
		for i, rel := range chunk {
			abs[i] = filepath.Join(root, filepath.FromSlash(rel))
		}
		read, err := s.liveIDReader(ctx, abs)
		if err != nil {
			return nil, err
		}
		tx, err := s.write.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		for i, rel := range chunk {
			id, ok := read[abs[i]]
			if !ok {
				tx.Rollback()
				return nil, fmt.Errorf("could not read the Live Photo identifier of %s", rel)
			}
			ids[rel] = id
			if _, err = tx.ExecContext(ctx, "INSERT INTO live_ids(path,size_bytes,mtime,content_id) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET size_bytes=excluded.size_bytes,mtime=excluded.mtime,content_id=excluded.content_id",
				rel, now[rel].size, now[rel].mtime, id); err != nil {
				tx.Rollback()
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// sameLive reports whether a clip and a photo that pair by name are one Live
// Photo, given their identifiers: unless both record one and the two differ.
func sameLive(ids map[string]string, clip, photo string) bool {
	c, p := ids[clip], ids[photo]
	return c == "" || p == "" || c == p
}

// IndexLiveClips finds the clip of every catalogued Live Photo under root, the
// archive, in .live-photos and beside the photos, and rewrites live_clips when
// what it finds differs. It reads folder listings, and the Live Photo
// identifiers of the clips and photos that pair by name, which live_ids keeps
// from one scan to the next. A clip whose photo is not catalogued, or is in
// the Bin, or whose identifier is another photo's, is left out. changed
// reports a rewrite, so the caller can rebuild the indexes that leave the
// clips out.
func (s *Store) IndexLiveClips(ctx context.Context, root string) (changed bool, err error) {
	return s.indexLiveClips(ctx, root, true)
}

// IndexKnownLiveClips is IndexLiveClips without reading an identifier that
// live_ids does not hold, so it is quick enough to wait for at start: a pair
// it cannot confirm stands by name, as it did before, until IndexLiveClips
// reads it. The Bin reads what it needs for itself.
func (s *Store) IndexKnownLiveClips(ctx context.Context, root string) (changed bool, err error) {
	return s.indexLiveClips(ctx, root, false)
}

func (s *Store) indexLiveClips(ctx context.Context, root string, read bool) (changed bool, err error) {
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
	paths := map[int64]string{}
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
			paths[id] = rel
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
	// A name is confirmed by the identifiers, where both halves record one.
	var named []string
	for _, row := range found {
		named = append(named, row.clip, paths[row.photo])
	}
	ids, err := s.liveIDs(ctx, resolved, named, read)
	if err != nil {
		return false, err
	}
	if ids != nil {
		found = slices.DeleteFunc(found, func(row liveClipRow) bool { return !sameLive(ids, row.clip, paths[row.photo]) })
		if read {
			if err = s.forgetLiveIDs(ctx, named); err != nil {
				return false, err
			}
		}
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

// forgetLiveIDs drops from live_ids every file but those given, the ones a
// scan found pairing by name, so it holds no file long gone.
func (s *Store) forgetLiveIDs(ctx context.Context, keep []string) error {
	held, err := s.read.QueryContext(ctx, "SELECT path FROM live_ids")
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(keep))
	for _, p := range keep {
		wanted[p] = true
	}
	var stale []string
	for held.Next() {
		var p string
		if err = held.Scan(&p); err != nil {
			held.Close()
			return err
		}
		if !wanted[p] {
			stale = append(stale, p)
		}
	}
	if err = held.Close(); err != nil {
		return err
	}
	for _, p := range stale {
		if _, err = s.write.ExecContext(ctx, "DELETE FROM live_ids WHERE path=?", p); err != nil {
			return err
		}
	}
	return nil
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
