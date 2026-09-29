package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"daddy-cull/next/internal/api"
)

func setupFixture(t *testing.T) (*Setup, string, *atomic.Int32) {
	t.Helper()
	state := t.TempDir()
	file := filepath.Join(state, "config.json")
	home := t.TempDir()
	config := SetupConfig{Library: filepath.Join(home, "Library"), Import: filepath.Join(home, "Import")}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	restarts := &atomic.Int32{}
	setup := NewSetup(file, SetupConfig{}, func() { restarts.Add(1) })
	setup.look = func(name string) (string, error) {
		if name == "exiftool" {
			return "/opt/homebrew/bin/exiftool", nil
		}
		return "", errors.New("not found")
	}
	return setup, home, restarts
}

func TestSetupChecksTheFolders(t *testing.T) {
	good := SetupConfig{Library: "/Users/sam/Pictures/Library", Import: "/Users/sam/Pictures/Import"}
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"sam@example.com", "sam.o'neil+photos@mail.example.co.uk", "+44 7700 900123", "(555) 010-9999"} {
		config := good
		config.ICloud = SetupICloud{On: true, AppleID: account}
		if err := config.Check(); err != nil {
			t.Errorf("%s: %v", account, err)
		}
	}
	for name, change := range map[string]func(*SetupConfig){
		"no library":        func(c *SetupConfig) { c.Library = "" },
		"a relative folder": func(c *SetupConfig) { c.Import = "Pictures/Import" },
		"import in library": func(c *SetupConfig) { c.Import = "/Users/sam/Pictures/Library/Import" },
		"library in import": func(c *SetupConfig) { c.Library = "/Users/sam/Pictures/Import/Library" },
		"the same folder":   func(c *SetupConfig) { c.Import = c.Library },
		"takeout in import": func(c *SetupConfig) { c.TakeoutInbox = "/Users/sam/Pictures/Import/Takeout" },
		"iCloud and no one": func(c *SetupConfig) { c.ICloud.On = true },
		"two accounts":      func(c *SetupConfig) { c.ICloud.AppleID = "sam@example.comsam@example.com" },
		"a shell command":   func(c *SetupConfig) { c.ICloud.AppleID = "sam@example.com; rm -rf ~" },
		"a day that is not": func(c *SetupConfig) { c.ICloud.Since = "14/08/2019" },
		"Immich, no scheme": func(c *SetupConfig) { c.Immich.URL = "immich.local:2283" },
		"Immich, no folder": func(c *SetupConfig) { c.Immich.URL = "http://immich.local:2283" },
	} {
		config := good
		change(&config)
		if err := config.Check(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSetupSavesAndRestarts(t *testing.T) {
	setup, home, restarts := setupFixture(t)
	handler := routeHandler(t, setup)
	change := `{"config":{"library":"` + filepath.Join(home, "Photos") + `","import":"` + filepath.Join(home, "Drop here") + `","shared":false,"icloud":{"on":true,"appleId":" someone@example.com ","since":"2024-01-01"},"immich":{"url":"","pathPrefix":""},"done":true},"immichKey":"abc123"}`
	request := httptest.NewRequest("POST", "/api/setup", strings.NewReader(change))
	request.Header.Set("Content-Type", "application/json")
	answer := httptest.NewRecorder()
	handler.ServeHTTP(answer, request)
	if answer.Code != http.StatusAccepted || !strings.Contains(answer.Body.String(), `"restarting":true`) {
		t.Fatalf("PUT: %d %s", answer.Code, answer.Body.String())
	}
	saved, err := ReadSetup(setup.file)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ICloud.AppleID != "someone@example.com" || !saved.Done || saved.Library != filepath.Join(home, "Photos") {
		t.Fatalf("saved %+v", saved)
	}
	for _, folder := range []string{saved.Library, saved.Import} {
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			t.Fatalf("%s was not made: %v", folder, err)
		}
	}
	for _, file := range []string{setup.file, filepath.Join(StateDir(setup.file), "secrets.env")} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v, want readable by its owner only", file, info.Mode(), err)
		}
	}
	if strings.Contains(answer.Body.String(), "abc123") {
		t.Fatal("the Immich key was shown")
	}
	deadline := time.Now().Add(2 * time.Second)
	for restarts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 1 {
		t.Fatalf("restarted %d times", restarts.Load())
	}
	view := setup.View()
	if !view.ImmichKeySet || !view.Configurable || view.Free <= 0 {
		t.Fatalf("view %+v", view)
	}
	if !view.Tools[0].Found || view.Tools[2].Found {
		t.Fatalf("tools %+v", view.Tools)
	}
}

func TestSetupRefusesWhatItCannotSave(t *testing.T) {
	setup, _, restarts := setupFixture(t)
	handler := routeHandler(t, setup)
	put := func(ctx context.Context, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/setup", strings.NewReader(body)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		answer := httptest.NewRecorder()
		handler.ServeHTTP(answer, request)
		return answer
	}
	if answer := put(context.Background(), `{"config":{"library":"/a","import":"/a/b"}}`); answer.Code != 400 || !strings.Contains(answer.Body.String(), "separate folders") {
		t.Fatalf("overlapping folders: %d %s", answer.Code, answer.Body.String())
	}
	addon := api.WithCaller(context.Background(), "someones-addon", []string{api.Settings})
	if answer := put(addon, `{"config":{"library":"/a","import":"/b"}}`); answer.Code != 403 {
		t.Fatalf("an addon: %d %s", answer.Code, answer.Body.String())
	}
	fixed := NewSetup("", SetupConfig{Library: "/archive"}, func() { t.Fatal("restarted") })
	if answer := httptest.NewRecorder(); true {
		routeHandler(t, fixed).ServeHTTP(answer, func() *http.Request {
			r := httptest.NewRequest("POST", "/api/setup", strings.NewReader(`{"config":{}}`))
			r.Header.Set("Content-Type", "application/json")
			return r
		}())
		if answer.Code != 409 {
			t.Fatalf("no config file: %d", answer.Code)
		}
	}
	if view := fixed.View(); view.Configurable || view.Config.Library != "/archive" {
		t.Fatalf("fixed view %+v", view)
	}
	time.Sleep(400 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("a refused change restarted Cull")
	}
}

func TestSetupReadsWhatTheDownloadsRecorded(t *testing.T) {
	setup, _, _ := setupFixture(t)
	status := `{"signedIn":true,"lastRun":"2026-09-28T06:00:00Z","ok":false,"message":"The session has ended. Sign in again."}`
	if err := os.WriteFile(filepath.Join(StateDir(setup.file), "icloud-status.json"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	view := setup.View()
	if !view.ICloud.SignedIn || view.ICloud.OK || !strings.Contains(view.ICloud.Message, "Sign in again") {
		t.Fatalf("iCloud %+v", view.ICloud)
	}
	if view.ApplePhotos.LastRun != "" {
		t.Fatalf("Apple Photos %+v", view.ApplePhotos)
	}
}

func TestWatchStopsWhenTheConfigChanges(t *testing.T) {
	setup, _, _ := setupFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go Watch(ctx, setup.file, func() { close(stopped) })
	time.Sleep(100 * time.Millisecond)
	later := time.Now().Add(time.Minute)
	if err := os.WriteFile(setup.file, []byte(`{"library":"/x","import":"/y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(setup.file, later, later); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("a changed config did not stop the process")
	}
}

// routeHandler serves the Setup routes.
func routeHandler(t *testing.T, setup *Setup) http.Handler {
	t.Helper()
	mux := api.NewMux(api.NewBook())
	setup.Routes(mux)
	return mux
}
