package catalog

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"daddy-cull/next/internal/addon"
	"daddy-cull/next/internal/api"
	"daddy-cull/next/internal/apidoc"
	"daddy-cull/next/mac"
)

const testWriterSecret = "test-writer-secret-0123456789abcdef"

// fullMux registers every route group the way cmd/cull does, with the Bin
// writer at upstream and photos as the Apple Photos hub, or a new one when
// it is nil.
func fullMux(t *testing.T, s *Store, upstream string, photos *PhotosHub) (*api.Mux, *addon.Registry) {
	t.Helper()
	book := api.NewBook()
	m := api.NewMux(book)
	m.Guard(api.SameOrigin)
	if photos == nil {
		photos = NewPhotosHub(s, "")
	}
	addons, err := addon.NewRegistry(s, book, t.TempDir(), s.BuiltInAddons(AddonNeeds{Photos: photos})...)
	if err != nil {
		t.Fatal(err)
	}
	m.Guard(addons.Guard)
	s.Routes(m)
	dir := t.TempDir()
	s.MediaRoutes(m, MediaRoots{Archive: dir, Screenshots: dir, Upgrades: dir, Disks: dir, Review: dir, Posters: dir, Cache: dir}, "", dir)
	s.WriterRoutes(m, upstream, testWriterSecret)
	photos.Routes(m)
	addons.Routes(m)
	s.EventRoutes(m, addons.Changed)
	s.NewTaskRunner(upstream, testWriterSecret).Routes(m)
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/openapi.json", Tag: "Reference", Needs: api.Read,
		Summary: "Get this reference", Doc: "This document.", Returns: map[string]any{},
	}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, m.Book().OpenAPI(api.Info{Title: "Cull", Version: api.Version}))
	})
	return m, addons
}

// binWorker serves b as the private Bin writer does.
func binWorker(t *testing.T, b *BinEngine) string {
	t.Helper()
	worker := httptest.NewServer(b.Handler(testWriterSecret))
	t.Cleanup(worker.Close)
	return worker.URL
}

// emptyBin serves a Bin writer for s that has moved nothing yet.
func emptyBin(t *testing.T, s *Store) string {
	t.Helper()
	b, err := NewBinEngine(s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return binWorker(t, b)
}

func TestAPIDocsAreGenerated(t *testing.T) {
	want, err := apidoc.Generate(".", "daddy-cull/next/internal/catalog")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(apidoc.OutputName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is out of date with the comments in the package; run go generate ./internal/catalog ./internal/addon", apidoc.OutputName)
	}
}

// TestEveryRouteDocumented reads the reference as a caller would and asks
// that nothing in it is left unexplained.
func TestEveryRouteDocumented(t *testing.T) {
	m, _ := fullMux(t, testStore(t), "", nil)
	document := m.Book().OpenAPI(api.Info{Title: "Cull", Version: api.Version})
	paths := document["paths"].(map[string]map[string]any)
	if len(paths) == 0 {
		t.Fatal("no routes were registered")
	}
	for path, operations := range paths {
		for method, operation := range operations {
			op := operation.(map[string]any)
			where := strings.ToUpper(method) + " " + path
			if s, _ := op["summary"].(string); s == "" {
				t.Errorf("%s has no summary", where)
			}
			if d, _ := op["description"].(string); d == "" {
				t.Errorf("%s has no description; give its Route a Doc", where)
			}
			params, _ := op["parameters"].([]any)
			for _, p := range params {
				param := p.(map[string]any)
				if d, _ := param["description"].(string); d == "" {
					t.Errorf("%s: parameter %v has no description", where, param["name"])
				}
			}
		}
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]api.Schema)
	for name, schema := range schemas {
		if d, _ := schema["description"].(string); d == "" {
			t.Errorf("schema %s has no description; comment its Go type", name)
		}
		properties, _ := schema["properties"].(map[string]any)
		for property, p := range properties {
			if d, _ := p.(api.Schema)["description"].(string); d == "" {
				t.Errorf("%s.%s has no description; comment its Go field", name, property)
			}
		}
	}
}

