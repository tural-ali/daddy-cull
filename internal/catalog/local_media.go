package catalog

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// playableVideo lists the container extensions a browser will play directly.
// Anything else is refused rather than streamed as a download the page cannot
// use, so the card keeps its honest "preview unavailable" state.
var playableVideo = map[string]bool{".mp4": true, ".m4v": true, ".mov": true, ".webm": true}

// viewableImage lists what a browser renders without a transcode step.
var viewableImage = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".avif": true}

// LocalMediaHandler serves catalogued archive files from a read-only mount, so
// previews and video playback work without a separate media service.
//
// It is read-only by construction. The request carries nothing but a catalogue
// ID, the path comes from the database, files are only ever opened for reading,
// and os.Root refuses any path that would leave the archive root even through a
// symlink. Nothing here creates, truncates or renames a file.
//
// Originals are streamed with http.ServeContent, which answers Range requests,
// and that is what lets a browser seek within a video rather than having to pull
// the whole file before it starts.
func (s *Store) LocalMediaHandler(archiveRoot, posterRoot string) http.Handler {
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
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		var relative, kind string
		err = s.read.QueryRowContext(ctx, "SELECT relative_path,kind FROM assets WHERE id=?", id).Scan(&relative, &kind)
		cancel()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(relative, "/upgrades/") {
			serveTakeout(w, r, relative)
			return
		}
		if !strings.HasPrefix(relative, "/archive/") {
			http.NotFound(w, r)
			return
		}
		extension := strings.ToLower(filepath.Ext(relative))

		// A still is the only thing an <img> can show, so a video's preview is
		// its captured poster frame. Falling through to the container instead
		// would hand the tag bytes it cannot decode.
		if mode == "preview" && kind == "video" {
			s.servePoster(w, r, id, posterRoot)
			return
		}
		if mode == "preview" && !viewableImage[extension] {
			// RAW and HEIC need a transcode this process deliberately does not do.
			s.servePoster(w, r, id, posterRoot)
			return
		}
		if mode == "original" && !playableVideo[extension] && !viewableImage[extension] {
			http.Error(w, "this format cannot be shown without a transcode", 415)
			return
		}

		root, err := os.OpenRoot(archiveRoot)
		if err != nil {
			http.Error(w, "archive unavailable", 503)
			return
		}
		defer root.Close()
		file, err := root.Open(strings.TrimPrefix(relative, "/archive/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, filepath.Base(relative), info.ModTime(), file)
	})
}

// servePoster answers with the still captured during social detection when one
// exists. A miss is a plain 404 so the page shows its own fallback rather than
// an error the reviewer cannot act on.
func (s *Store) servePoster(w http.ResponseWriter, r *http.Request, id int64, posterRoot string) {
	if posterRoot == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	poster, err := s.SocialPoster(ctx, id)
	cancel()
	if err != nil || poster == "" {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(posterRoot, poster))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, poster, info.ModTime(), file)
}
