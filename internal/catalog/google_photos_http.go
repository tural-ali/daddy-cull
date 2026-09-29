package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"daddy-cull/next/internal/api"
	"daddy-cull/next/internal/takeout"
)

// GooglePhotosSkipped says how many photos a skip changed.
type GooglePhotosSkipped struct {
	// Changed counts the photos set aside or waiting again. A photo already
	// added, or being added by a task, is left as it is.
	Changed int `json:"changed"`
}

// unpackLimit is the largest photo or video shown straight from a zip. A
// bigger one has to be unpacked to its own folder to be seen, since it is
// copied out whole first.
const unpackLimit = 1 << 30

// unpackedKeep is how much the unpacked copies may take up before the oldest
// are removed, and unpackedTrim what they are cut back to.
const (
	unpackedKeep = 4 << 30
	unpackedTrim = 2 << 30
)

// googlePhotosLimit is how many photos a page of a tab holds.
const googlePhotosLimit = 120

// googlePhotosTabNames are the tabs of GET /api/google-photos, in order.
var googlePhotosTabNames = []string{"missing", "alternative", "uncertain", "represented", "removed", "added", "skipped"}

// Routes serves the Google Photos page, and adding its photos through tasks.
func (g *GooglePhotos) Routes(m *api.Mux, tasks *TaskRunner, roots MediaRoots) {
	s := g.s
	m.Book().Tag("Google Photos", "Photos from Google Takeout exports dropped into the inbox, checked against the library: those it has no copy of can be added to the archive under the day they were taken. The exports are only read, and nothing already in the archive is moved or replaced.")
	notJSONBody := "Send the photos' ids, as GET /api/google-photos lists them."
	tab := api.Query("tab", "string", "Which photos to list. missing is the default.")
	tab.Enum = googlePhotosTabNames
	from := api.Query("from", "integer", "How many to skip, for the next page. Pages hold 120.")
	noInbox := api.Error{Status: 409, When: "Cull was started without a Takeout inbox."}

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/google-photos", Addon: AddonGooglePhotos, Tag: "Google Photos", Needs: api.Read,
		Summary: "List photos from Google Photos",
		Doc: "The exports in the inbox, how many photos each tab holds, and one tab's photos, oldest first. " +
			"missing are photos the library has no copy of; alternative are those it holds a different copy of, under the same name from the same day; uncertain are those Cull cannot place, with no date or like a file it could not compare; " +
			"represented are already in the library, removed were removed in Cull, added were added from here, and skipped were set aside.",
		Params:  []api.Param{tab, from},
		Returns: GooglePhotosPage{}, Errors: []api.Error{{Status: 400, When: "The tab is not one of those listed"}, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		start, ok := fromParam(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		page, err := g.Page(ctx, r.URL.Query().Get("tab"), start, googlePhotosLimit)
		if err != nil {
			failFor(w, err, "tab should be one of "+strings.Join(googlePhotosTabNames, ", ")+".")
			return
		}
		writeJSON(w, page)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/google-photos/scan", Addon: AddonGooglePhotos, Tag: "Google Photos", Needs: api.Import,
		Summary: "Read the inbox again",
		Doc:     "Reads the inbox now rather than at the next check, which comes every five minutes, and checks every waiting photo against the library again. Answers at once; the page shows Scanning until it is done.",
		Returns: GooglePhotosPage{}, Status: http.StatusAccepted, Errors: []api.Error{noInbox, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		if !g.Configured() {
			api.Fail(w, 409, "Cull was started without a Takeout inbox, so there is nothing to read. The page says how to add one.")
			return
		}
		g.Wake()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		page, err := g.Page(ctx, "missing", 0, 0)
		if err != nil {
			failFor(w, err, "")
			return
		}
		accepted(w, page)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/google-photos/add", Addon: AddonGooglePhotos, Tag: "Google Photos", Needs: api.Import,
		Summary: "Add photos to the library in the background",
		Doc: "Queues copying photos into the archive and answers at once. Each goes under the day it was taken, as 2019/2019-08/2019-08-14/IMG_1234.JPG, or beside the library's different copy as IMG_1234 (Google Photos).JPG, " +
			"dated with when it was taken; a number is added if the name is taken, and a photo whose bytes the folder already holds is refused. Only missing and alternative photos can be added. They leave their tab straight away and show under added once in.",
		Body: GooglePhotosSelection{}, Returns: Task{}, Status: http.StatusAccepted,
		Errors: []api.Error{{Status: 400, When: "No photos, or more than 20,000, were named"}, {Status: 409, When: "A photo can no longer be added, or is already waiting in a task. The body says which."}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var request GooglePhotosSelection
		if !decodeBody(w, r, 1<<20, &request, notJSONBody) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err := s.QueueGooglePhotos(ctx, request)
		if err != nil {
			taskFail(w, err, notJSONBody)
			return
		}
		tasks.Wake()
		accepted(w, task)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/google-photos/skip", Addon: AddonGooglePhotos, Tag: "Google Photos", Needs: api.Review,
		Summary: "Set photos aside, or take them back",
		Doc:     "A photo set aside is no longer offered for adding and moves to the skipped tab; skip false has it waiting again. Nothing on disk changes.",
		Body:    GooglePhotosSkip{}, Returns: GooglePhotosSkipped{},
		Errors: []api.Error{{Status: 400, When: "No photos, or more than 20,000, were named"}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var request GooglePhotosSkip
		if !decodeBody(w, r, 1<<20, &request, notJSONBody) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		n, err := s.SkipGooglePhotos(ctx, request)
		if err != nil {
			failFor(w, err, notJSONBody)
			return
		}
		writeJSON(w, GooglePhotosSkipped{Changed: n})
	})

	mode := api.Param{Name: "mode", In: "path", Type: "string", Required: true, Enum: []string{"preview", "original"}, Example: "preview",
		Doc: "preview is a picture small enough for a grid, a frame for a video; original is the file as it is."}
	size := api.Query("size", "string", "large asks for a bigger preview, for the viewer.")
	size.Enum = []string{"large"}
	roots.Takeout = g.dir
	if g.cache != "" {
		roots.Unpacked = filepath.Join(g.cache, "takeout-unpacked")
	}
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/google-photos/media/{id}/{mode}", Addon: AddonGooglePhotos, Tag: "Google Photos", Needs: api.Read,
		Summary:  "Get a photo from an export",
		Doc:      "A photo or video from the inbox by its id, as /api/media serves the library's. One inside a zip is unpacked to a cache first, which is why a file over 1 GB inside a zip is not shown; unpack that export into a folder to see it.",
		Params:   []api.Param{api.PathInt("id", "The photo's id from GET /api/google-photos.", "1"), mode, size},
		Produces: "image/*, video/*",
		Errors:   []api.Error{{Status: 404, When: "There is no such photo in the inbox, or it cannot be shown."}},
	}, func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		want := r.PathValue("mode")
		if err != nil || id < 1 || (want != "preview" && want != "original") || !g.Configured() {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		var archive, kind, entry, itemKind string
		var crc sql.NullInt64
		var bytes int64
		err = s.read.QueryRowContext(ctx, `SELECT a.name,a.kind,e.path,e.crc32,e.size_bytes,i.kind FROM takeout_entries e JOIN takeout_archives a ON a.id=e.archive_id JOIN takeout_items i ON i.id=e.item_id
			WHERE e.item_id=? AND e.kind='media' AND a.present=1 ORDER BY a.kind='folder' DESC,a.id LIMIT 1`, id).Scan(&archive, &kind, &entry, &crc, &bytes, &itemKind)
		cancel()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		subject := "takeout/" + strconv.FormatInt(id, 10)
		if kind == takeout.KindFolder {
			s.serveMedia(w, r, roots, want, mediaFile{relative: "/takeout/" + archive + "/" + entry, kind: itemKind, subject: subject})
			return
		}
		if roots.Unpacked == "" || bytes > unpackLimit {
			http.NotFound(w, r)
			return
		}
		name, err := g.unpack(r.Context(), roots.Unpacked, id, archive, kind, entry, bytes, crc.Int64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.serveMedia(w, r, roots, want, mediaFile{relative: "/takeout-unpacked/" + name, kind: itemKind, subject: subject})
	})
}

// accepted answers 202 for work that goes on after the answer.
func accepted(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(value)
}

var unpacking sync.Mutex

// unpack copies one photo out of a zip into dir, once, so it can be shown and
// seeked like a file on disk. The copy is named by the photo and the zip's
// own checksum of it, so a changed export is unpacked again.
func (g *GooglePhotos) unpack(ctx context.Context, dir string, id int64, archive, kind, entry string, size, crc int64) (string, error) {
	name := fmt.Sprintf("%d-%d-%08x%s", id, size, crc, strings.ToLower(path.Ext(entry)))
	unpacking.Lock()
	defer unpacking.Unlock()
	target := filepath.Join(dir, name)
	if info, err := os.Stat(target); err == nil && info.Size() == size {
		now := time.Now()
		_ = os.Chtimes(target, now, now)
		return name, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	source, err := g.inbox.ReadEntry(archive, kind, entry)
	if err != nil {
		return "", err
	}
	defer source.Close()
	temp, err := os.CreateTemp(dir, ".unpacking-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	n, err := io.Copy(temp, io.LimitReader(&contextReader{ctx, source}, unpackLimit+1))
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if n != size {
		return "", fmt.Errorf("the photo in %s is not the size it was listed with", archive)
	}
	if err := os.Rename(temp.Name(), target); err != nil {
		return "", err
	}
	trimUnpacked(dir, name)
	return name, nil
}

// trimUnpacked removes the copies least recently shown once they take up
// more than unpackedKeep, keeping the one just made.
func trimUnpacked(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type copyInfo struct {
		name string
		size int64
		used time.Time
	}
	var copies []copyInfo
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		copies = append(copies, copyInfo{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= unpackedKeep {
		return
	}
	sort.Slice(copies, func(a, b int) bool { return copies[a].used.Before(copies[b].used) })
	for _, c := range copies {
		if total <= unpackedTrim {
			return
		}
		if c.name == keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, c.name)); err == nil || errors.Is(err, fs.ErrNotExist) {
			total -= c.size
		}
	}
}