// checkGETs calls every JSON GET route and checks that each answer is what
// the reference says it returns, so a field added without its documentation,
// or a list sent as null, is caught. paths gives the address to call for a
// route whose path parameters need more than their example. It returns how
// many routes it checked.
func checkGETs(t *testing.T, s *Store, upstream string, photos *PhotosHub, paths map[string]string) int {
	t.Helper()
	ctx := context.Background()
	m, addons := fullMux(t, s, upstream, photos)
	for _, view := range addons.List(ctx) {
		if view.BuiltIn {
			if err := addons.Set(ctx, view.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A move that finished with one file it could not handle, so a task and
	// its failures are both in the answer.
	if _, err := s.write.ExecContext(ctx, `INSERT INTO tasks(id,kind,label,state,created_at,started_at,finished_at) VALUES('0123456789abcdef0123456789abcdef','screenshots.remove','Move 2 screenshots to the Bin','failed','2026-09-28T09:00:00Z','2026-09-28T09:00:01Z','2026-09-28T09:00:02Z');
		INSERT INTO task_items(task_id,seq,chunk,asset_id,name,size,state,error) VALUES('0123456789abcdef0123456789abcdef',0,0,1,'IMG_0001.PNG',10,'done',''),('0123456789abcdef0123456789abcdef',1,1,2,'IMG_0002.PNG',10,'failed','The file is no longer where the catalogue says.')`); err != nil {
		t.Fatal(err)
	}
	document := m.Book().OpenAPI(api.Info{Title: "Cull", Version: api.Version})
	// The test libraries' files were all taken on 1 January.
	concrete := map[string]string{"/api/today/{md}": "/api/today/01-01"}
	for route, path := range paths {
		concrete[route] = path
	}
	// Routes that need something a test library cannot give them.
	unsatisfiable := map[string]string{
		"/api/photos/jobs/{job}": "needs a sync job from the Mac",
		"/api/photos/setup/{id}": "needs a setup code from the Mac",
	}
	checked := 0
	for _, route := range m.Book().Routes() {
		// Media and the event stream are not JSON. The helper's own routes
		// need its key and are held open until there is work.
		if route.Method != "GET" || route.Produces != "" || route.Internal {
			continue
		}
		if why, skip := unsatisfiable[route.Path]; skip && concrete[route.Path] == "" {
			t.Logf("%s skipped: %s", route.Pattern(), why)
			continue
		}
		target := route.Path
		if c, ok := concrete[route.Path]; ok {
			target = c
		} else {
			for _, p := range route.Params {
				if p.In == "path" {
					target = strings.Replace(target, "{"+p.Name+"}", p.Example, 1)
				}
			}
		}
		t.Run(route.Path, func(t *testing.T) {
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
			if w.Code != 200 {
				t.Fatalf("GET %s answered %d: %s", target, w.Code, w.Body.String())
			}
			if err := api.Conforms(document, route, w.Body.Bytes()); err != nil {
				t.Errorf("GET %s does not match the reference: %v\n%s", target, err, w.Body.String())
			}
		})
		checked++
	}
	return checked
}

func TestGETResponsesConform(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Seed(ctx, 40); err != nil {
		t.Fatal(err)
	}
	for _, d := range []Decision{
		{RequestID: "conform-keep-1", AssetID: 1, Status: "keep", Favourite: true},
		{RequestID: "conform-cull-2", AssetID: 2, Status: "cull"},
		{RequestID: "conform-later-3", AssetID: 3, Status: "later"},
	} {
		if _, err := s.Decide(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if checked := checkGETs(t, s, emptyBin(t, s), nil, nil); checked < 20 {
		t.Fatalf("only %d routes were checked", checked)
	}
}

// TestGETResponsesConformWithBin checks the same routes with a file moved
// into the Bin, so the Bin's plans, cards and the Log's places are filled.
func TestGETResponsesConformWithBin(t *testing.T) {
	b, s, _ := binFixture(t)
	ctx := context.Background()
	plan, err := b.Preview(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Run(ctx, plan.ID, "quarantine", ""); err != nil {
		t.Fatal(err)
	}
	if checked := checkGETs(t, s, binWorker(t, b), nil, nil); checked < 20 {
		t.Fatalf("only %d routes were checked", checked)
	}
}

// TestPhotosGETResponsesConform checks the Apple Photos routes with a check
// that found matches and a setup code waiting, which fill most of their
// fields.
func TestPhotosGETResponsesConform(t *testing.T) {
	h, s, _ := photosHubFixture(t)
	h.SetHelper(mac.CullSync)
	job := checkAndMatch(t, h, "IMG_1001.HEIC")
	setup, err := h.NewSetup(fakeBase)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"/api/photos/jobs/{job}": "/api/photos/jobs/" + job.ID,
		"/api/photos/setup/{id}": "/api/photos/setup/" + setup.ID,
	}
	if checked := checkGETs(t, s, emptyBin(t, s), h, paths); checked < 20 {
		t.Fatalf("only %d routes were checked", checked)
	}
}
