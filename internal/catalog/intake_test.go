package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type intakeFixture struct {
	s       *Store
	w       *IntakeWriter
	inbox   string
	archive string
	dated   map[string]time.Time
}

// newIntakeFixture is an empty library and Import folder, with a dater that
// answers from dated, by file name, the way exiftool answers from metadata.
func newIntakeFixture(t *testing.T) *intakeFixture {
	t.Helper()
	s := testStore(t)
	inbox, archive := t.TempDir(), t.TempDir()
	w, err := NewIntakeWriter(s, inbox, archive, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	f := &intakeFixture{s: s, w: w, inbox: inbox, archive: archive, dated: map[string]time.Time{}}
	w.settle = 0
	w.date = func(_ context.Context, files []string) (map[string]time.Time, error) {
		found := map[string]time.Time{}
		for _, file := range files {
			if when, ok := f.dated[filepath.Base(file)]; ok {
				found[file] = when
			}
		}
		return found, nil
	}
	return f
}

func (f *intakeFixture) pass(t *testing.T) IntakeStatus {
	t.Helper()
	status, err := f.w.Pass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func readText(t *testing.T, file string) string {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestIntakeFilesEachPhotoUnderTheDayItWasTaken(t *testing.T) {
	f := newIntakeFixture(t)
	f.dated["IMG_0001.JPG"] = time.Date(2019, 8, 14, 9, 30, 0, 0, time.Local)
	writeFile(t, f.inbox, "card/DCIM/IMG_0001.JPG", "metadata says 14 August")
	writeFile(t, f.inbox, "PXL_20210305_101500123.jpg", "the name says 5 March")
	writeFile(t, f.inbox, "holiday.mov", "only the modification time says")
	modified := time.Date(2016, 12, 24, 18, 0, 0, 0, time.Local)
	if err := os.Chtimes(filepath.Join(f.inbox, "holiday.mov"), modified, modified); err != nil {
		t.Fatal(err)
	}

	status := f.pass(t)
	if status.Filed != 3 || status.FiledTotal != 3 || status.Problem != "" {
		t.Fatalf("status = %+v", status)
	}
	for rel, body := range map[string]string{
		"2019/2019-08/2019-08-14/IMG_0001.JPG":               "metadata says 14 August",
		"2021/2021-03/2021-03-05/PXL_20210305_101500123.jpg": "the name says 5 March",
		"2016/2016-12/2016-12-24/holiday.mov":                "only the modification time says",
	} {
		if got := readText(t, filepath.Join(f.archive, filepath.FromSlash(rel))); got != body {
			t.Errorf("%s = %q", rel, got)
		}
	}
	if exists(t, filepath.Join(f.inbox, "card")) {
		t.Error("the folders filing emptied are left in Import")
	}
	if !exists(t, f.inbox) {
		t.Fatal("Import itself was removed")
	}
	var catalogued int
	if err := f.s.read.QueryRow("SELECT count(*) FROM assets WHERE source_id='archive'").Scan(&catalogued); err != nil {
		t.Fatal(err)
	}
	if catalogued != 3 {
		t.Fatalf("catalogued %d files, want the 3 filed", catalogued)
	}
	// Filing keeps the time the file was made.
	info, err := os.Stat(filepath.Join(f.archive, "2016/2016-12/2016-12-24/holiday.mov"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(modified) {
		t.Fatalf("modified %v, want %v", info.ModTime(), modified)
	}
}

func TestIntakeKeepsALivePhotoTogetherAndNeverOverwrites(t *testing.T) {
	f := newIntakeFixture(t)
	day := filepath.Join(f.archive, "2019/2019-08/2019-08-14")
	writeFile(t, day, "IMG_0001.HEIC", "a different photo from another camera")
	f.dated["IMG_0001.HEIC"] = time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	// The video has no date of its own; it goes with its still.
	writeFile(t, f.inbox, "IMG_0001.HEIC", "the still")
	writeFile(t, f.inbox, "IMG_0001_HEVC.MOV", "the video")
	writeFile(t, f.inbox, "IMG_0001.HEIC.xmp", "the sidecar")

	status := f.pass(t)
	if status.Filed != 3 {
		t.Fatalf("status = %+v", status)
	}
	if got := readText(t, filepath.Join(day, "IMG_0001.HEIC")); got != "a different photo from another camera" {
		t.Fatalf("the library's photo was overwritten: %q", got)
	}
	for name, body := range map[string]string{
		"IMG_0001 (2).HEIC":     "the still",
		"IMG_0001_HEVC (2).MOV": "the video",
		"IMG_0001 (2).HEIC.xmp": "the sidecar",
	} {
		if got := readText(t, filepath.Join(day, name)); got != body {
			t.Errorf("%s = %q", name, got)
		}
	}
}

func TestIntakeSetsAsideWhatTheLibraryHolds(t *testing.T) {
	f := newIntakeFixture(t)
	day := filepath.Join(f.archive, "2019/2019-08/2019-08-14")
	writeFile(t, day, "IMG_0002.JPG", "the same bytes")
	f.dated["copy.jpg"] = time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	writeFile(t, f.inbox, "phone/copy.jpg", "the same bytes")
	writeFile(t, f.inbox, "phone/notes.pdf", "not a photo")
	writeFile(t, f.inbox, ".hidden.jpg", "hidden")

	status := f.pass(t)
	if status.Filed != 0 || status.Duplicates != 1 || status.Unsupported != 1 {
		t.Fatalf("status = %+v", status)
	}
	if got := readText(t, filepath.Join(f.inbox, IntakeDuplicates, "phone/copy.jpg")); got != "the same bytes" {
		t.Fatalf("set aside = %q", got)
	}
	for _, rel := range []string{"phone/notes.pdf", ".hidden.jpg"} {
		if !exists(t, filepath.Join(f.inbox, rel)) {
			t.Errorf("%s was moved", rel)
		}
	}
	entries, err := os.ReadDir(day)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the day holds %d files, want 1", len(entries))
	}
	// A second look finds nothing new, and leaves the set-aside file alone.
	again := f.pass(t)
	if again.Filed != 0 || again.Duplicates != 1 {
		t.Fatalf("second look = %+v", again)
	}
}

func TestIntakeWaitsForAPhotoStillArriving(t *testing.T) {
	f := newIntakeFixture(t)
	f.dated["IMG_0003.HEIC"] = time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	writeFile(t, f.inbox, "IMG_0003.HEIC", "the still has arrived")
	writeFile(t, f.inbox, "IMG_0003.MOV", "")

	status := f.pass(t)
	if status.Filed != 0 || status.Waiting != 2 {
		t.Fatalf("status = %+v", status)
	}
	writeFile(t, f.inbox, "IMG_0003.MOV", "and now the video")
	if status := f.pass(t); status.Filed != 2 || status.Waiting != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestIntakeCopiesAcrossDisks(t *testing.T) {
	f := newIntakeFixture(t)
	f.w.link = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EXDEV}
	}
	writeFile(t, f.inbox, "IMG_20200101_000000.jpg", "from the card")
	modified := time.Date(2020, 1, 1, 0, 5, 0, 0, time.Local)
	if err := os.Chtimes(filepath.Join(f.inbox, "IMG_20200101_000000.jpg"), modified, modified); err != nil {
		t.Fatal(err)
	}
	if status := f.pass(t); status.Filed != 1 {
		t.Fatalf("status = %+v", status)
	}
	day := filepath.Join(f.archive, "2020/2020-01/2020-01-01")
	if got := readText(t, filepath.Join(day, "IMG_20200101_000000.jpg")); got != "from the card" {
		t.Fatalf("copied = %q", got)
	}
	if exists(t, filepath.Join(f.inbox, "IMG_20200101_000000.jpg")) {
		t.Fatal("the copied file is still in Import")
	}
	entries, err := os.ReadDir(day)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the day holds %v, %v; no temporary file should remain", entries, err)
	}
	info, err := os.Stat(filepath.Join(day, "IMG_20200101_000000.jpg"))
	if err != nil || !info.ModTime().Equal(modified) {
		t.Fatalf("modified %v, %v", info, err)
	}
}

func TestIntakeStatusReachesTheWebProcess(t *testing.T) {
	f := newIntakeFixture(t)
	secret := strings.Repeat("k", 32)
	handler := f.w.Handler(secret)
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, httptest.NewRequest(http.MethodPost, "/intake/run", nil))
	if refused.Code != http.StatusForbidden {
		t.Fatalf("no key: %d", refused.Code)
	}
	writeFile(t, f.inbox, "IMG_20200101_000000.jpg", "new year")
	request := httptest.NewRequest(http.MethodPost, "/intake/run", nil)
	request.Header.Set("X-Bin-Key", secret)
	ran := httptest.NewRecorder()
	handler.ServeHTTP(ran, request)
	if ran.Code != 200 || !strings.Contains(ran.Body.String(), `"filed":1`) {
		t.Fatalf("run: %d %s", ran.Code, ran.Body.String())
	}
	status, err := f.s.Intake(context.Background())
	if err != nil || status == nil || status.Filed != 1 || status.LastRun == "" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if err := f.s.ForgetIntake(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status, err := f.s.Intake(context.Background()); err != nil || status != nil {
		t.Fatalf("after forgetting = %+v, %v", status, err)
	}
}

func TestIntakeRefusesFoldersInsideEachOther(t *testing.T) {
	s := testStore(t)
	archive := t.TempDir()
	inside := filepath.Join(archive, "Import")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewIntakeWriter(s, inside, archive, ""); err == nil {
		t.Fatal("an Import folder inside the library was accepted")
	}
	if _, err := NewIntakeWriter(s, archive, inside, ""); err == nil {
		t.Fatal("a library inside the Import folder was accepted")
	}
}

func TestDateFromName(t *testing.T) {
	for name, want := range map[string]string{
		"IMG_20190814_120000.jpg":    "2019-08-14",
		"PXL_20190814_120000123.jpg": "2019-08-14",
		"IMG-20190814-WA0001.jpg":    "2019-08-14",
		"2019-08-14 12.00.00.heic":   "2019-08-14",
		"Screenshot 2019-08-14.png":  "2019-08-14",
		"IMG_1234.JPG":               "",
		"DSC_20191399.JPG":           "",
		"clip_120190814.mov":         "",
	} {
		got, ok := dateFromName(name)
		if want == "" {
			if ok {
				t.Errorf("%s dated %v", name, got)
			}
			continue
		}
		if !ok || got.Format("2006-01-02") != want {
			t.Errorf("%s = %v %v, want %s", name, got, ok, want)
		}
	}
}

func TestWithSuffix(t *testing.T) {
	for name, want := range map[string]string{
		"IMG_0001.HEIC":     "IMG_0001 (2).HEIC",
		"IMG_0001.HEIC.xmp": "IMG_0001 (2).HEIC.xmp",
		"IMG_0001.xmp":      "IMG_0001 (2).xmp",
		"README":            "README (2)",
	} {
		if got := withSuffix(name, " (2)"); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}
