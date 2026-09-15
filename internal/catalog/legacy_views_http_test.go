package catalog

import (
	"net/http/httptest"
	"testing"
)

func TestLegacyViewHTTPRoutes(t *testing.T) {
	s := testStore(t)
	for _, route := range []string{"/api/shadows", "/api/legacy-bin"} {
		request := httptest.NewRequest("GET", route, nil)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 200 || response.Header().Get("Content-Type") != "application/json" || response.Body.String() != "[]\n" {
			t.Fatalf("%s returned %d %q", route, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest("GET", "/api/screenshots", nil)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != "{\"items\":[],\"total\":0,\"bytes\":0}\n" {
		t.Fatalf("screenshots returned %d %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest("GET", "/api/screenshot-bin", nil)
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != "[]\n" {
		t.Fatalf("screenshot Bin returned %d %q", response.Code, response.Body.String())
	}
}
