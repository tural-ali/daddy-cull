package catalog

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"daddy-cull/next/internal/api"
)

// takenAt is noon UTC on 14 August 2019, which is the 14th in any zone the
// tests run in.
var takenAt = time.Date(2019, 8, 14, 12, 0, 0, 0, time.UTC)

func sidecarFor(title string, taken time.Time) string {
	return fmt.Sprintf(`{"title":%q,"description":"","photoTakenTime":{"timestamp":"%d"},"people":[{"name":"Test Person"}],"favorited":true}`, title, taken.Unix())
}

func writeZip(t *testing.T, file string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range entries {
		e, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type googleFixture struct {
	s       *Store
	g       *GooglePhotos
	inbox   string
	archive string
	ids     map[string]int64
}

// newGoogleFixture is a library of a few photos from 14 August 2019 and an
// export holding one photo of each kind Cull tells apart. Every file is
// made up.
func newGoogleFixture(t *testing.T) *googleFixture {
	t.Helper()
	s := testStore(t)
	inbox, archive := t.TempDir(), t.TempDir()
	day := "2019/2019-08/2019-08-14/"
	captured := time.Date(2019, 8, 14, 12, 0, 0, 0, time.UTC).Unix()
	library := []struct {
		id         int64
		rel, body  string
		catalogued bool
	}{
		{1, day + "IMG_0001.JPG", "photo one bytes", true},
		{2, day + "IMG_0002.JPG", "photo two bytes", true},
		{3, day + "IMG_0003.JPG", "the library's larger copy of three", true},
		{4, day + "PXL_20190814_120000.jpg", "renamed by google", true},
	}
	for _, f := range library {
		writeFile(t, archive, f.rel, f.body)
		if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,?,?,'archive')", f.id, "/archive/"+f.rel, captured, "image", len(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.Exec("INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(2,'cull',0,1)"); err != nil {
		t.Fatal(err)
	}
	// A file nobody catalogued yet already holds photo five's bytes.
	writeFile(t, archive, "2019/2019-08/2019-08-15/copy of five.jpg", "photo five bytes")
	album := "Takeout/Google Photos/Photos from 2019/"
	writeZip(t, filepath.Join(inbox, "takeout-001.zip"), map[string]string{
		album + "IMG_0001.JPG":          "photo one bytes",
		album + "IMG_0001.JPG.json":     sidecarFor("IMG_0001.JPG", takenAt),
		album + "IMG_0001.MOV":          "live photo motion",
		album + "IMG_0002.JPG":          "photo two bytes",
		album + "IMG_0002.JPG.json":     sidecarFor("IMG_0002.JPG", takenAt),
		album + "IMG_0003.JPG":          "google's copy of three",
		album + "IMG_0003.JPG.json":     sidecarFor("IMG_0003.JPG", takenAt),
		album + "IMG_0004.JPG":          "photo four bytes",
		album + "IMG_0004.JPG.json":     sidecarFor("IMG_0004.JPG", takenAt),
		album + "IMG_0005.JPG":          "photo five bytes",
		album + "IMG_0005.JPG.json":     sidecarFor("IMG_0005.JPG", takenAt.AddDate(0, 0, 1)),
		album + "renamed.jpg":           "renamed by google",
		album + "renamed.jpg.json":      sidecarFor("renamed.jpg", takenAt),
		album + "undated.png":           "no date anywhere",
		"Takeout/Drive/not a photo.jpg": "a picture kept in Drive",
	})
	g, err := s.NewGooglePhotos(inbox, archive, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	if err := g.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &googleFixture{s: s, g: g, inbox: inbox, archive: archive, ids: map[string]int64{}}
	rows, err := s.read.Query("SELECT id,name FROM takeout_items")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		f.ids[name] = id
	}
	return f
}

func (f *googleFixture) outcome(t *testing.T, name string) (string, string) {
	t.Helper()
	var outcome, state string
	if err := f.s.read.QueryRow("SELECT outcome,state FROM takeout_items WHERE name=?", name).Scan(&outcome, &state); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return outcome, state
}

func TestGooglePhotosTellsEachPhotoApart(t *testing.T) {
	f := newGoogleFixture(t)
	want := map[string]string{
		"IMG_0001.JPG": TakeoutRepresented,
		"IMG_0001.MOV": TakeoutRepresented,
		"IMG_0002.JPG": TakeoutRemoved,
		"IMG_0003.JPG": TakeoutAlternative,
		"IMG_0004.JPG": TakeoutMissing,
		"IMG_0005.JPG": TakeoutMissing,
		"renamed.jpg":  TakeoutRepresented,
		"undated.png":  TakeoutUncertain,
	}
	if len(f.ids) != len(want) {
		t.Fatalf("items %v, want only the Google Photos files", f.ids)
	}
	for name, outcome := range want {
		if got, _ := f.outcome(t, name); got != outcome {
			t.Errorf("%s is %s, want %s", name, got, outcome)
		}
	}
	page, err := f.g.Page(context.Background(), "missing", 0, 120)
	if err != nil {
		t.Fatal(err)
	}
	if page.Counts.Missing != 2 || page.Counts.Alternative != 1 || page.Counts.Represented != 3 || page.Counts.Removed != 1 || page.Counts.Uncertain != 1 {
		t.Fatalf("counts %+v", page.Counts)
	}
	if len(page.Items) != 2 || page.Items[0].Name != "IMG_0004.JPG" || page.Items[0].Taken == "" || page.Items[0].TakenFrom != "google" || !page.Items[0].Favourite || len(page.Items[0].People) != 1 {
		t.Fatalf("missing tab %+v", page.Items)
	}
	if len(page.Archives) != 1 || page.Archives[0].Media != 8 {
		t.Fatalf("archives %+v", page.Archives)
	}
	if _, err := f.g.Page(context.Background(), "everything", 0, 120); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown tab: %v", err)
	}
	// Removing a photo in Cull stops it being offered.
	if _, err := f.s.write.Exec("INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(1,'cull',0,1)"); err != nil {
		t.Fatal(err)
	}
	if err := f.g.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"IMG_0001.JPG", "IMG_0001.MOV"} {
		if got, _ := f.outcome(t, name); got != TakeoutRemoved {
			t.Errorf("%s is %s once its library copy is removed", name, got)
		}
	}
}

