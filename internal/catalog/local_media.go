package catalog

import (
	"bytes"
	"context"
	"io"
	"log"
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
	Review      string // flat hardlink farm, one entry per asset id, for trees with no mount
	Posters     string // stills captured during social detection
	Cache       string // writable directory for generated gallery thumbnails
	FFmpeg      string // frame extractor for videos with no captured poster
	RawTool     string // reader for the JPEG a camera embeds in a RAW file
}

// root returns the mount holding a catalogued path, and the path relative to it.
// A prefix with no configured mount reports false, which the handler answers as
// a plain 404.
//
// Review is the last resort for files the share cannot expose under their own
// names: the physical copies behind a shadowed pair live at paths the merged
// share resolves to something else, and some differ from each other only by
// letter case, which a case-insensitive client folds together. So they are
// reached instead through a flat directory of hardlinks named by asset id,
// where a collision cannot occur by construction and the name is exactly what
// the request already carries.
func (m MediaRoots) root(relative string, id int64) (string, string, bool) {
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
				break
			}
			return entry.root, strings.TrimPrefix(relative, entry.prefix), true
		}
	}
	// The farm is named by catalogue id, so a file that has none, such as a row
	// of the imported Bin history, has no entry there and must not be guessed at.
	if m.Review != "" && id > 0 {
		return m.Review, strconv.FormatInt(id, 10) + strings.ToLower(filepath.Ext(relative)), true
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
		s.serveMedia(w, r, roots, mode, mediaFile{relative: relative, kind: kind, id: id, subject: strconv.FormatInt(id, 10), posterID: id})
	})
}

// mediaFile is one file the media pipeline can serve: where the catalogue says
// it lives, what it believes it is, and how its generated previews are named.
//
// id is the catalogue id, or zero for a file the catalogue does not hold, which
// is what decides whether the hardlink farm can be asked for it. subject keys
// the preview cache and must carry its own numbering's name, since a Bin row
// and an asset both count from one. posterID is the asset whose captured social
// still may stand in for a frame, or zero when there is none.
type mediaFile struct {
	relative string
	kind     string
	id       int64
	subject  string
	posterID int64
}

// serveMedia answers one media request for a file that has already been
// identified. It is shared by every route that shows a file, so a preview that
// works in the gallery works in the Bin too, rather than each route growing its
// own half of the decision.
func (s *Store) serveMedia(w http.ResponseWriter, r *http.Request, roots MediaRoots, mode string, source mediaFile) {
	relative, kind, id := source.relative, source.kind, source.id
	mountRoot, inMount, known := roots.root(relative, id)
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
	if videoPreview && s.servedPoster(w, r, source.posterID, roots.Posters) {
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
		s.servePoster(w, r, source.posterID, roots.Posters)
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
		// The viewer asks for the large size; a grid asks for a tile.
		pixels := gridPixels
		if r.URL.Query().Get("size") == "large" {
			pixels = viewerPixels
		}
		var tile []byte
		var rawErr error
		if rawPreviewWanted {
			tile, rawErr = rawPreview(r.Context(), roots.RawTool, file, roots.Cache, source.subject, info.Size(), info.ModTime().Unix(), pixels)
			if rawErr != nil && roots.FFmpeg != "" {
				// Whatever the file really is, the frame extractor reads far
				// more formats than the name suggested.
				tile, rawErr = stillFrame(r.Context(), roots.FFmpeg, file, roots.Cache, source.subject, info.Size(), info.ModTime().Unix(), pixels)
			}
		} else {
			tile, rawErr = stillFrame(r.Context(), roots.FFmpeg, file, roots.Cache, source.subject, info.Size(), info.ModTime().Unix(), pixels)
		}
		if rawErr != nil {
			// No embedded preview is an honest miss: the page keeps its own
			// fallback rather than an <img> being handed sensor data. It is
			// still logged, because a decoder that cannot run looks exactly
			// like a file with nothing to show.
			log.Printf("preview %s: %v", source.subject, rawErr)
			s.servePoster(w, r, source.posterID, roots.Posters)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeContent(w, r, "raw.jpg", thumbnailModTime(info), bytes.NewReader(tile))
		return
	}

	if videoPreview {
		frame, frameErr := videoFrame(r.Context(), roots.FFmpeg, file, roots.Cache, source.subject, info.Size(), info.ModTime().Unix())
		if frameErr != nil {
			// No poster and no frame is an honest miss: the page keeps its
			// own fallback rather than an <img> being handed a container it
			// cannot decode.
			log.Printf("frame %s: %v", source.subject, frameErr)
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
		if tile, tileErr := thumbnail(file, roots.Cache, source.subject, info.Size(), info.ModTime().Unix()); tileErr == nil {
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
