package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealImportAndMediaBoundary(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "real.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	input := `{"path":"/archive/2020/A.jpg","source":"archive","capturedAt":1,"kind":"image","size":10,"favourite":true}`
	if e = s.ImportSnapshot(context.Background(), strings.NewReader(input)); e != nil {
		t.Fatal(e)
	}
	if e = s.ImportSnapshot(context.Background(), strings.NewReader(input)); e == nil {
		t.Fatal("reimport must refuse existing decisions")
	}
	p, e := s.ReviewPage(context.Background(), "", "", 40)
	if e != nil || len(p.Assets) != 1 || !p.Assets[0].Favourite {
		t.Fatalf("import failed %+v %v", p, e)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/thumb" || r.URL.Query().Get("p") != "/archive/2020/A.jpg" {
			t.Error("unexpected media route")
		}
		w.Header().Set("Content-Type", "image/jpeg")
		fmt.Fprint(w, "preview")
	}))
	defer upstream.Close()
	mux := http.NewServeMux()
	mux.Handle("/api/media/{id}/{mode}", s.MediaHandler(upstream.URL))
	for _, v := range []struct {
		path, method string
		status       int
	}{{"/api/media/1/preview", "GET", 200}, {"/api/media/999/preview", "GET", 404}, {"/api/media/1/purge", "GET", 404}, {"/api/media/1/preview", "POST", 405}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(v.method, v.path, nil))
		if w.Code != v.status {
			t.Fatalf("%s got %d", v.path, w.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected upstream calls %d", calls)
	}
}
func TestImportRejectsTraversalAtomically(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "real.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	input := `{"path":"/archive/../outside.jpg","source":"archive","capturedAt":1,"kind":"image","size":10}`
	if e = s.ImportSnapshot(context.Background(), strings.NewReader(input)); e == nil {
		t.Fatal("accepted traversal")
	}
	n, _ := s.Count(context.Background())
	if n != 0 {
		t.Fatal("partial import")
	}
}
