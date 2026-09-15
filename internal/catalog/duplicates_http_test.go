package catalog

import (
	"net/http/httptest"
	"testing"
)

func TestExactDuplicateHTTPRoute(t *testing.T) {
	s := testStore(t)
	request := httptest.NewRequest("GET", "/api/duplicates?md=01-02", nil)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	badRequest := httptest.NewRequest("GET", "/api/duplicates?md=02-30", nil)
	badResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(badResponse, badRequest)
	if badResponse.Code != 400 {
		t.Fatalf("invalid date returned %d", badResponse.Code)
	}
}
