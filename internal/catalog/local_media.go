package catalog

import (
	"bytes"
	"context"
	"io"
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

// videoContainer lists every container a frame can be decoded from, which is a
// wider set than the browser will play. It decides what gets a frame rather than
// the catalogued kind, because kind is inherited from the earlier tool and 86
// .MPG files arrived from it recorded as images.
var videoContainer = map[string]bool{".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".avi": true, ".mkv": true, ".mpg": true, ".mpeg": true, ".3gp": true, ".m2ts": true, ".wmv": true, ".flv": true}

// viewableImage lists what a browser renders without a transcode step.
var viewableImage = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".avif": true}

// MediaRoots maps each logical path prefix the catalogue uses onto a read-only
// mount that holds those files. A prefix left empty is simply not served, so a
// tree that is not mounted yields an honest "preview unavailable" rather than a
// wrong file from another tree.
type MediaRoots struct {
	Archive     string // "/archive/..."   the family archive
	Screenshots string // "/screenshots/..." the flat screenshot holding area
	Upgrades    string // "/upgrades/..."  read-only Takeout upgrade staging
	Disks       string // "/disks/<disk>/..." physical disk roots behind the share
	Posters     string // stills captured during social detection
	Cache       string // writable directory for generated gallery thumbnails
	FFmpeg      string // frame extractor for videos with no captured poster
	RawTool     string // reader for the JPEG a camera embeds in a RAW file
}

// root returns the mount holding a catalogued path, and the path relative to it.
// A prefix with no configured mount reports false, which the handler answers as
// a plain 404.
func (m MediaRoots) root(relative string) (string, string, bool) {
	// A shadowed pair is one relative path present on two disks, and the share
	// exposes whichever copy sits on the cache. So the cache half of a pair is
	// reachable through the archive mount at that same relative path even when
	// no disk mount exists, and serving it there is not a guess: it is the file
	// the share resolves that path to. The disk half has no such route, which is
	// the whole reason it is invisible and the reason the page exists.
	if m.Disks == "" && strings.HasPrefix(relative, "/disks/cache/") && m.Archive != "" {
		return m.Archive, strings.TrimPrefix(relative, "/disks/cache/"), true
	}
	for _, entry := range []struct{ prefix, root string }{
		{"/archive/", m.Archive},
		{"/screenshots/", m.Screenshots},
		{"/upgrades/", m.Upgrades},
		{"/disks/", m.Disks},
	} {
		if strings.HasPrefix(relative, entry.prefix) {
			if entry.root == "" {
				return "", "", false
			}
			return entry.root, strings.TrimPrefix(relative, entry.prefix), true
		}
	}
	return "", "", false
}

// LocalMediaHandler serves catalogued files from read-only mounts, so previews
// and video playback work without a separate media service.
//
// It is read-only by construction. The request carries nothing but a catalogue
// ID, the path comes from the database, files are only ever opened for reading,
// and os.Root refuses any path that would leave its mount even through a
// symlink. Nothing here creates, truncates or renames a file.
//
// Originals are streamed with http.ServeContent, which answers Range requests,
// and that is what lets a browser seek within a video rather than having to pull
// the whole file before it starts.
func (s *Store) LocalMediaHandler(roots MediaRoots) http.Handler {
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
		mountRoot, inMount, known := roots.root(relative)
		if !known {
			http.NotFound(w, r)
			return
		}
		extension := strings.ToLower(filepath.Ext(relative))

		// A still is the only thing an <img> can show, so a video's preview is a
		// frame rather than the container. The poster captured during social
		// detection is preferred when there is one, because it was chosen; the
		// rest of the archive gets a frame decoded on first view.
		videoPreview := mode == "preview" && (kind == "video" || videoContainer[extension])
		if videoPreview && s.servedPoster(w, r, id, roots.Posters) {
			return
		}
		// A RAW file is not decodable by a browser either, but it carries the
		// camera's own JPEG preview, so it gets a tile from that. HEIC has no
		// embedded preview and is decoded instead.
		rawPreviewWanted := mode == "preview" && rawImage[extension] && roots.RawTool != ""
		stillPreviewWanted := mode == "preview" && browserBlindStill[extension] && roots.FFmpeg != ""
		if mode == "preview" && !viewableImage[extension] && !videoPreview && !rawPreviewWanted && !stillPreviewWanted {
			// A format with no reader configured needs a transcode this process
			// deliberately does not do.
			s.servePoster(w, r, id, roots.Posters)
			return
		}
		if mode == "original" && !playableVideo[extension] && !viewableImage[extension] {
			http.Error(w, "this format cannot be shown without a transcode", 415)
			return
		}

		root, err := os.OpenRoot(mountRoot)
		if err != nil {
			http.Error(w, "media mount unavailable", 503)
			return
		}
		defer root.Close()
		file, err := root.Open(inMount)
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

		if rawPreviewWanted || stillPreviewWanted {
			var tile []byte
			var rawErr error
			if rawPreviewWanted {
				tile, rawErr = rawPreview(r.Context(), roots.RawTool, file, roots.Cache, id, info.Size(), info.ModTime().Unix())
				if rawErr != nil && roots.FFmpeg != "" {
					// Whatever the file really is, the frame extractor reads far
					// more formats than the name suggested.
					tile, rawErr = stillFrame(r.Context(), roots.FFmpeg, file, roots.Cache, id, info.Size(), info.ModTime().Unix())
				}
			} else {
				tile, rawErr = stillFrame(r.Context(), roots.FFmpeg, file, roots.Cache, id, info.Size(), info.ModTime().Unix())
			}
			if rawErr != nil {
				// No embedded preview is an honest miss: the page keeps its own
				// fallback rather than an <img> being handed sensor data.
				s.servePoster(w, r, id, roots.Posters)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "private, max-age=3600")
			http.ServeContent(w, r, "raw.jpg", thumbnailModTime(info), bytes.NewReader(tile))
			return
		}

		if videoPreview {
			frame, frameErr := videoFrame(r.Context(), roots.FFmpeg, file, roots.Cache, id, info.Size(), info.ModTime().Unix())
			if frameErr != nil {
				// No poster and no frame is an honest miss: the page keeps its
				// own fallback rather than an <img> being handed a container it
				// cannot decode.
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "private, max-age=3600")
			http.ServeContent(w, r, "frame.jpg", thumbnailModTime(info), bytes.NewReader(frame))
			return
		}

		// A gallery tile is drawn a couple of hundred pixels wide, so sending the
		// original costs megabytes per tile for no visible gain. The page asks for
		// the size it is about to draw, and a grid gets a downscaled JPEG.
		if mode == "preview" && r.URL.Query().Get("size") != "large" && viewableImage[extension] {
			if tile, tileErr := thumbnail(file, roots.Cache, id, info.Size(), info.ModTime().Unix()); tileErr == nil {
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("Cache-Control", "private, max-age=3600")
				http.ServeContent(w, r, "tile.jpg", thumbnailModTime(info), bytes.NewReader(tile))
				return
			}
			// A format the decoder cannot read falls through to the original
			// rather than failing, so the tile is heavy but never blank.
			if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, filepath.Base(relative), info.ModTime(), file)
	})
}

// servePoster answers with the still captured during social detection when one
// exists. A miss is a plain 404 so the page shows its own fallback rather than
// an error the reviewer cannot act on.
func (s *Store) servePoster(w http.ResponseWriter, r *http.Request, id int64, posterRoot string) {
	if !s.servedPoster(w, r, id, posterRoot) {
		http.NotFound(w, r)
	}
}

// servedPoster writes the captured poster and reports whether it did, so a
// caller with another source to try is not left having already sent a 404.
func (s *Store) servedPoster(w http.ResponseWriter, r *http.Request, id int64, posterRoot string) bool {
	if posterRoot == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	poster, err := s.SocialPoster(ctx, id)
	cancel()
	if err != nil || poster == "" {
		return false
	}
	file, err := os.Open(filepath.Join(posterRoot, poster))
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, poster, info.ModTime(), file)
	return true
}
