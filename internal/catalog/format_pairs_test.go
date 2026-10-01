package catalog

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExposure(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`[{"DateTimeOriginal":"2022:10:02 10:11:12","SubSecTimeOriginal":123,"Model":"iPhone 12"}]`, "2022:10:02 10:11:12|123|iPhone 12"},
		{`[{"DateTimeOriginal":"2022:10:02 10:11:12","SubSecTimeOriginal":"045","Model":" iPhone 12 "}]`, "2022:10:02 10:11:12|045|iPhone 12"},
		{`[{"DateTimeOriginal":"0000:00:00 00:00:00","SubSecTimeOriginal":0,"Model":"X"}]`, "|0|X"},
		{`[{"SourceFile":"/dev/fd/3"}]`, "||"},
		{`not json`, "||"},
	} {
		taken, subsec, model := parseExposure([]byte(c.in))
		if got := taken + "|" + subsec + "|" + model; got != c.want {
			t.Errorf("%s: got %q, want %q", c.in, got, c.want)
		}
	}
}

// A HEIC and a JPEG of one name stack only when both record the same moment,
// to a fraction of a second, and camera. A stack is one photo in the
// library's count, the reviewer can split it, and the day lists it as one
// exposure saved twice, to keep one of, until one is removed.
func TestFormatPairsStackOnlyOneExposure(t *testing.T) {
	tool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	const folder = "2022/2022-10/2022-10-02"
	if err = os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	// Each file is a small JPEG with the tags given; the reader is handed an
	// open file, so it goes by what is inside, whatever the name says.
	write := func(name string, tags ...string) {
		t.Helper()
		target := filepath.Join(root, folder, name)
		staging := target + ".jpg"
		file, err := os.Create(staging)
		if err != nil {
			t.Fatal(err)
		}
		if err = jpeg.Encode(file, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
			t.Fatal(err)
		}
		file.Close()
		if len(tags) > 0 {
			if out, err := exec.Command(tool, append([]string{"-q", "-overwrite_original"}, append(tags, staging)...)...).CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
		}
		if err = os.Rename(staging, target); err != nil {
			t.Fatal(err)
		}
	}
	shot := func(subsec string) []string {
		tags := []string{"-DateTimeOriginal=2022:10:02 10:11:12", "-Model=iPhone 12"}
		if subsec != "" {
			tags = append(tags, "-SubSecTimeOriginal="+subsec)
		}
		return tags
	}
	files := []struct {
		name, kind string
		tags       []string
	}{
		{"IMG_1.HEIC", "image", shot("123")}, // 1 one exposure with 2
		{"IMG_1.JPG", "image", shot("123")},  // 2
		{"IMG_2.heic", "image", shot("123")}, // 3 another moment
		{"img_2.jpeg", "image", shot("456")}, // 4
		{"IMG_3.HEIC", "image", shot("")},    // 5 no fraction of a second
		{"IMG_3.JPG", "image", shot("")},     // 6
		{"IMG_4.HEIC", "image", shot("123")}, // 7 a RAW's exports stack with it
		{"IMG_4.JPG", "image", shot("123")},  // 8
		{"IMG_4.DNG", "raw", nil},            // 9
		{"IMG_5.HEIC", "image", shot("123")}, // 10 three files of one name
		{"IMG_5.JPG", "image", shot("123")},  // 11
		{"IMG_5.TIF", "image", nil},          // 12
	}
	for i, f := range files {
		if f.kind == "image" {
			write(f.name, f.tags...)
		} else if err = os.WriteFile(filepath.Join(root, folder, f.name), []byte("raw"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(root, folder, f.name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1664668800,?,?,'archive')", i+1, "/archive/"+folder+"/"+f.name, f.kind, info.Size()); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	var pairs [][2]int64
	rows, err := s.read.Query("SELECT heic_id,jpeg_id FROM format_pairs ORDER BY heic_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p [2]int64
		rows.Scan(&p[0], &p[1])
		pairs = append(pairs, p)
	}
	rows.Close()
	if fmt.Sprint(pairs) != "[[1 2] [3 4] [5 6]]" {
		t.Fatal("pairs by name", pairs)
	}
	stacks := func() string {
		t.Helper()
		data, err := s.Today(ctx, "10-02")
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]string{}
		for _, y := range data.Years {
			for _, a := range y.Assets {
				if len(a.Stack) > 0 {
					out[a.ID] = fmt.Sprint(a.Stack)
				}
			}
		}
		return fmt.Sprint(out)
	}
	photos := func() int64 {
		t.Helper()
		library, err := s.LibraryStats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return library.Photos.Files
	}
	exposures := func() string {
		t.Helper()
		groups, err := s.sameExposures(ctx, "10-02")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, g := range groups {
			out = append(out, fmt.Sprintf("%s %s %d:%d", g.Hash, g.Proof, g.Members[0].ID, g.Members[1].ID))
		}
		return fmt.Sprint(out)
	}

	// Nothing is proven before the files are read: only the RAW stacks.
	if got := stacks(); got != "map[7:[8 9] 8:[7 9] 9:[7 8]]" {
		t.Fatal("before reading", got)
	}
	if got := photos(); got != 10 {
		t.Fatalf("photos before reading %d, want 12 less the RAW's two exports", got)
	}
	read, err := s.FillExposures(ctx, MediaRoots{Archive: root, RawTool: tool})
	if err != nil || read != 6 {
		t.Fatalf("read %d (%v), want the six files of a pair", read, err)
	}
	if got := stacks(); got != "map[1:[2] 2:[1] 7:[8 9] 8:[7 9] 9:[7 8]]" {
		t.Fatal("after reading", got)
	}
	if got := photos(); got != 9 {
		t.Fatalf("photos %d, want one exposure counted once", got)
	}
	if got := exposures(); got != "[exposure:1 exposure 1:2]" {
		t.Fatal("same exposures", got)
	}
	if read, err = s.FillExposures(ctx, MediaRoots{Archive: root, RawTool: tool}); err != nil || read != 0 {
		t.Fatalf("second pass read %d (%v), want 0", read, err)
	}

	// Shown apart, the pair is two photos and still one exposure saved twice.
	if err = s.SetPaired(ctx, 2, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); got != "map[7:[8 9] 8:[7 9] 9:[7 8]]" {
		t.Fatal("split", got)
	}
	if got := photos(); got != 10 {
		t.Fatalf("photos after the split %d, want 10", got)
	}
	if got := exposures(); got != "[exposure:1 exposure 1:2]" {
		t.Fatal("same exposures after the split", got)
	}
	if err = s.SetPaired(ctx, 1, 2, true); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); got != "map[1:[2] 2:[1] 7:[8 9] 8:[7 9] 9:[7 8]]" {
		t.Fatal("joined again", got)
	}
	// A pair the files do not prove cannot be joined.
	if err = s.SetPaired(ctx, 3, 4, true); err != ErrInvalid {
		t.Fatalf("joining another moment: %v", err)
	}

	// With stacks off in Settings, every file is a photo.
	if err = s.SetRawTogether(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := photos(); got != 12 {
		t.Fatalf("photos shown apart %d, want 12", got)
	}
	if err = s.SetRawTogether(ctx, true); err != nil {
		t.Fatal(err)
	}

	// The day lists it after the copies, and the whole library does not.
	for _, c := range []struct{ query, want string }{{"?md=10-02", `"hash":"exposure:1","proof":"exposure"`}, {"", "[]"}} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/duplicates"+c.query, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), c.want) {
			t.Fatalf("/api/duplicates%s: %d %s", c.query, w.Code, w.Body.String())
		}
	}

	// Removing one settles the choice.
	if _, err = s.write.Exec("INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(2,'cull',0,1)"); err != nil {
		t.Fatal(err)
	}
	if got := exposures(); got != "[]" {
		t.Fatal("same exposures after a removal", got)
	}

	// A file that changes proves nothing until it is read again.
	if _, err = s.write.Exec("UPDATE assets SET size_bytes=size_bytes+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); got != "map[7:[8 9] 8:[7 9] 9:[7 8]]" {
		t.Fatal("after a change", got)
	}
}
