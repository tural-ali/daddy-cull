package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()
	decisions := func(w http.ResponseWriter, r *http.Request) {
		// JSON-only plus same-origin checks stop browser cross-site form writes.
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON required", 415)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "origin rejected", 403)
			return
		}
		var ds []Decision
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		dec.DisallowUnknownFields()
		var decodeErr error
		batch := r.URL.Path == "/api/decisions/batch"
		if batch {
			decodeErr = dec.Decode(&ds)
		} else {
			var d Decision
			decodeErr = dec.Decode(&d)
			ds = []Decision{d}
		}
		if decodeErr != nil {
			http.Error(w, "invalid decision", 400)
			return
		}
		if e := dec.Decode(new(any)); e != io.EOF {
			http.Error(w, "one decision required", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		result, e := s.DecideBatch(ctx, ds)
		if e != nil {
			status := 503
			if errors.Is(e, ErrConflict) {
				status = 409
			} else if errors.Is(e, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if batch {
			json.NewEncoder(w).Encode(result)
		} else {
			json.NewEncoder(w).Encode(result[0])
		}
	}
	mux.HandleFunc("POST /api/decisions", decisions)
	mux.HandleFunc("POST /api/decisions/batch", decisions)
	mux.HandleFunc("GET /api/assets/{id}/alternatives", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid asset", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		assets, err := s.Alternatives(ctx, id)
		if err != nil {
			status := 503
			if errors.Is(err, sql.ErrNoRows) {
				status = 404
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(assets)
	})
	mux.HandleFunc("GET /api/assets/{id}/related", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid asset", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		assets, err := s.Related(ctx, id)
		if err != nil {
			status := 503
			if errors.Is(err, sql.ErrNoRows) {
				status = 404
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(assets)
	})
	mux.HandleFunc("GET /api/assets", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		limit := 80
		if v := r.URL.Query().Get("limit"); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil {
				http.Error(w, "invalid limit", 400)
				return
			}
			limit = n
		}
		var p Page
		var e error
		if r.URL.Query().Get("view") == "review" {
			p, e = s.page(ctx, r.URL.Query().Get("after"), r.URL.Query().Get("kind"), "", limit, true, r.URL.Query().Get("from"), r.URL.Query().Get("matches") == "1", r.URL.Query().Get("status"), r.URL.Query().Get("groups") == "1")
		} else {
			p, e = s.Page(ctx, r.URL.Query().Get("after"), r.URL.Query().Get("kind"), r.URL.Query().Get("source"), limit)
		}
		if e != nil {
			status := 503
			if errors.Is(e, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		n, e := s.Count(ctx)
		if e != nil {
			http.Error(w, "catalogue unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		var library, snapshot string
		_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='library'").Scan(&library)
		_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='snapshot_at'").Scan(&snapshot)
		var candidates int
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM assets WHERE anchor_id IS NOT NULL").Scan(&candidates)
		json.NewEncoder(w).Encode(map[string]any{"total": n, "synthetic": library != "real", "snapshotAt": snapshot, "candidates": candidates})
	})
	return mux
}
