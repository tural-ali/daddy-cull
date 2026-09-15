package catalog

import (
	"context"
	"net/http"
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
		var inShare string
		if err == nil {
			inShare, err = s.binPathInShare(ctx, stored)
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
			relative: inShare,
			kind:     binMediaKind(stored),
			subject:  "bin/" + strconv.FormatInt(id, 10),
		})
	})
}
