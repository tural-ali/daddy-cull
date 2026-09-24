package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWebAppNeverServesAStalePage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "app", "queue.html": "queue", "assets/app-abc123.js": "js"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := webApp(dir, []string{"/bin", "/year"})
	for _, c := range []struct{ path, body, cache string }{
		{"/", "app", "no-cache"},
		{"/bin", "app", "no-cache"},
		{"/on/09-23", "app", "no-cache"},
		{"/day/2020-09-23", "app", "no-cache"},
		{"/queue.html", "queue", "no-cache"},
		{"/assets/app-abc123.js", "js", "public, max-age=31536000, immutable"},
	} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest("GET", c.path, nil))
		if response.Code != http.StatusOK || response.Body.String() != c.body || response.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s: got %d %q cache %q, want %q cache %q", c.path, response.Code, response.Body.String(), response.Header().Get("Cache-Control"), c.body, c.cache)
		}
	}
}
