package catalog

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestHTTPBounds(t *testing.T) {
	s := testStore(t)
	if err := s.Seed(context.Background(), 12); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		url    string
		status int
	}{{"/api/assets", 200}, {"/api/assets?limit=0", 400}, {"/api/assets?kind=bogus", 400}, {"/api/assets?after=bad", 400}, {"/api/stats", 200}} {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, httptest.NewRequest("GET", tc.url, nil))
		if r.Code != tc.status {
			t.Fatalf("%s: %d", tc.url, r.Code)
		}
	}
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("POST", "/api/assets", nil))
	if r.Code != 405 {
		t.Fatalf("unexpected write route: %d", r.Code)
	}
}
