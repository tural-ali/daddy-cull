package catalog

import (
	"context"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Each file of a RAW and its exports says what it is: the camera and lens
// that took it, the exposure, its size turned by its orientation, and what
// wrote it last. A file that is not there, or not catalogued, is not read.
func TestFileDetailsReadTheFileItself(t *testing.T) {
	tool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2024", "2024-09", "2024-09-29")
	if err = os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	photo := filepath.Join(day, "DSC01234.jpg")
	file, err := os.Create(photo)
	if err != nil {
		t.Fatal(err)
	}
	if err = jpeg.Encode(file, image.NewRGBA(image.Rect(0, 0, 60, 40)), nil); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if out, err := exec.Command(tool, "-q", "-overwrite_original", "-Make=SONY", "-Model=ILCE-7M4", "-LensModel=FE 24-105mm F4 G OSS",
		"-ExposureTime=0.004", "-FNumber=4.5", "-ISO=100", "-FocalLength=46", "-Software=Adobe Lightroom 9.5.1 (iOS)", "-Orientation#=6", photo).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	for _, row := range []struct {
		id   int64
		name string
	}{{1, "DSC01234.jpg"}, {2, "GONE.jpg"}} {
		if _, err = s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',100,'archive')", row.id, "/archive/2024/2024-09/2024-09-29/"+row.name); err != nil {
			t.Fatal(err)
		}
	}
	roots := MediaRoots{Archive: root, RawTool: tool}

	got, ok := s.fileDetails(ctx, roots, 1)
	if !ok {
		t.Fatal("the file was not read")
	}
	if got.Modified.IsZero() {
		t.Fatal("no modification time")
	}
	want := FileDetails{Camera: "SONY ILCE-7M4", Lens: "FE 24-105mm F4 G OSS", Shutter: "1/250", Aperture: 4.5, ISO: 100, Focal: 46,
		Software: "Adobe Lightroom 9.5.1 (iOS)", Width: 40, Height: 60, Modified: got.Modified}
	if got != want {
		t.Fatalf("details %+v, want %+v", got, want)
	}
	for _, id := range []int64{2, 3} {
		if _, ok = s.fileDetails(ctx, roots, id); ok {
			t.Fatalf("file %d was read, but it is not on disk or not catalogued", id)
		}
	}
	if _, ok = s.fileDetails(ctx, MediaRoots{Archive: root}, 1); ok {
		t.Fatal("read without a metadata reader")
	}
}

// A model that repeats its maker's name is not written twice, and a location
// is noticed however it is written.
func TestParseDetailsNamesTheCamera(t *testing.T) {
	for _, c := range []struct{ out, camera string }{
		{`[{"Make":"Canon","Model":"Canon EOS R5"}]`, "Canon EOS R5"},
		{`[{"Make":"NIKON CORPORATION","Model":"Z 6"}]`, "NIKON Z 6"},
		{`[{"Make":"Apple","Model":"iPhone 15 Pro","GPSLatitude":51.5}]`, "Apple iPhone 15 Pro"},
		{`[{"Model":"ILCE-7M4"}]`, "ILCE-7M4"},
		{`[{}]`, ""},
	} {
		got := parseDetails([]byte(c.out))
		if got.Camera != c.camera {
			t.Errorf("%s: camera %q, want %q", c.out, got.Camera, c.camera)
		}
		if got.Located != (c.camera == "Apple iPhone 15 Pro") {
			t.Errorf("%s: located %v", c.out, got.Located)
		}
	}
}