func TestGooglePhotosSkip(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	id := f.ids["IMG_0004.JPG"]
	if n, err := f.s.SkipGooglePhotos(ctx, GooglePhotosSkip{IDs: []int64{id}, Skip: true}); err != nil || n != 1 {
		t.Fatalf("skip: %d %v", n, err)
	}
	if _, state := f.outcome(t, "IMG_0004.JPG"); state != "skipped" {
		t.Fatalf("state %s", state)
	}
	if _, err := f.s.QueueGooglePhotos(ctx, GooglePhotosSelection{IDs: []int64{id}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a skipped photo was queued: %v", err)
	}
	if n, err := f.s.SkipGooglePhotos(ctx, GooglePhotosSkip{IDs: []int64{id}, Skip: false}); err != nil || n != 1 {
		t.Fatalf("unskip: %d %v", n, err)
	}
	if _, err := f.s.SkipGooglePhotos(ctx, GooglePhotosSkip{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty skip: %v", err)
	}
}

func TestGooglePhotosAddThroughTheWriter(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	secret := strings.Repeat("k", 32)
	writer, err := NewGooglePhotosWriter(f.s, f.inbox, f.archive)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	mux := http.NewServeMux()
	mux.Handle("/google-photos/", writer.Handler(secret))
	server := httptest.NewServer(mux)
	defer server.Close()
	runner := f.s.NewTaskRunner(server.URL, secret)
	finished := 0
	runner.OnFinished(TaskGooglePhotosAdd, func(context.Context) { finished++ })

	if _, err := f.s.QueueGooglePhotos(ctx, GooglePhotosSelection{IDs: []int64{f.ids["IMG_0001.JPG"]}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a photo the library holds was queued: %v", err)
	}
	ids := []int64{f.ids["IMG_0004.JPG"], f.ids["IMG_0003.JPG"], f.ids["IMG_0005.JPG"]}
	task, err := f.s.QueueGooglePhotos(ctx, GooglePhotosSelection{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if task.Label != "Add 3 photos from Google Photos to the library" || task.Total != 3 {
		t.Fatalf("task %+v", task)
	}
	if _, err := f.s.QueueGooglePhotos(ctx, GooglePhotosSelection{IDs: ids[:1]}); !errors.Is(err, ErrQueued) {
		t.Fatalf("queued twice: %v", err)
	}
	page, err := f.g.Page(ctx, "missing", 0, 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Counts.Missing != 0 {
		t.Fatalf("photos being added still listed: %+v", page.Items)
	}
	runner.run(ctx, task.ID)
	task, err = f.s.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != TaskFailed || task.Done != 2 || task.Failed != 1 || finished != 1 {
		t.Fatalf("task %+v, finished %d", task, finished)
	}
	if !strings.Contains(task.Failures[0].Error, "copy of five.jpg") {
		t.Fatalf("the duplicate was not refused by name: %+v", task.Failures)
	}
	day := filepath.Join(f.archive, "2019", "2019-08", "2019-08-14")
	for name, body := range map[string]string{"IMG_0004.JPG": "photo four bytes", "IMG_0003 (Google Photos).JPG": "google's copy of three", "IMG_0003.JPG": "the library's larger copy of three"} {
		got, err := os.ReadFile(filepath.Join(day, name))
		if err != nil || string(got) != body {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	info, err := os.Stat(filepath.Join(day, "IMG_0004.JPG"))
	if err != nil || !info.ModTime().Equal(takenAt) {
		t.Fatalf("not dated with when it was taken: %v %v", info.ModTime(), err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(day, ".daddy-cull-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
	if _, state := f.outcome(t, "IMG_0004.JPG"); state != "added" {
		t.Fatalf("state %s", state)
	}
	added, err := f.g.Page(ctx, "added", 0, 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Items) != 2 || added.Counts.Added != 2 {
		t.Fatalf("added tab %+v", added.Items)
	}
	// Asking again is refused rather than copying twice.
	if _, err := writer.Preview(ctx, f.ids["IMG_0004.JPG"]); err == nil {
		t.Fatal("a photo already added was planned again")
	}
	// The export is only read.
	if _, err := os.Stat(filepath.Join(f.inbox, "takeout-001.zip")); err != nil {
		t.Fatal(err)
	}
}

func TestGooglePhotosNewFoldersAreShared(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	if _, err := f.s.write.Exec("UPDATE takeout_items SET local_at=local_at+86400*365,taken_at=taken_at+86400*365 WHERE name='IMG_0004.JPG'"); err != nil {
		t.Fatal(err)
	}
	writer, err := NewGooglePhotosWriter(f.s, f.inbox, f.archive)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	plan, err := writer.Preview(ctx, f.ids["IMG_0004.JPG"])
	if err != nil {
		t.Fatal(err)
	}
	if plan.Destination != "2020/2020-08/2020-08-13/IMG_0004.JPG" {
		t.Fatalf("destination %s", plan.Destination)
	}
	if _, err := writer.Run(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"2020", "2020/2020-08", "2020/2020-08/2020-08-13"} {
		info, err := os.Stat(filepath.Join(f.archive, filepath.FromSlash(dir)))
		if err != nil || info.Mode().Perm() != 0o777 {
			t.Fatalf("%s: %v %v", dir, info.Mode(), err)
		}
	}
	// Running a finished plan again changes nothing.
	again, err := writer.Run(ctx, plan.ID)
	if err != nil || again.State != "added" {
		t.Fatalf("again: %+v %v", again, err)
	}
}

func TestGooglePhotosShowsPhotosInsideAZip(t *testing.T) {
	f := newGoogleFixture(t)
	book := api.NewBook()
	m := api.NewMux(book)
	f.g.Routes(m, f.s.NewTaskRunner("", ""), MediaRoots{Archive: f.archive, Cache: t.TempDir()})
	get := func(target string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		m.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		return response
	}
	id := f.ids["IMG_0004.JPG"]
	for i := 0; i < 2; i++ {
		response := get(fmt.Sprintf("/api/google-photos/media/%d/original", id))
		if response.Code != 200 || response.Body.String() != "photo four bytes" {
			t.Fatalf("original: %d %q", response.Code, response.Body.String())
		}
	}
	if response := get("/api/google-photos/media/99999/original"); response.Code != 404 {
		t.Fatalf("unknown photo: %d", response.Code)
	}
	if response := get(fmt.Sprintf("/api/google-photos/media/%d/raw", id)); response.Code != 404 {
		t.Fatalf("unknown mode: %d", response.Code)
	}
	page := get("/api/google-photos?tab=alternative")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `"IMG_0003.JPG"`) {
		t.Fatalf("page: %d %s", page.Code, page.Body.String())
	}
}
