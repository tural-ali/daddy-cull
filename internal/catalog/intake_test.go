package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	// live is Apple's Live Photo identifier of each file, by name.
	live map[string]string
}

// newIntakeFixture is an empty library and Import folder, with a reader that
// answers from dated and live, by file name, the way exiftool answers from
// metadata.
func newIntakeFixture(t *testing.T) *intakeFixture {
	t.Helper()
	s := testStore(t)
	inbox, archive := t.TempDir(), t.TempDir()
	w, err := NewIntakeWriter(s, inbox, archive, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	f := &intakeFixture{s: s, w: w, inbox: inbox, archive: archive, dated: map[string]time.Time{}, live: map[string]string{}}
	w.settle = 0
	w.capture = func(_ context.Context, files []string) (map[string]Capture, error) {
		found := map[string]Capture{}
		for _, file := range files {
			when, dated := f.dated[filepath.Base(file)]
			live, ok := f.live[filepath.Base(file)]
			if dated || ok {
				found[file] = Capture{Taken: when, LiveID: live}
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

// livePairs is each catalogued Live Photo's file name and its video's, as
// Cull pairs them once the filed day is catalogued.
func (f *intakeFixture) livePairs(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.s.read.Query("SELECT a.relative_path,l.clip FROM live_clips l JOIN assets a ON a.id=l.photo_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pairs := map[string]string{}
	for rows.Next() {
		var photo, clip string
		if err := rows.Scan(&photo, &clip); err != nil {
			t.Fatal(err)
		}
		pairs[filepath.Base(photo)] = filepath.Base(clip)
	}
	return pairs
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
		"IMG_0001 (2)_HEVC.MOV": "the video",
		"IMG_0001 (2).HEIC.xmp": "the sidecar",
	} {
		if got := readText(t, filepath.Join(day, name)); got != body {
			t.Errorf("%s = %q", name, got)
		}
	}
	if got := f.livePairs(t); got["IMG_0001 (2).HEIC"] != "IMG_0001 (2)_HEVC.MOV" {
		t.Fatalf("Cull pairs %v", got)
	}
}

// icloudpd tells photos of one name apart by adding their sizes, and names the
// two halves of a Live Photo apart: only Apple's identifier, which both carry,
// says FullSizeRender_HEVC-5510754.MOV is the video of FullSizeRender-1198557.HEIC.
// A phone's export may name the video IMG_0002.MOV. Each video is filed under
// its still's name with _HEVC, which Cull pairs, and never as a video of its own.
func TestIntakePairsALivePhotoByAppleIdentifier(t *testing.T) {
	f := newIntakeFixture(t)
	day := filepath.Join(f.archive, "2019/2019-08/2019-08-14")
	when := time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	f.dated["FullSizeRender-1198557.HEIC"] = when
	f.live["FullSizeRender-1198557.HEIC"] = "6C2C54E8-0000-4000-8000-000000000001"
	f.live["FullSizeRender_HEVC-5510754.MOV"] = "6C2C54E8-0000-4000-8000-000000000001"
	// The video of another photo of that name, and a film that is no one's.
	f.dated["FullSizeRender-2245120.HEIC"] = when
	f.live["FullSizeRender-2245120.HEIC"] = "6C2C54E8-0000-4000-8000-000000000002"
	f.live["FullSizeRender_HEVC-7781023.MOV"] = "6C2C54E8-0000-4000-8000-000000000002"
	f.dated["FullSizeRender_HEVC-1000001.MOV"] = when
	f.dated["IMG_0002.HEIC"] = when
	f.live["IMG_0002.HEIC"] = "6C2C54E8-0000-4000-8000-000000000003"
	f.live["IMG_0002.MOV"] = "6C2C54E8-0000-4000-8000-000000000003"
	f.dated["IMG_0003.HEIC"] = when
	f.dated["IMG_0003.MOV"] = when
	for name, body := range map[string]string{
		"FullSizeRender-1198557.HEIC": "still one", "FullSizeRender_HEVC-5510754.MOV": "video one",
		"FullSizeRender-2245120.HEIC": "still two", "FullSizeRender_HEVC-7781023.MOV": "video two",
		"FullSizeRender_HEVC-1000001.MOV": "a film",
		"IMG_0002.HEIC":                   "exported still", "IMG_0002.MOV": "exported video",
		"IMG_0003.HEIC": "a photo", "IMG_0003.MOV": "a film of its own",
	} {
		writeFile(t, f.inbox, name, body)
	}

	if status := f.pass(t); status.Filed != 9 || status.Problem != "" {
		t.Fatalf("status = %+v", status)
	}
	for name, body := range map[string]string{
		"FullSizeRender-1198557.HEIC": "still one", "FullSizeRender-1198557_HEVC.MOV": "video one",
		"FullSizeRender-2245120.HEIC": "still two", "FullSizeRender-2245120_HEVC.MOV": "video two",
		"FullSizeRender_HEVC-1000001.MOV": "a film",
		"IMG_0002.HEIC":                   "exported still", "IMG_0002_HEVC.MOV": "exported video",
		// Without the identifier a video of the same name is not taken for one.
		"IMG_0003.HEIC": "a photo", "IMG_0003.MOV": "a film of its own",
	} {
		if got := readText(t, filepath.Join(day, name)); got != body {
			t.Errorf("%s = %q", name, got)
		}
	}
	want := map[string]string{
		"FullSizeRender-1198557.HEIC": "FullSizeRender-1198557_HEVC.MOV",
		"FullSizeRender-2245120.HEIC": "FullSizeRender-2245120_HEVC.MOV",
		"IMG_0002.HEIC":               "IMG_0002_HEVC.MOV",
	}
	if got := f.livePairs(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("Cull pairs %v, want %v", got, want)
	}
}

// icloudpd downloads a Live Photo's still, then its video, so the still may be
// filed a look before the video arrives. The video then finds its still in the
// library by Apple's identifier, even one filed under another name.
func TestIntakePairsALivePhotoVideoArrivingAfterItsStill(t *testing.T) {
	f := newIntakeFixture(t)
	day := filepath.Join(f.archive, "2019/2019-08/2019-08-14")
	when := time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	writeFile(t, day, "IMG_0001.HEIC", "a different photo from another camera")
	f.live["IMG_0001.HEIC"] = "6C2C54E8-0000-4000-8000-00000000000A"
	f.dated["IMG_0001 (2).HEIC"] = when
	f.live["IMG_0001 (2).HEIC"] = "6C2C54E8-0000-4000-8000-000000000001"
	f.dated["FullSizeRender-1198557.HEIC"] = when
	f.live["FullSizeRender-1198557.HEIC"] = "6C2C54E8-0000-4000-8000-000000000002"
	writeFile(t, f.inbox, "IMG_0001.HEIC", "the still")
	writeFile(t, f.inbox, "FullSizeRender-1198557.HEIC", "another still")
	f.dated["IMG_0001.HEIC"] = when
	f.live["IMG_0001.HEIC"] = "6C2C54E8-0000-4000-8000-000000000001"
	if status := f.pass(t); status.Filed != 2 {
		t.Fatalf("status = %+v", status)
	}
	if got := readText(t, filepath.Join(day, "IMG_0001 (2).HEIC")); got != "the still" {
		t.Fatalf("still = %q", got)
	}
	// In the library, IMG_0001.HEIC is the other camera's photo again.
	f.live["IMG_0001.HEIC"] = "6C2C54E8-0000-4000-8000-00000000000A"

	f.dated["IMG_0001_HEVC.MOV"] = when
	f.live["IMG_0001_HEVC.MOV"] = "6C2C54E8-0000-4000-8000-000000000001"
	f.dated["FullSizeRender_HEVC-5510754.MOV"] = when
	f.live["FullSizeRender_HEVC-5510754.MOV"] = "6C2C54E8-0000-4000-8000-000000000002"
	// A photo that already has its video keeps it; the newcomer is filed apart.
	writeFile(t, day, "IMG_0005.HEIC", "a filed Live Photo")
	writeFile(t, day, "IMG_0005_HEVC.MOV", "its video")
	f.live["IMG_0005.HEIC"] = "6C2C54E8-0000-4000-8000-000000000005"
	f.dated["IMG_0005_HEVC-99.MOV"] = when
	f.live["IMG_0005_HEVC-99.MOV"] = "6C2C54E8-0000-4000-8000-000000000005"
	writeFile(t, f.inbox, "IMG_0001_HEVC.MOV", "the video")
	writeFile(t, f.inbox, "FullSizeRender_HEVC-5510754.MOV", "another video")
	writeFile(t, f.inbox, "IMG_0005_HEVC-99.MOV", "a second video")
	if status := f.pass(t); status.Filed != 3 || status.Problem != "" {
		t.Fatalf("status = %+v", status)
	}
	for name, body := range map[string]string{
		"IMG_0001.HEIC":                   "a different photo from another camera",
		"IMG_0001 (2)_HEVC.MOV":           "the video",
		"FullSizeRender-1198557_HEVC.MOV": "another video",
		"IMG_0005_HEVC.MOV":               "its video",
		"IMG_0005_HEVC-99.MOV":            "a second video",
	} {
		if got := readText(t, filepath.Join(day, name)); got != body {
			t.Errorf("%s = %q", name, got)
		}
	}
	if exists(t, filepath.Join(day, "IMG_0001_HEVC.MOV")) {
		t.Error("the video was filed as the other camera's photo's")
	}
	want := map[string]string{
		"IMG_0001 (2).HEIC":           "IMG_0001 (2)_HEVC.MOV",
		"FullSizeRender-1198557.HEIC": "FullSizeRender-1198557_HEVC.MOV",
		"IMG_0005.HEIC":               "IMG_0005_HEVC.MOV",
	}
	if got := f.livePairs(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("Cull pairs %v, want %v", got, want)
	}
}

// A still the library already holds is set aside, and its new video is
// filed under the name the library's copy has.
func TestIntakeNamesALiveVideoAfterTheStillTheLibraryHolds(t *testing.T) {
	f := newIntakeFixture(t)
	day := filepath.Join(f.archive, "2019/2019-08/2019-08-14")
	when := time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	writeFile(t, day, "IMG_0001.HEIC", "a different photo from another camera")
	writeFile(t, day, "IMG_0001 (2).HEIC", "the still")
	f.dated["IMG_0001.HEIC"] = when
	writeFile(t, f.inbox, "IMG_0001.HEIC", "the still")
	writeFile(t, f.inbox, "IMG_0001_HEVC.MOV", "the video")
	if status := f.pass(t); status.Filed != 1 || status.Problem != "" {
		t.Fatalf("status = %+v", status)
	}
	if got := readText(t, filepath.Join(day, "IMG_0001 (2)_HEVC.MOV")); got != "the video" {
		t.Fatalf("video = %q", got)
	}
	if got := readText(t, filepath.Join(f.inbox, IntakeDuplicates, "IMG_0001.HEIC")); got != "the still" {
		t.Fatalf("set aside = %q", got)
	}
}

// exiftool reads Apple's Live Photo identifier with the dates, in one call.
func TestExiftoolCaptureReadsTheLivePhotoIdentifier(t *testing.T) {
	tool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	clip, film := filepath.Join(dir, "IMG_0001_HEVC.MOV"), filepath.Join(dir, "IMG_0002.MOV")
	for _, file := range []string{clip, film} {
		if out, err := exec.Command(ffmpeg, "-loglevel", "error", "-f", "lavfi", "-i", "color=c=gray:s=64x64:d=1", "-c:v", "libx264", file).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	if out, err := exec.Command(tool, "-q", "-overwrite_original", "-Keys:ContentIdentifier=6c2c54e8-0000-4000-8000-000000000001",
		"-Keys:CreationDate=2019:08:14 12:00:00+01:00", clip).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	read, err := exiftoolCapture(tool)(context.Background(), []string{clip, film})
	if err != nil {
		t.Fatal(err)
	}
	if got := read[clip]; got.LiveID != "6C2C54E8-0000-4000-8000-000000000001" || got.Taken.Format("2006-01-02 15:04") != "2019-08-14 12:00" {
		t.Fatalf("clip = %+v", got)
	}
	if got, ok := read[film]; ok {
		t.Fatalf("a film with nothing to say = %+v", got)
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

func TestIntakeLeavesPlaceholdersInADownloadFolder(t *testing.T) {
	f := newIntakeFixture(t)
	downloads := t.TempDir()
	if err := f.w.Mirror(downloads); err != nil {
		t.Fatal(err)
	}
	day := filepath.Join(f.archive, "2019/2019-08/2019-08-14")
	writeFile(t, day, "IMG_0009.JPG", "already filed")
	f.dated["IMG_0010_AbCdEfG.HEIC"] = time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	f.dated["IMG_0009_AbCdEfH.JPG"] = time.Date(2019, 8, 14, 12, 0, 0, 0, time.Local)
	writeFile(t, downloads, "IMG_0010_AbCdEfG.HEIC", "downloaded")
	writeFile(t, downloads, "IMG_0009_AbCdEfH.JPG", "already filed")
	writeFile(t, downloads, "abcdef.part", "still downloading")

	status := f.pass(t)
	if status.Filed != 1 || status.Duplicates != 0 || status.Unsupported != 0 || status.Problem != "" {
		t.Fatalf("status = %+v", status)
	}
	if got := readText(t, filepath.Join(day, "IMG_0010_AbCdEfG.HEIC")); got != "downloaded" {
		t.Fatalf("filed = %q", got)
	}
	// Each leaves an empty file of its name, which the downloader takes as
	// already downloaded.
	for _, name := range []string{"IMG_0010_AbCdEfG.HEIC", "IMG_0009_AbCdEfH.JPG"} {
		if got := readText(t, filepath.Join(downloads, name)); got != "" {
			t.Errorf("%s holds %q, want an empty placeholder", name, got)
		}
	}
	if got := readText(t, filepath.Join(downloads, "abcdef.part")); got != "still downloading" {
		t.Fatalf("the download in progress was touched: %q", got)
	}
	if again := f.pass(t); again.Filed != 0 || again.Waiting != 0 {
		t.Fatalf("second look = %+v", again)
	}
	inside := filepath.Join(f.archive, "iCloud")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Mirror(inside); err == nil || !strings.Contains(err.Error(), "must not be inside") {
		t.Fatal("a download folder inside the library was accepted")
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
		"IMG_0001_HEVC.MOV": "IMG_0001 (2)_HEVC.MOV",
		"IMG_0001_hevc.mp4": "IMG_0001 (2)_hevc.mp4",
		"IMG_0001_HEVC.JPG": "IMG_0001_HEVC (2).JPG",
		"README":            "README (2)",
	} {
		if got := withSuffix(name, " (2)"); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}
