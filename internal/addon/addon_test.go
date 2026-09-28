package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"daddy-cull/next/internal/api"
	"daddy-cull/next/internal/apidoc"
)

// memoryStore keeps addon choices in memory.
type memoryStore struct {
	mu      sync.Mutex
	choices map[string]bool
}

func newMemoryStore() *memoryStore { return &memoryStore{choices: map[string]bool{}} }

func (m *memoryStore) AddonChoice(ctx context.Context, id string) (on, chosen bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	on, chosen = m.choices[id]
	return on, chosen, nil
}

func (m *memoryStore) SetAddonChoice(ctx context.Context, id string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.choices[id] = on
	return nil
}

func builtIn(id string, on bool) BuiltIn {
	return BuiltIn{
		Manifest: Manifest{ID: id, Name: "Test " + id, Version: "1", Summary: "A built-in addon for tests."},
		Default:  func(context.Context) bool { return on },
	}
}

// writeManifest makes an addon of your own's folder in dir holding raw as
// its addon.json.
func writeManifest(t *testing.T, dir, folder, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, folder, ManifestFile), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validManifestJSON = `{
	"id": "photo-frame",
	"name": "Photo frame",
	"version": "0.1.0",
	"summary": "Shows kept photos on a frame.",
	"permissions": ["review"],
	"pages": [{"id": "frame", "label": "Frame", "section": "tools", "url": "http://localhost:9000/frame"}]
}`

func view(t *testing.T, r *Registry, id string) View {
	t.Helper()
	for _, v := range r.List(context.Background()) {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("%s is not listed", id)
	return View{}
}

func TestAPIDocsAreGenerated(t *testing.T) {
	want, err := apidoc.Generate(".", "daddy-cull/next/internal/addon")
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

func TestBuiltInDefaultAppliesUntilChosen(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	r, err := NewRegistry(store, api.NewBook(), "", builtIn("shown", true), builtIn("hidden", false))
	if err != nil {
		t.Fatal(err)
	}
	if !r.On(ctx, "shown") || r.On(ctx, "hidden") {
		t.Fatal("an addon nobody chose for should follow its default")
	}
	if v := view(t, r, "shown"); !v.BuiltIn || !v.On || v.Chosen || v.Status.State != Ready {
		t.Fatalf("default view %+v", v)
	}
	changed := r.Changed()
	if err := r.Set(ctx, "shown", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("Changed was not closed when an addon was turned off")
	}
	if r.On(ctx, "shown") {
		t.Fatal("turned off, but still on")
	}
	if v := view(t, r, "shown"); v.On || !v.Chosen {
		t.Fatalf("chosen view %+v", v)
	}
	if on, chosen, _ := store.AddonChoice(ctx, "shown"); on || !chosen {
		t.Fatal("the choice was not saved")
	}
	// A registry started again reads the saved choice over the default.
	again, err := NewRegistry(store, api.NewBook(), "", builtIn("shown", true), builtIn("hidden", false))
	if err != nil {
		t.Fatal(err)
	}
	if again.On(ctx, "shown") {
		t.Fatal("the choice did not outlast the registry")
	}
	if err := again.Set(ctx, "hidden", true); err != nil {
		t.Fatal(err)
	}
	if !again.On(ctx, "hidden") {
		t.Fatal("turned on, but still off")
	}
	if err := again.Set(ctx, "nobody", true); !errors.Is(err, ErrUnknown) {
		t.Fatalf("an unknown addon: %v", err)
	}
}

func TestExternalAddonIsListedAndGivenAKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeManifest(t, dir, "photo-frame", validManifestJSON)
	r, err := NewRegistry(newMemoryStore(), api.NewBook(), dir)
	if err != nil {
		t.Fatal(err)
	}
	v := view(t, r, "photo-frame")
	if v.BuiltIn || v.On || v.Problem != "" || v.Folder != "photo-frame" || v.Status.State != Setup {
		t.Fatalf("before turning on %+v", v)
	}
	if len(v.Pages) != 1 || v.Pages[0].Path != "/addons/photo-frame/frame" {
		t.Fatalf("pages %+v", v.Pages)
	}
	keyPath := filepath.Join(dir, "photo-frame", KeyFile)
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("a key was written before the addon was turned on")
	}
	if err := r.Set(ctx, "photo-frame", true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the key is readable by others: %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(keyPath)
	key := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(key, "cull_") || len(key) < 32 {
		t.Fatalf("key %q", key)
	}
	if v := view(t, r, "photo-frame"); !v.On || v.Status.State != Ready {
		t.Fatalf("after turning on %+v", v)
	}
	// Turning it off and on again keeps the key it was given.
	if err := r.Set(ctx, "photo-frame", false); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, "photo-frame", true); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(keyPath); string(again) != string(raw) {
		t.Fatal("the key changed")
	}
}

