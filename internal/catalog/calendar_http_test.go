package catalog

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
)

func TestCalendarHTTPRoutes(t *testing.T) {
	s := testStore(t)
	if _, err := s.write.Exec("INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES('/archive/2020/2020-01/2020-01-02/A.JPG',1577966400,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{"GET", "/api/year", "", 200},
		{"GET", "/api/today/01-02", "", 200},
		{"GET", "/api/today/02-30", "", 400},
		{"POST", "/api/day-progress", `{"day":"2020-01-02","status":"done","requestId":"progress-http-1"}`, 200},
		{"POST", "/api/day-progress", `{"day":"2020-01-02","status":"bogus","requestId":"progress-http-2"}`, 400},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		if tc.method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s %s: got %d body %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
