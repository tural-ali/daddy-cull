package catalog

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourcesAndNoDestructionRoutes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if e := s.Seed(ctx, 100); e != nil {
		t.Fatal(e)
	}
	p, e := s.Page(ctx, "", "video", "takeout", 200)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Assets) != 5 {
		t.Fatalf("got %d Takeout videos", len(p.Assets))
	}
	for _, a := range p.Assets {
		if a.Source != "takeout" || a.Kind != "video" {
			t.Fatal("filter leaked")
		}
	}
	for _, path := range []string{"/api/purge", "/api/delete", "/api/cull", "/api/import", "/api/source-cleanup"} {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, httptest.NewRequest("POST", path, nil))
		if r.Code != 404 {
			t.Fatalf("unexpected destructive route %s: %d", path, r.Code)
		}
	}
}

func TestDecisionOriginAndPayloadGuards(t *testing.T) {
	s := testStore(t)
	if e := s.Seed(context.Background(), 1); e != nil {
		t.Fatal(e)
	}
	body := `{"requestId":"http-test","assetId":1,"status":"keep","expectedRevision":0,"favourite":false}`
	for _, tc := range []struct {
		origin, content, body string
		status                int
	}{
		{"https://another.example", "application/json", body, 403},
		{"", "text/plain", body, 415},
		{"", "application/json", body + body, 400},
		{"", "application/json", `{"unknown":1}`, 400},
		{"http://example.com", "application/json", body, 200},
	} {
		r := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "http://example.com/api/decisions", strings.NewReader(tc.body))
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", tc.content)
		s.Handler().ServeHTTP(r, req)
		if r.Code != tc.status {
			t.Fatalf("origin=%s got %d want %d", tc.origin, r.Code, tc.status)
		}
	}
}

func TestTimelineUsesSeekIndex(t *testing.T) {
	s := testStore(t)
	rows, e := s.read.Query("EXPLAIN QUERY PLAN SELECT id FROM assets WHERE source_id=? AND kind=? AND (captured_at,id)>(?,?) ORDER BY captured_at,id LIMIT 80", "takeout", "video", 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if e = rows.Scan(&a, &b, &c, &detail); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(detail, "assets_source_kind_timeline") && strings.Contains(detail, "SEARCH") {
			found = true
		}
		if strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal("query sorts entire result")
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if !found {
		t.Fatal("missing indexed seek")
	}
}