func TestBrokenExternalAddonCannotBeTurnedOn(t *testing.T) {
	for _, tc := range []struct {
		name, folder, manifest, problem string
	}{
		{"unknown field", "photo-frame", strings.Replace(validManifestJSON, `"version"`, `"colour": "blue", "version"`, 1), "unknown field"},
		{"id is not its folder", "frame", validManifestJSON, `its folder is "frame"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, tc.folder, tc.manifest)
			r, err := NewRegistry(newMemoryStore(), api.NewBook(), dir)
			if err != nil {
				t.Fatal(err)
			}
			v := view(t, r, tc.folder)
			if !strings.Contains(v.Problem, tc.problem) || v.Status.State != Problem || v.On {
				t.Fatalf("view %+v", v)
			}
			if err := r.Set(context.Background(), tc.folder, true); !errors.Is(err, ErrBroken) {
				t.Fatalf("turned on: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, tc.folder, KeyFile)); !os.IsNotExist(err) {
				t.Fatal("a broken addon was given a key")
			}
		})
	}
}

// guarded is a mux behind r's guard with a route of each kind, which answer
// with what the request was allowed to do.
func guarded(t *testing.T, r *Registry, book *api.Book) *api.Mux {
	t.Helper()
	m := api.NewMux(book)
	m.Guard(r.Guard)
	answer := func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		writeJSON(w, map[string]any{
			"caller": api.Caller(ctx),
			"review": api.Allowed(ctx, api.Review),
			"delete": api.Allowed(ctx, api.Delete),
		})
	}
	m.HandleFunc(api.Route{Method: "GET", Path: "/api/things", Tag: "Test", Needs: api.Read, Summary: "Read", Returns: map[string]any{}}, answer)
	m.HandleFunc(api.Route{Method: "POST", Path: "/api/things/review", Tag: "Test", Needs: api.Review, Summary: "Review", Returns: map[string]any{}}, answer)
	m.HandleFunc(api.Route{Method: "POST", Path: "/api/things/delete", Tag: "Test", Needs: api.Delete, Summary: "Delete", Returns: map[string]any{}}, answer)
	m.HandleFunc(api.Route{Method: "GET", Path: "/api/shown", Addon: "shown", Tag: "Test", Needs: api.Read, Summary: "An addon's route", Returns: map[string]any{}}, answer)
	return m
}

func call(m *api.Mux, method, path, key string) (int, map[string]any) {
	req := httptest.NewRequest(method, path, nil)
	if key != "" {
		req.Header.Set("Authorization", key)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestGuard(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeManifest(t, dir, "photo-frame", validManifestJSON)
	book := api.NewBook()
	r, err := NewRegistry(newMemoryStore(), book, dir, builtIn("shown", true))
	if err != nil {
		t.Fatal(err)
	}
	m := guarded(t, r, book)
	if err := r.Set(ctx, "photo-frame", true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "photo-frame", KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	key := "Bearer " + strings.TrimSpace(string(raw))

	// Cull's own pages send no key and may do anything.
	if status, body := call(m, "POST", "/api/things/delete", ""); status != 200 || body["caller"] != "" || body["review"] != true || body["delete"] != true {
		t.Fatalf("no key: %d %v", status, body)
	}
	for _, bad := range []string{"Bearer cull_" + strings.Repeat("0", 64), "Basic dXNlcjpwYXNz", "Bearer "} {
		if status, body := call(m, "GET", "/api/things", bad); status != 401 || body["error"] == nil {
			t.Fatalf("%q: %d %v", bad, status, body)
		}
	}
	// The addon may read and do what it asked for, and nothing more.
	if status, body := call(m, "GET", "/api/things", key); status != 200 || body["caller"] != "photo-frame" {
		t.Fatalf("read: %d %v", status, body)
	}
	if status, body := call(m, "POST", "/api/things/review", key); status != 200 || body["review"] != true || body["delete"] != false {
		t.Fatalf("review: %d %v", status, body)
	}
	if status, body := call(m, "POST", "/api/things/delete", key); status != 403 || !strings.Contains(body["error"].(string), "delete permission") {
		t.Fatalf("delete: %d %v", status, body)
	}
	// Turned off, its key is refused everywhere.
	if err := r.Set(ctx, "photo-frame", false); err != nil {
		t.Fatal(err)
	}
	if status, body := call(m, "GET", "/api/things", key); status != 403 || !strings.Contains(body["error"].(string), "turned off") {
		t.Fatalf("off: %d %v", status, body)
	}
	// An addon's route answers only while that addon is on.
	if status, _ := call(m, "GET", "/api/shown", ""); status != 200 {
		t.Fatalf("addon route while on: %d", status)
	}
	if err := r.Set(ctx, "shown", false); err != nil {
		t.Fatal(err)
	}
	if status, body := call(m, "GET", "/api/shown", ""); status != 404 || body["error"] == nil {
		t.Fatalf("addon route while off: %d %v", status, body)
	}
}

func TestWhileFollowsTheAddon(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := NewRegistry(newMemoryStore(), api.NewBook(), "", builtIn("worker", false))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 4)
	stopped := make(chan struct{}, 4)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		r.While(ctx, "worker", func(work context.Context) {
			started <- struct{}{}
			<-work.Done()
			stopped <- struct{}{}
		})
	}()
	// A change is seen at once, even one made just before While starts
	// waiting: it never has to wait out cacheFor.
	expect := func(c chan struct{}, what string) {
		t.Helper()
		select {
		case <-c:
		case <-time.After(time.Second):
			t.Fatalf("the work was not %s", what)
		}
	}
	select {
	case <-started:
		t.Fatal("the work started while the addon was off")
	case <-time.After(50 * time.Millisecond):
	}
	for range 2 {
		if err := r.Set(ctx, "worker", true); err != nil {
			t.Fatal(err)
		}
		expect(started, "started when the addon was turned on")
		if err := r.Set(ctx, "worker", false); err != nil {
			t.Fatal(err)
		}
		expect(stopped, "stopped when the addon was turned off")
	}
	if err := r.Set(ctx, "worker", true); err != nil {
		t.Fatal(err)
	}
	expect(started, "started again")
	cancel()
	expect(stopped, "stopped when its context ended")
	expect(returned, "left behind when its context ended")
}

func TestWatchTellsOfTheAddonsFolderChanging(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	r, err := NewRegistry(newMemoryStore(), api.NewBook(), dir)
	if err != nil {
		t.Fatal(err)
	}
	go r.Watch(ctx)
	waitFor := func(what string, changed <-chan struct{}) {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			t.Fatalf("no change was told when %s", what)
		}
	}
	// The folder as it is at the start is not news.
	changed := r.Changed()
	select {
	case <-changed:
		t.Fatal("reading the folder for the first time was told as a change")
	case <-time.After(1500 * time.Millisecond):
	}
	writeManifest(t, dir, "photo-frame", validManifestJSON)
	waitFor("an addon was put in", changed)
	if v := view(t, r, "photo-frame"); v.Problem != "" {
		t.Fatalf("the new addon %+v", v)
	}
	// Turning it on is told once; the key it is given is not told again.
	changed = r.Changed()
	if err := r.Set(ctx, "photo-frame", true); err != nil {
		t.Fatal(err)
	}
	waitFor("it was turned on", changed)
	changed = r.Changed()
	select {
	case <-changed:
		t.Fatal("the key written when it was turned on was told as a change")
	case <-time.After(1500 * time.Millisecond):
	}
	writeManifest(t, dir, "photo-frame", strings.Replace(validManifestJSON, "Photo frame", "Picture frame", 1))
	waitFor("its manifest was edited", changed)
	if v := view(t, r, "photo-frame"); v.Name != "Picture frame" {
		t.Fatalf("the edit was not read: %+v", v)
	}
	changed = r.Changed()
	if err := os.RemoveAll(filepath.Join(dir, "photo-frame")); err != nil {
		t.Fatal(err)
	}
	waitFor("it was taken out", changed)
	for _, v := range r.List(ctx) {
		if v.ID == "photo-frame" {
			t.Fatal("an addon taken out is still listed")
		}
	}
}

func TestTurningAnAddonOffEndsItsOpenRequests(t *testing.T) {
	ctx := context.Background()
	for _, how := range []string{"turned off", "key taken away"} {
		t.Run(how, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, "photo-frame", validManifestJSON)
			book := api.NewBook()
			r, err := NewRegistry(newMemoryStore(), book, dir)
			if err != nil {
				t.Fatal(err)
			}
			m := api.NewMux(book)
			m.Guard(r.Guard)
			started := make(chan struct{})
			// A route that runs until its request ends, as the event stream does.
			m.HandleFunc(api.Route{Method: "GET", Path: "/api/stream", Tag: "Test", Needs: api.Read, Summary: "Stream", Returns: map[string]any{}}, func(w http.ResponseWriter, req *http.Request) {
				close(started)
				<-req.Context().Done()
			})
			if err := r.Set(ctx, "photo-frame", true); err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(dir, "photo-frame", KeyFile)
			raw, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				req := httptest.NewRequest("GET", "/api/stream", nil)
				req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
				m.ServeHTTP(httptest.NewRecorder(), req)
				close(done)
			}()
			<-started
			select {
			case <-done:
				t.Fatal("the request ended while the addon was on")
			case <-time.After(200 * time.Millisecond):
			}
			if how == "turned off" {
				if err := r.Set(ctx, "photo-frame", false); err != nil {
					t.Fatal(err)
				}
			} else {
				go r.Watch(t.Context())
				if err := os.Remove(keyPath); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("the request carried on after the addon's %s", how)
			}
		})
	}
}
