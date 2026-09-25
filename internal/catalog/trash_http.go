package catalog

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// trashBodyLimit allows a whole Bin to be selected at once. A key is a few dozen
// bytes, so this is room for tens of thousands of them and still small.
const trashBodyLimit = 1 << 20

// TrashGateway forwards the Bin page's three actions to the private writer, the
// only process that can move a file.
func TrashGateway(upstream, secret string) http.Handler {
	client := &http.Client{Timeout: 60 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/api/trash")
		if r.Method != "POST" || (route != "/restore" && route != "/restore-file" && route != "/delete" && route != "/empty" && route != "/purge-now") {
			http.NotFound(w, r)
			return
		}
		if !sameOriginJSON(w, r) {
			return
		}
		if upstream == "" || len(secret) < 32 {
			http.Error(w, "Bin service unavailable", 503)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, trashBodyLimit))
		if err != nil {
			http.Error(w, "request too large", 413)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), "POST", upstream+"/trash"+route, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "Bin unavailable", 503)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Bin-Key", secret)
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "The Bin's result could not be confirmed. Reload the Bin before trying again.", 503)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	})
}

// Handler serves the writer's side of the Bin page's actions. An action runs to
// the end even if the page is closed halfway, so no batch is left half moved
// because a browser tab went away.
func (t *TrashWriter) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, result TrashResult, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(result)
	}
	type input struct {
		Keys         []string `json:"keys"`
		Confirmation string   `json:"confirmation"`
	}
	decode := func(w http.ResponseWriter, r *http.Request) (input, error) {
		var value input
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, trashBodyLimit))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return value, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return value, fmt.Errorf("one request required")
		}
		return value, nil
	}
	run := func(action func(context.Context, input) (TrashResult, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			value, err := decode(w, r)
			if err != nil {
				respond(w, TrashResult{}, err)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
			defer cancel()
			result, err := action(ctx, value)
			respond(w, result, err)
		}
	}
	mux.HandleFunc("POST /trash/restore", run(func(ctx context.Context, value input) (TrashResult, error) {
		return t.Restore(ctx, value.Keys)
	}))
	mux.HandleFunc("POST /trash/restore-file", run(func(ctx context.Context, value input) (TrashResult, error) {
		if len(value.Keys) != 1 {
			return TrashResult{}, fmt.Errorf("choose exactly one file to restore")
		}
		return t.RestoreFile(ctx, value.Keys[0])
	}))
	mux.HandleFunc("POST /trash/delete", run(func(ctx context.Context, value input) (TrashResult, error) {
		return t.Delete(ctx, value.Keys, value.Confirmation)
	}))
	mux.HandleFunc("POST /trash/purge-now", run(func(ctx context.Context, value input) (TrashResult, error) {
		return t.PurgeNow(ctx, value.Keys, value.Confirmation)
	}))
	mux.HandleFunc("POST /trash/empty", run(func(ctx context.Context, value input) (TrashResult, error) {
		return t.Empty(ctx, value.Confirmation)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(r.Header.Get("X-Bin-Key"))) != 1 {
			http.Error(w, "forbidden", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
