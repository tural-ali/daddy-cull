package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// binMediaKind guesses what a stored Bin file is from its name. The imported
// history records only "media" or "sidecar", which is not enough to choose
// between a frame and a tile, and the file itself is the better witness anyway.
func binMediaKind(storedPath string) string {
	if videoContainer[strings.ToLower(filepath.Ext(storedPath))] {
		return "video"
	}
	return "image"
}

// binPathInShare rewrites the disk-qualified path the history recorded into the
// path the merged share uses for it.
//
// Only files still held count as rivals: a restored row names a path its file has
// already left, and a purged one names a path nothing is at.
//
// The rewrite is refused when another held row has the same path on a different
// disk, because the share can expose only one of the two and there is no way from
// here to tell which. Refusing leaves the card honestly blank; guessing would put
// one photograph's picture on another photograph's delete button.
func (s *Store) binPathInShare(ctx context.Context, stored string) (string, error) {
	disk, tail, ok := splitDiskPath(stored)
	if !ok {
		return stored, nil
	}
	rows, err := s.read.QueryContext(ctx, "SELECT culled_path FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL AND culled_path LIKE ?", "%/"+tail)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var other string
		if err = rows.Scan(&other); err != nil {
			return "", err
		}
		if otherDisk, otherTail, split := splitDiskPath(other); split && otherTail == tail && otherDisk != disk {
			return "", ErrInvalid
		}
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return "/archive/" + tail, nil
}

// splitDiskPath separates "/disks/<disk>/<tail>" into its disk and the path
// below the share root.
func splitDiskPath(path string) (disk, tail string, ok bool) {
	rest, found := strings.CutPrefix(path, "/disks/")
	if !found {
		return "", "", false
	}
	slash := strings.Index(rest, "/")
	if slash < 1 || slash+1 >= len(rest) {
		return "", "", false
	}
	return rest[:slash], rest[slash+1:], true
}

// onRecordedDisk reports whether a disk-qualified Bin path still names a
// regular file under the mounted disk roots. It only looks; os.Root keeps the
// look inside the mount.
func onRecordedDisk(disksRoot, stored string) bool {
	if disksRoot == "" {
		return false
	}
	disk, tail, ok := splitDiskPath(stored)
	if !ok {
		return false
	}
	root, err := os.OpenRoot(disksRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Stat(disk + "/" + tail)
	return err == nil && info.Mode().IsRegular()
}

// LegacyBinMediaHandler previews a file the earlier PHP tool moved to the Bin.
//
// A culled file is not gone: it sits in a .culled folder inside the archive
// share, at the path the history recorded. So it can be shown through exactly
// the same pipeline as anything else, which is the point: deciding whether to
// restore or permanently delete a file is a judgement about a photograph, and
// a filename is not enough to make it on.
//
// The request carries only a Bin row id. The path comes from the database and
// the file is opened read-only through os.Root, so this route cannot reach
// outside a media mount and cannot write.
func (s *Store) LegacyBinMediaHandler(roots MediaRoots) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "read only", 405)
			return
		}
		mode := r.PathValue("mode")
		if mode != "preview" && mode != "original" {
			http.NotFound(w, r)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		var stored string
		err = s.read.QueryRowContext(ctx, "SELECT culled_path FROM legacy_culled WHERE legacy_id=? AND restored_at IS NULL AND purged_at IS NULL", id).Scan(&stored)
		// With the physical disks mounted, the recorded path names the exact
		// file, as long as it is still there. Unraid's mover migrates the cache
		// onto the array, so a file recorded on the cache may since have moved
		// to a disk, and then the share is the way to it.
		source := stored
		if err == nil && !onRecordedDisk(roots.Disks, stored) {
			source, err = s.binPathInShare(ctx, stored)
		}
		cancel()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// id is zero because a Bin row is not a catalogue asset: it has no entry
		// in the hardlink farm and no captured social still to stand in for a
		// frame. The cache subject is namespaced for the same reason, since row
		// 12 and asset 12 are different files.
		s.serveMedia(w, r, roots, mode, mediaFile{
			relative: source,
			kind:     binMediaKind(stored),
			subject:  "bin/" + strconv.FormatInt(id, 10),
		})
	})
}

// BinnedMediaHandler serves a file this app moved into the Bin, from where it
// now sits, so a card in the Bin shows the photograph rather than its name.
//
// The writer keeps a moved file at a path derived from its batch and position,
// inside the same share the preview process already reads. The request names
// only the batch and the position; the path is rebuilt from the recorded batch,
// and a file that has left the Bin, or is still on its way in, is not served.
func (s *Store) BinnedMediaHandler(roots MediaRoots) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "read only", 405)
			return
		}
		mode := r.PathValue("mode")
		index, err := strconv.Atoi(r.PathValue("index"))
		plan := r.PathValue("plan")
		if (mode != "preview" && mode != "original") || err != nil || index < 0 || len(plan) != 32 || strings.Trim(plan, "0123456789abcdef") != "" {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		relative, name, found := s.binnedFile(ctx, r.PathValue("source"), plan, index)
		cancel()
		if !found {
			http.NotFound(w, r)
			return
		}
		s.serveMedia(w, r, roots, mode, mediaFile{
			relative: relative,
			kind:     mediaKind(name),
			subject:  "binned/" + plan + "/" + strconv.Itoa(index),
		})
	})
}

// binnedFile rebuilds where one file of a Bin batch is stored, if it is there.
func (s *Store) binnedFile(ctx context.Context, source, id string, index int) (relative, name string, found bool) {
	switch source {
	case "bin":
		var body string
		if s.read.QueryRowContext(ctx, "SELECT body FROM file_plans WHERE id=?", id).Scan(&body) != nil {
			return "", "", false
		}
		var plan BinPlan
		if json.Unmarshal([]byte(body), &plan) != nil || plan.ID != id || index >= len(plan.Files) || plan.Files[index].Phase != "bin" || !safeRelative(plan.Files[index].Original) {
			return "", "", false
		}
		return "/archive/" + stored(&plan, index), plan.Files[index].Original, true
	case "shot":
		var body string
		if s.read.QueryRowContext(ctx, "SELECT body FROM screenshot_plans WHERE id=?", id).Scan(&body) != nil {
			return "", "", false
		}
		var plan ScreenshotPlan
		if json.Unmarshal([]byte(body), &plan) != nil || plan.ID != id || plan.Action != "remove" || plan.State != "bin" || index >= len(plan.Files) {
			return "", "", false
		}
		file := plan.Files[index]
		if file.Destination != path.Join(".culled/next", plan.ID, fmt.Sprintf("%04d-%s", index, file.Source)) {
			return "", "", false
		}
		return "/screenshots/" + file.Destination, file.Source, true
	}
	return "", "", false
}
