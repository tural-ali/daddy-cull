package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyScreenshot(t *testing.T) {
	for name, want := range map[string]string{
		"/icloud/alex/2026/08/21/IMG_3793.PNG":           "png",
		"/x/Screenshot 2026-08-21 at 10.00.00.jpg":        "name",
		"/x/screen_shot.HEIC":                             "name",
		"/x/SCREEN-RECORDING 1.mov":                       "name",
		"/x/Simulator Screen Shot - iPhone.png":           "name",
		"/x/ScreenShot.xmp":                               "",
		"/x/IMG_0001.png.aae":                             "",
		"/x/My Screenshot.jpg":                            "",
		"/x/IMG_5122.HEIC":                                "",
		"/x/9D751694-7A2C-43DD-89E6-5C09327981DC (2).PNG": "png",
		`C:\x\Screenshot.jpg`:                             "",
		"/x/screens.jpg":                                  "",
		"/x/ſcreenshot.jpg":                               "",
	} {
		if got := ClassifyScreenshot(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestScanArchiveAddsOnlyNewDatedMedia(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := `{"path":"/archive/2026/2026-08/2026-08-20/OLD.HEIC","source":"archive","capturedAt":1787184000,"kind":"image","size":3,"favourite":true}
{"path":"/archive/2026/2026-08/2026-08-20/GONE.HEIC","source":"archive","capturedAt":1787184000,"kind":"image","size":3}`
	if err = s.ImportSnapshot(ctx, strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(rel string, mtime time.Time) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("media"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	inDay := time.Date(2026, 8, 21, 14, 30, 0, 0, time.UTC)
	outOfDay := time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC)
	write("2026/2026-08/2026-08-20/OLD.HEIC", inDay)
	write("2026/2026-08/2026-08-21/NEW.HEIC", inDay)
	write("2026/2026-08/2026-08-21/NEW.MOV", outOfDay)
	write("2026/2026-08/2026-08-21/A7400001.ARW", inDay)
	write("2026/2026-08/2026-08-21/A7400001.xmp", inDay)
	write("2026/2026-08/2026-08-21/IMG_3793.PNG", inDay)
	write("2026/2026-08/2026-08-21/.hidden.jpg", inDay)
	write("2026/2026-08/2026-08-21/.thumbs/T.jpg", inDay)
	write(".live-photos/2026/2026-08/2026-08-21/LIVE.MOV", inDay)
	write("downloaded-smart-previews/P.jpg", inDay)
	write("2026/2026-09/2026-08-22/MISFILED.jpg", inDay)
	result, err := s.ScanArchive(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 4 || result.Missing != 1 || len(result.Screenshots) != 1 || result.Screenshots[0] != "/archive/2026/2026-08/2026-08-21/IMG_3793.PNG" {
		t.Fatalf("scan result %+v", result)
	}
	rows, err := s.read.Query("SELECT a.relative_path,a.kind,a.captured_at,COALESCE(d.day,'') FROM assets a LEFT JOIN asset_days d ON d.asset_id=a.id WHERE a.relative_path LIKE '%2026-08-21%' ORDER BY a.relative_path")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var p, kind, day string
		var captured int64
		if err = rows.Scan(&p, &kind, &captured, &day); err != nil {
			t.Fatal(err)
		}
		got[filepath.Base(p)] = kind + " " + time.Unix(captured, 0).UTC().Format(time.RFC3339) + " " + day
	}
	want := map[string]string{
		"A7400001.ARW": "raw 2026-08-21T14:30:00Z 2026-08-21",
		"IMG_3793.PNG": "image 2026-08-21T14:30:00Z 2026-08-21",
		"NEW.HEIC":     "image 2026-08-21T14:30:00Z 2026-08-21",
		// A modification time off the folder's day does not move the file.
		"NEW.MOV": "video 2026-08-21T00:00:00Z 2026-08-21",
	}
	if len(got) != len(want) {
		t.Fatalf("catalogued %v", got)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s: got %q, want %q", name, got[name], value)
		}
	}
	// The existing file keeps its decision and its recorded time.
	var favourite int
	var captured int64
	if err = s.read.QueryRow("SELECT d.favourite,a.captured_at FROM assets a JOIN decisions d ON d.asset_id=a.id WHERE a.relative_path LIKE '%OLD.HEIC'").Scan(&favourite, &captured); err != nil || favourite != 1 || captured != 1787184000 {
		t.Fatalf("existing asset changed: favourite=%d captured=%d err=%v", favourite, captured, err)
	}
	var total int
	if err = s.read.QueryRow("SELECT total FROM stats WHERE id=1").Scan(&total); err != nil || total != 6 {
		t.Fatalf("total %d %v", total, err)
	}
	again, err := s.ScanArchive(ctx, root)
	if err != nil || again.Added != 0 || again.Files != 5 {
		t.Fatalf("second scan %+v %v", again, err)
	}
}
