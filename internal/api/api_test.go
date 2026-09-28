package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	ctx := context.Background()
	for _, p := range Permissions {
		if !Allowed(ctx, p.Name) {
			t.Errorf("Cull's own pages were refused %s", p.Name)
		}
	}
	asked := WithCaller(ctx, "photo-frame", []string{Review, Bin})
	if Caller(asked) != "photo-frame" {
		t.Fatalf("caller %q", Caller(asked))
	}
	for permission, want := range map[string]bool{Read: true, Review: true, Bin: true, Delete: false, Settings: false} {
		if got := Allowed(asked, permission); got != want {
			t.Errorf("an addon that asked for review and bin: Allowed(%s) = %v", permission, got)
		}
	}
	reader := WithCaller(ctx, "viewer", nil)
	if !Allowed(reader, Read) || Allowed(reader, Review) {
		t.Error("an addon that asked for nothing may only read")
	}
}

func testMux() *Mux {
	m := NewMux(NewBook())
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }
	m.HandleFunc(Route{Method: "GET", Path: "/api/things", Tag: "Test", Needs: Read, Summary: "List things"}, ok)
	m.HandleFunc(Route{Method: "POST", Path: "/api/things/{id}", Tag: "Test", Needs: Review, Summary: "Change a thing",
		Params: []Param{PathInt("id", "The thing's id.", "1")}}, ok)
	return m
}

func serve(m *Mux, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func jsonError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return body["error"]
}

func TestMuxAnswersUnknownRoutesInJSON(t *testing.T) {
	m := testMux()
	if w := serve(m, "GET", "/api/things"); w.Code != 204 || w.Header().Get(VersionHeader) != Version {
		t.Fatalf("a described route: %d, version %q", w.Code, w.Header().Get(VersionHeader))
	}
	w := serve(m, "GET", "/api/nothing")
	if w.Code != 404 || w.Header().Get(VersionHeader) != Version {
		t.Fatalf("an unknown route: %d", w.Code)
	}
	if message := jsonError(t, w); !strings.Contains(message, "/api/nothing") {
		t.Fatalf("message %q", message)
	}
	for _, tc := range []struct{ method, path, allow string }{
		{"POST", "/api/things", "GET"},
		{"GET", "/api/things/7", "POST"},
	} {
		w := serve(m, tc.method, tc.path)
		if w.Code != 405 || w.Header().Get("Allow") != tc.allow {
			t.Fatalf("%s %s: %d, Allow %q", tc.method, tc.path, w.Code, w.Header().Get("Allow"))
		}
		jsonError(t, w)
	}
	if w := serve(m, "DELETE", "/api/nothing"); w.Code != 404 {
		t.Fatalf("an unknown method on an unknown route: %d", w.Code)
	}
}

func TestRouteMustDescribeItself(t *testing.T) {
	for name, route := range map[string]Route{
		"no summary":            {Method: "GET", Path: "/api/a", Tag: "Test", Needs: Read},
		"summary with a stop":   {Method: "GET", Path: "/api/a", Tag: "Test", Needs: Read, Summary: "Get a."},
		"GET that writes":       {Method: "GET", Path: "/api/a", Tag: "Test", Needs: Bin, Summary: "Get a"},
		"write that only reads": {Method: "POST", Path: "/api/a", Tag: "Test", Needs: Read, Summary: "Change a"},
		"undescribed parameter": {Method: "GET", Path: "/api/a/{id}", Tag: "Test", Needs: Read, Summary: "Get a", Params: []Param{{Name: "id", In: "path", Type: "string"}}},
		"missing parameter":     {Method: "GET", Path: "/api/a/{id}", Tag: "Test", Needs: Read, Summary: "Get a"},
	} {
		if err := route.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestConformsRefusesNullForAList(t *testing.T) {
	type item struct {
		Names []string `json:"names"`
	}
	b := NewBook()
	route := Route{Method: "GET", Path: "/api/items", Tag: "Test", Needs: Read, Summary: "List items", Returns: item{}}
	b.add(route)
	document := b.OpenAPI(Info{Title: "Test", Version: Version})
	if err := Conforms(document, route, []byte(`{"names":["a"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := Conforms(document, route, []byte(`{"names":null}`)); err == nil {
		t.Fatal("null was accepted for a list")
	}
	if err := Conforms(document, route, []byte(`{"names":[],"extra":1}`)); err == nil {
		t.Fatal("an undocumented field was accepted")
	}
}
