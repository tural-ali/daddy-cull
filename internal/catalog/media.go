package catalog

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MediaHandler exposes only fixed read routes for catalogued asset IDs.
// No user-supplied URL or filesystem path reaches the upstream application.
func (s *Store) MediaHandler(upstream string) http.Handler {
	client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 4, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 45 * time.Second}, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	slots := make(chan struct{}, 4)
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
		var p string
		err = s.read.QueryRowContext(ctx, "SELECT relative_path FROM assets WHERE id=?", id).Scan(&p)
		cancel()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		route := "/thumb"
		q := url.Values{"p": {p}, "size": {"grid"}}
		if r.URL.Query().Get("size") == "large" {
			q.Set("size", "large")
		}
		if r.PathValue("mode") == "original" {
			route = "/file"
			q.Del("size")
		} else if r.PathValue("mode") != "preview" {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(p, "/upgrades/") {
			serveTakeout(w, r, p)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-r.Context().Done():
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+route+"?"+q.Encode(), nil)
		if err != nil {
			http.Error(w, "preview unavailable", 502)
			return
		}
		if v := r.Header.Get("Range"); v != "" {
			req.Header.Set("Range", v)
		}
		res, err := client.Do(req)
		if err != nil {
			http.Error(w, "preview unavailable", 502)
			return
		}
		defer res.Body.Close()
		if res.StatusCode != 200 && res.StatusCode != 206 {
			http.Error(w, "preview unavailable", 502)
			return
		}
		for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
			if v := res.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	})
}
