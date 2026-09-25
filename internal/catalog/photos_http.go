package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// PhotosAgentHeader carries the helper's shared secret. A header rather than a
// query parameter, so the key never lands in an access log or a proxy's URL
// history, and a custom one, so a browser cannot send it cross-site without a
// preflight this server never answers.
const PhotosAgentHeader = "X-Photos-Agent-Key"

// photosReportLimit bounds one report from the helper. It sends matches a few
// dozen at a time, so even with thumbnails a report is a megabyte or two.
const photosReportLimit = 8 << 20

// Handler serves the Photos page's routes and the helper's. The page's POSTs
// pass the same JSON and same-origin checks as every other write in the app;
// the helper's routes need the shared key instead, because the helper is not a
// browser and has no origin to check.
func (h *PhotosHub) Handler() http.Handler {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	fail := func(w http.ResponseWriter, status int, message string) {
		writeJSON(w, status, map[string]string{"error": message})
	}
	failFor := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, ErrPhotosDisabled):
			fail(w, 503, "The Mac helper is not set up on the server: PHOTOS_AGENT_KEY is missing.")
		case errors.Is(err, ErrPhotosStale):
			fail(w, 409, "This sync has moved on, perhaps in another tab. The page has been refreshed.")
		case errors.Is(err, ErrInvalid):
			fail(w, 400, "That request did not match this sync.")
		default:
			fail(w, 503, "The catalogue could not be read. Try again in a moment.")
		}
	}
	decode := func(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(into); err != nil {
			fail(w, 400, "The request could not be read.")
			return false
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			fail(w, 400, "One request body expected.")
			return false
		}
		return true
	}

	// The page.
	mux.HandleFunc("GET /api/photos", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, h.Status())
	})
	mux.HandleFunc("GET /api/photos/overview", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		plan, err := h.s.PhotosPlan(ctx)
		if err != nil {
			failFor(w, err)
			return
		}
		deleted, favourited, last := h.s.PhotosSyncCounts(ctx)
		writeJSON(w, 200, map[string]any{
			"delete": len(plan.Delete), "favourite": len(plan.Favourite), "held": len(plan.Held), "undated": plan.Undated,
			"restored": plan.Restored, "synced": map[string]any{"deleted": deleted, "favourited": favourited, "last": last},
		})
	})
	mux.HandleFunc("GET /api/photos/jobs/{job}", func(w http.ResponseWriter, r *http.Request) {
		view, ok := h.Job(r.PathValue("job"))
		if !ok {
			fail(w, 404, "That sync is no longer held. Check again.")
			return
		}
		writeJSON(w, 200, view)
	})
	mux.HandleFunc("GET /api/photos/thumb/{job}/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		thumb, ok := h.Thumb(r.PathValue("job"), n)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(thumb)
	})
	mux.HandleFunc("POST /api/photos/check", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginJSON(w, r) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		view, err := h.StartCheck(ctx)
		if errors.Is(err, ErrPhotosBusy) {
			writeJSON(w, 409, map[string]any{"error": "A sync is already running.", "job": view})
			return
		}
		if err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, view)
	})
	mux.HandleFunc("POST /api/photos/apply", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginJSON(w, r) {
			return
		}
		var body struct {
			Job       string   `json:"job"`
			Delete    []string `json:"delete"`
			Favourite []string `json:"favourite"`
		}
		if !decode(w, r, 1<<20, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		view, err := h.Apply(ctx, body.Job, body.Delete, body.Favourite)
		if errors.Is(err, ErrPhotosStale) && view.ID != "" {
			writeJSON(w, 409, map[string]any{"error": "Everything chosen has changed in Cull since the check. Check again.", "job": view})
			return
		}
		if err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, view)
	})
	mux.HandleFunc("POST /api/photos/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginJSON(w, r) {
			return
		}
		var body struct {
			Job string `json:"job"`
		}
		if !decode(w, r, 4096, &body) {
			return
		}
		view, err := h.Cancel(body.Job)
		if err != nil {
			if view.ID != "" {
				writeJSON(w, 409, map[string]any{"error": "The Mac is already changing Photos. Use the dialog on the Mac to stop it.", "job": view})
				return
			}
			failFor(w, err)
			return
		}
		writeJSON(w, 200, view)
	})
	mux.HandleFunc("POST /api/photos/forget", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginJSON(w, r) {
			return
		}
		var body struct {
			Keys []string `json:"keys"`
		}
		if !decode(w, r, 1<<20, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		n, err := h.s.ForgetPhotosDeletions(ctx, body.Keys)
		if err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, map[string]int{"forgotten": n})
	})

	// The helper.
	agent := http.NewServeMux()
	agent.HandleFunc("GET /api/photos/agent/work", func(w http.ResponseWriter, r *http.Request) {
		task, err := h.Claim(r.Context())
		if err != nil {
			// The helper hung up, or the server is shutting down.
			return
		}
		if task == nil {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, 200, task)
	})
	agent.HandleFunc("POST /api/photos/agent/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var beat PhotosHeartbeat
		if !decode(w, r, 8192, &beat) {
			return
		}
		writeJSON(w, 200, map[string]bool{"cancel": h.Seen(beat)})
	})
	agent.HandleFunc("POST /api/photos/agent/jobs/{job}/matches", func(w http.ResponseWriter, r *http.Request) {
		var report PhotosMatchReport
		if !decode(w, r, photosReportLimit, &report) {
			return
		}
		if err := h.Matches(r.PathValue("job"), report); err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	agent.HandleFunc("POST /api/photos/agent/jobs/{job}/checked", func(w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if !decode(w, r, 1024, &body) {
			return
		}
		if err := h.Checked(r.PathValue("job")); err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	agent.HandleFunc("POST /api/photos/agent/jobs/{job}/failed", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Error string `json:"error"`
		}
		if !decode(w, r, 8192, &body) {
			return
		}
		if err := h.Failed(r.PathValue("job"), body.Error); err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	agent.HandleFunc("POST /api/photos/agent/jobs/{job}/applied", func(w http.ResponseWriter, r *http.Request) {
		var report PhotosAppliedReport
		if !decode(w, r, 4<<20, &report) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := h.Applied(ctx, r.PathValue("job"), report); err != nil {
			if !errors.Is(err, ErrPhotosStale) && !errors.Is(err, ErrInvalid) {
				log.Printf("photos: could not record what the Mac changed: %v", err)
			}
			failFor(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.Handle("/api/photos/agent/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.enabled {
			failFor(w, ErrPhotosDisabled)
			return
		}
		if !h.authorised(r.Header.Get(PhotosAgentHeader)) {
			fail(w, 403, "The helper's key does not match PHOTOS_AGENT_KEY.")
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			fail(w, 415, "JSON required.")
			return
		}
		agent.ServeHTTP(w, r)
	}))
	return mux
}
