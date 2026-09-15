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
	writeJSON := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("GET /api/year", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		data, err := s.Calendar(ctx, time.Now())
		if err != nil {
			http.Error(w, "catalogue unavailable", 503)
			return
		}
		writeJSON(w, data)
	})
	mux.HandleFunc("GET /api/today/{md}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		data, err := s.Today(ctx, r.PathValue("md"))
		if err != nil {
			status := 503
			if errors.Is(err, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		writeJSON(w, data)
	})
	mux.HandleFunc("GET /api/duplicates", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		limit := 100
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				http.Error(w, "invalid limit", 400)
				return
			}
			limit = parsed
		}
		groups, err := s.ExactDuplicates(ctx, r.URL.Query().Get("md"), limit)
		if err != nil {
			status := 503
			if errors.Is(err, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		writeJSON(w, groups)
	})
	mux.HandleFunc("GET /api/upgrades", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		data, err := s.Upgrades(ctx)
		if err != nil {
			http.Error(w, "catalogue unavailable", 503)
			return
		}
		writeJSON(w, data)
	})
	mux.HandleFunc("GET /api/log", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		limit := 200
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				http.Error(w, "invalid limit", 400)
				return
			}
			limit = parsed
		}
		events, err := s.History(ctx, limit)
		if err != nil {
			status := 503
			if errors.Is(err, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		writeJSON(w, events)
	})
	mux.HandleFunc("GET /api/screenshots", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		from := 0
		var err error
		if raw := r.URL.Query().Get("from"); raw != "" {
			from, err = strconv.Atoi(raw)
		}
		if err != nil || from < 0 {
			http.Error(w, http.StatusText(400), 400)
			return
		}
		items, err := s.ScreenshotPage(ctx, r.URL.Query().Get("kind"), from, 120)
		if err != nil {
			status := 503
			if errors.Is(err, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		writeJSON(w, items)
	})
	mux.HandleFunc("GET /api/social", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		from := 0
		var err error
		if raw := r.URL.Query().Get("from"); raw != "" {
			from, err = strconv.Atoi(raw)
		}
		if err != nil || from < 0 {
			http.Error(w, http.StatusText(400), 400)
			return
		}
		page, err := s.SocialCandidates(ctx, r.URL.Query().Get("band"), from, 120)
		if err != nil {
			status := 503
			if errors.Is(err, ErrInvalid) {
				status = 400
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		writeJSON(w, page)
	})
	mux.HandleFunc("GET /api/shadows", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		groups, err := s.ShadowGroups(ctx, 500)
		if err != nil {
			http.Error(w, "catalogue unavailable", 503)
			return
		}
		writeJSON(w, groups)
	})
	mux.HandleFunc("GET /api/legacy-bin", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, err := s.LegacyBin(ctx, 5000)
		if err != nil {
			http.Error(w, "catalogue unavailable", 503)
			return
		}
		writeJSON(w, items)
	})
	mux.HandleFunc("GET /api/screenshot-bin", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		plans, err := s.ScreenshotBin(ctx, 500)
		if err != nil {
			http.Error(w, "catalogue unavailable", 503)
			return
		}
		writeJSON(w, plans)
	})
	mux.HandleFunc("POST /api/day-progress", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON required", 415)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "origin rejected", 403)
			return
		}
		var change DayProgressChange
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&change); err != nil {
			http.Error(w, "invalid progress", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "one progress change required", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		result, err := s.SetDayProgress(ctx, change)
		if err != nil {
			status := 503
			if errors.Is(err, ErrInvalid) {
				status = 400
			} else if errors.Is(err, ErrConflict) {
				status = 409
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("POST /api/reindex", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON required", 415)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "origin rejected", 403)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.IndexRelated(ctx); err != nil {
			http.Error(w, "index unavailable", 503)
			return
		}
		if err := s.IndexCalendar(ctx); err != nil {
			http.Error(w, "index unavailable", 503)
			return
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('snapshot_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
			http.Error(w, "index unavailable", 503)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
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
		var calendarDays, reviewedDays, decisions, favourites, evidence, fullHashes, marked int
		_ = s.read.QueryRowContext(ctx, "SELECT count(DISTINCT day) FROM asset_days").Scan(&calendarDays)
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM day_progress WHERE status='done'").Scan(&reviewedDays)
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM decisions WHERE status!='unreviewed' OR favourite=1").Scan(&decisions)
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM decisions WHERE favourite=1").Scan(&favourites)
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM asset_evidence").Scan(&evidence)
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM asset_evidence WHERE full_hash IS NOT NULL").Scan(&fullHashes)
		_ = s.read.QueryRowContext(ctx, "SELECT count(*) FROM decisions d WHERE d.status='cull' AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=d.asset_id AND fs.state!='restored')").Scan(&marked)
		legacyBin := countQuery(ctx, s.read, "SELECT count(*) FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL")
		shadowGroups := countQuery(ctx, s.read, "SELECT count(*) FROM (SELECT 1 FROM shadow_entries GROUP BY kind,group_key)")
		screenshots := countQuery(ctx, s.read, "SELECT count(*) FROM screenshot_items WHERE state='waiting'")
		social := countQuery(ctx, s.read, "SELECT count(*) FROM social_items s LEFT JOIN decisions d ON d.asset_id=s.asset_id WHERE s.state='waiting'"+socialPending)
		upgradesAccepted := countQuery(ctx, s.read, "SELECT count(*) FROM upgrade_history")
		upgradeCandidates := countQuery(ctx, s.read, "SELECT count(DISTINCT archive_asset_id) FROM upgrade_candidates")
		json.NewEncoder(w).Encode(map[string]any{"total": n, "synthetic": library != "real", "snapshotAt": snapshot, "candidates": candidates, "calendarDays": calendarDays, "reviewedDays": reviewedDays, "decisions": decisions, "favourites": favourites, "evidence": evidence, "fullHashes": fullHashes, "marked": marked, "legacyBin": legacyBin, "shadowGroups": shadowGroups, "screenshots": screenshots, "social": social, "upgradesAccepted": upgradesAccepted, "upgradeCandidates": upgradeCandidates})
	})
	return mux
}
