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

func LegacyBinGateway(upstream, secret string) http.Handler {
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/api/legacy-bin")
		if r.Method != "POST" || (route != "/preview" && route != "/execute") {
			http.NotFound(w, r)
			return
		}
		if !sameOriginJSON(w, r) {
			return
		}
		if upstream == "" || len(secret) < 32 {
			http.Error(w, "legacy Bin service unavailable", 503)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			http.Error(w, "request too large", 413)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+"/legacy-bin"+route, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "legacy Bin unavailable", 503)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Bin-Key", secret)
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "Bin result could not be confirmed. Reopen the Bin before retrying.", 503)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	})
}

func (b *LegacyBinEngine) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, value any, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "plan": value})
			return
		}
		json.NewEncoder(w).Encode(value)
	}
	decode := func(w http.ResponseWriter, r *http.Request, value any) error {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(value); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("one request required")
		}
		return nil
	}
	mux.HandleFunc("POST /legacy-bin/preview", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			IDs []int64 `json:"ids"`
		}
		if err := decode(w, r, &input); err != nil {
			respond(w, nil, err)
			return
		}
		plan, err := b.Preview(r.Context(), input.IDs)
		respond(w, plan, err)
	})
	mux.HandleFunc("POST /legacy-bin/execute", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID           string `json:"id"`
			Action       string `json:"action"`
			Confirmation string `json:"confirmation"`
		}
		if err := decode(w, r, &input); err != nil {
			respond(w, nil, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		plan, err := b.Run(ctx, input.ID, input.Action, input.Confirmation)
		respond(w, plan, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(r.Header.Get("X-Bin-Key"))) != 1 {
			http.Error(w, "forbidden", 403)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
