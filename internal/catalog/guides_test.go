package catalog

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// A guide hidden once stays hidden in every browser: the choice is saved in
// the catalogue, one page at a time, same-origin JSON only, and comes back
// with the stats; with no page, every guide shows again.
func TestGuideSettings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	handler := s.Handler()
	post := func(body, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/settings/guides", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, request)
		return got
	}
	stats := func() string {
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, httptest.NewRequest("GET", "/api/stats", nil))
		if got.Code != 200 {
			t.Fatalf("stats: %d %s", got.Code, got.Body)
		}
		return got.Body.String()
	}
	if !strings.Contains(stats(), `"hiddenGuides":[]`) {
		t.Fatalf("a new catalogue hides a guide: %s", stats())
	}
	for _, page := range []string{"upgrades", "today", "upgrades"} {
		if got := post(`{"page":"`+page+`","hidden":true}`, ""); got.Code != 200 || strings.TrimSpace(got.Body.String()) != `{"page":"`+page+`","hidden":true}` {
			t.Fatalf("hide %s: %d %s", page, got.Code, got.Body)
		}
	}
	if !strings.Contains(stats(), `"hiddenGuides":["today","upgrades"]`) {
		t.Fatalf("the hidden guides are not read back once each: %s", stats())
	}
	if got := post(`{"page":"today","hidden":false}`, ""); got.Code != 200 {
		t.Fatalf("show: %d %s", got.Code, got.Body)
	}
	if pages, _ := s.HiddenGuides(ctx); len(pages) != 1 || pages[0] != "upgrades" {
		t.Fatalf("showing one guide changed another: %v", pages)
	}
	for _, body := range []string{`{}`, `{"page":"today"}`, `{"hidden":true}`, `{"page":"Today","hidden":true}`, `{"page":"../x","hidden":true}`, `{"page":"today","hidden":"yes"}`, `{"page":"today","hidden":true,"other":1}`} {
		if got := post(body, ""); got.Code != 400 {
			t.Fatalf("%s accepted: %d", body, got.Code)
		}
	}
	if got := post(`{"hidden":false}`, "https://elsewhere.example"); got.Code != 403 {
		t.Fatalf("a cross-site request changed the guides: %d", got.Code)
	}
	if pages, _ := s.HiddenGuides(ctx); len(pages) != 1 {
		t.Fatalf("a refused request changed the guides: %v", pages)
	}
	// Only the guides' own rows go when every guide shows again.
	if err := s.SetVideoMuted(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := post(`{"hidden":false}`, ""); got.Code != 200 {
		t.Fatalf("show all: %d %s", got.Code, got.Body)
	}
	if pages, _ := s.HiddenGuides(ctx); len(pages) != 0 {
		t.Fatalf("a guide is still hidden: %v", pages)
	}
	if muted, _ := s.VideoMuted(ctx); muted {
		t.Fatal("showing every guide changed another setting")
	}
}
