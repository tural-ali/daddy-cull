package catalog

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// Clips start muted until the reviewer says otherwise; the choice is saved by
// the preview process, same-origin JSON only, and comes back with the stats.
func TestVideoSoundSetting(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if muted, err := s.VideoMuted(ctx); err != nil || !muted {
		t.Fatalf("clips do not start muted by default: %v %v", muted, err)
	}
	handler := s.Handler()
	post := func(body string, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/settings/video", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, request)
		return got
	}
	if got := post(`{"muted":false}`, ""); got.Code != 200 || strings.TrimSpace(got.Body.String()) != `{"muted":false}` {
		t.Fatalf("save: %d %s", got.Code, got.Body)
	}
	if muted, _ := s.VideoMuted(ctx); muted {
		t.Fatal("the sound is still off after choosing it")
	}
	for _, body := range []string{`{}`, `{"muted":"no"}`, `{"muted":true,"other":1}`} {
		if got := post(body, ""); got.Code != 400 {
			t.Fatalf("%s accepted: %d", body, got.Code)
		}
	}
	if got := post(`{"muted":true}`, "https://elsewhere.example"); got.Code != 403 {
		t.Fatalf("a cross-site request changed the setting: %d", got.Code)
	}
	if muted, _ := s.VideoMuted(ctx); muted {
		t.Fatal("a rejected request changed the setting")
	}
	stats := httptest.NewRecorder()
	handler.ServeHTTP(stats, httptest.NewRequest("GET", "/api/stats", nil))
	if stats.Code != 200 || !strings.Contains(stats.Body.String(), `"videoMuted":false`) {
		t.Fatalf("stats: %d %s", stats.Code, stats.Body)
	}
	if got := post(`{"muted":true}`, ""); got.Code != 200 {
		t.Fatalf("save: %d %s", got.Code, got.Body)
	}
	if muted, _ := s.VideoMuted(ctx); !muted {
		t.Fatal("muting again did not hold")
	}
}
