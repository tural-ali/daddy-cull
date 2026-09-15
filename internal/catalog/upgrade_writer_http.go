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

func UpgradeGateway(upstream, secret string) http.Handler {
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/api/upgrade-actions")
		if r.Method != "POST" || (route != "/preview" && route != "/execute") {
			http.NotFound(w, r)
			return
		}
		if !sameOriginJSON(w, r) {
			return
		}
		if upstream == "" || len(secret) < 32 {
			http.Error(w, "upgrade writer unavailable", 503)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			http.Error(w, "request too large", 413)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+"/upgrade"+route, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "upgrade writer unavailable", 503)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Bin-Key", secret)
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "Upgrade result could not be confirmed. Reload before retrying.", 503)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	})
}

func (w *UpgradeWriter) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	respond := func(writer http.ResponseWriter, value any, err error) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		if err != nil {
			writer.WriteHeader(409)
			json.NewEncoder(writer).Encode(map[string]any{"error": err.Error(), "plan": value})
			return
		}
		json.NewEncoder(writer).Encode(value)
	}
	decode := func(writer http.ResponseWriter, request *http.Request, value any) error {
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(value); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("one request required")
		}
		return nil
	}
	mux.HandleFunc("POST /upgrade/preview", func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			ArchiveAssetID int64 `json:"archiveAssetId"`
			SourceAssetID  int64 `json:"sourceAssetId"`
		}
		if err := decode(writer, request, &input); err != nil {
			respond(writer, nil, err)
			return
		}
		plan, err := w.Preview(request.Context(), input.ArchiveAssetID, input.SourceAssetID)
		respond(writer, plan, err)
	})
	mux.HandleFunc("POST /upgrade/execute", func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			ID string `json:"id"`
		}
		if err := decode(writer, request, &input); err != nil {
			respond(writer, nil, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		plan, err := w.Run(ctx, input.ID)
		respond(writer, plan, err)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(request.Header.Get("X-Bin-Key"))) != 1 {
			http.Error(writer, "forbidden", 403)
			return
		}
		mux.ServeHTTP(writer, request)
	})
}
