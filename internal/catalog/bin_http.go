package catalog

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func sameOriginJSON(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "JSON required", 415)
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "origin rejected", 403)
			return false
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "origin rejected", 403)
		return false
	}
	return true
}

// The web process has no archive mount. Only these fixed internal routes can be reached.
func BinGateway(upstream, secret string) http.Handler {
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/api/bin")
		if r.Method == "GET" && route != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method == "POST" {
			if route != "/preview" && route != "/execute" {
				http.NotFound(w, r)
				return
			}
			if !sameOriginJSON(w, r) {
				return
			}
		} else if r.Method != "GET" {
			http.Error(w, "method refused", 405)
			return
		}
		if upstream == "" || len(secret) < 32 {
			http.Error(w, "Bin service unavailable", 503)
			return
		}
		var body []byte
		var e error
		if r.Method == "POST" {
			body, e = io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
			if e != nil {
				http.Error(w, "request too large", 413)
				return
			}
		}
		req, e := http.NewRequestWithContext(r.Context(), r.Method, upstream+"/bin"+route, bytes.NewReader(body))
		if e != nil {
			http.Error(w, "Bin unavailable", 503)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Bin-Key", secret)
		res, e := client.Do(req)
		if e != nil {
			http.Error(w, "Bin result could not be confirmed. Reopen the Bin before retrying.", 503)
			return
		}
		defer res.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	})
}
func (b *BinEngine) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, p any, e error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if e != nil {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"error": e.Error(), "plan": p})
			return
		}
		json.NewEncoder(w).Encode(p)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) error {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		d.DisallowUnknownFields()
		if e := d.Decode(v); e != nil {
			return e
		}
		if e := d.Decode(new(any)); e != io.EOF {
			return fmt.Errorf("one request required")
		}
		return nil
	}
	mux.HandleFunc("GET /bin", func(w http.ResponseWriter, r *http.Request) { p, e := b.List(); respond(w, p, e) })
	mux.HandleFunc("POST /bin/preview", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			IDs []int64 `json:"ids"`
		}
		if e := decode(w, r, &v); e != nil {
			respond(w, nil, e)
			return
		}
		p, e := b.Preview(r.Context(), v.IDs)
		respond(w, p, e)
	})
	mux.HandleFunc("POST /bin/execute", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ID           string `json:"id"`
			Action       string `json:"action"`
			Confirmation string `json:"confirmation"`
		}
		if e := decode(w, r, &v); e != nil {
			respond(w, nil, e)
			return
		}
		// Once explicitly requested, finish even if the browser disconnects. The ledger
		// makes a repeated request recoverable, and GET /bin exposes its outcome.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		p, e := b.Run(ctx, v.ID, v.Action, v.Confirmation)
		respond(w, p, e)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/health" {
			w.WriteHeader(200)
			return
		}
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(r.Header.Get("X-Bin-Key"))) != 1 {
			http.Error(w, "forbidden", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
