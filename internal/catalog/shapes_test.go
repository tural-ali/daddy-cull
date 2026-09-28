package catalog

import (
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The reader's answer is the stored size, swapped when the picture is turned
// a quarter turn by its orientation tag or its rotation.
func TestParseShape(t *testing.T) {
	for _, c := range []struct {
		name, out     string
		width, height int
	}{
		{"landscape still", `[{"ImageWidth":5712,"ImageHeight":4284,"Orientation":1}]`, 5712, 4284},
		{"still turned by its tag", `[{"ImageWidth":4032,"ImageHeight":3024,"Orientation":6}]`, 3024, 4032},
		{"mirrored and turned", `[{"ImageWidth":4032,"ImageHeight":3024,"Orientation":5}]`, 3024, 4032},
		{"upside down", `[{"ImageWidth":4032,"ImageHeight":3024,"Orientation":3}]`, 4032, 3024},
		{"phone video held upright", `[{"ImageWidth":3840,"ImageHeight":2160,"Rotation":90}]`, 2160, 3840},
		{"video turned the other way", `[{"ImageWidth":1920,"ImageHeight":1080,"Rotation":270}]`, 1080, 1920},
		{"video upside down", `[{"ImageWidth":1920,"ImageHeight":1080,"Rotation":180}]`, 1920, 1080},
		{"no size", `[{"SourceFile":"/dev/fd/3"}]`, 0, 0},
		{"nonsense", `not json`, 0, 0},
		{"zero", `[{"ImageWidth":0,"ImageHeight":10}]`, 0, 0},
	} {
		width, height := parseShape([]byte(c.out))
		if width != c.width || height != c.height {
			t.Errorf("%s: %dx%d, want %dx%d", c.name, width, height, c.width, c.height)
		}
	}
}

// Each file's shape is read once, through the read-only mount, turned by its
// orientation, and given to the files a page lists. A file the reader cannot
// make sense of is recorded as unknown and not tried again until it changes;
// one that cannot be reached, or sits in the Bin, is left alone.
func TestShapesAreReadOnce(t *testing.T) {
	tool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2025", "2025-04", "2025-04-08")
	if err = os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, encode func(*os.File) error) {
		file, err := os.Create(filepath.Join(day, name))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err = encode(file); err != nil {
			t.Fatal(err)
		}
	}
	write("IMG_0001.JPG", func(f *os.File) error { return jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 64, 48)), nil) })
	write("IMG_0002.JPG", func(f *os.File) error { return jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 64, 48)), nil) })
	write("SHOT.PNG", func(f *os.File) error { return png.Encode(f, image.NewRGBA(image.Rect(0, 0, 30, 90))) })
	if out, err := exec.Command(tool, "-q", "-overwrite_original", "-Orientation#=6", filepath.Join(day, "IMG_0002.JPG")).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if err = os.WriteFile(filepath.Join(day, "BROKEN.JPG"), []byte("not a picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("BINNED.JPG", func(f *os.File) error { return jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil) })
	for _, row := range []struct {
		id   int64
		name string
	}{{1, "IMG_0001.JPG"}, {2, "IMG_0002.JPG"}, {3, "SHOT.PNG"}, {4, "BROKEN.JPG"}, {5, "GONE.JPG"}, {6, "BINNED.JPG"}} {
		if _, err = s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',100,'archive')", row.id, "/archive/2025/2025-04/2025-04-08/"+row.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) VALUES(6,'bin','plan')"); err != nil {
		t.Fatal(err)
	}
	roots := MediaRoots{Archive: root, RawTool: tool}

	read, err := s.FillShapes(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	if read != 4 {
		t.Fatalf("recorded %d, want the three pictures and the broken file", read)
	}
	assets := []*Asset{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}
	if err = s.markShapes(ctx, assets); err != nil {
		t.Fatal(err)
	}
	got := [][2]int{}
	for _, a := range assets {
		got = append(got, [2]int{a.Width, a.Height})
	}
	want := [][2]int{{64, 48}, {48, 64}, {30, 90}, {0, 0}, {0, 0}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shapes %v, want %v", got, want)
		}
	}

	// Nothing changed, so nothing is read again.
	if read, err = s.FillShapes(ctx, roots); err != nil || read != 0 {
		t.Fatalf("second pass read %d (%v), want 0", read, err)
	}
	// A file replaced by one of another size is read again.
	if _, err = s.write.ExecContext(ctx, "UPDATE assets SET size_bytes=200 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if read, err = s.FillShapes(ctx, roots); err != nil || read != 1 {
		t.Fatalf("after a change read %d (%v), want 1", read, err)
	}
}
