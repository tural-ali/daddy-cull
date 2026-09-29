package takeout

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSafePath(t *testing.T) {
	for p, want := range map[string]bool{
		"Takeout/Google Photos/a.jpg": true,
		"a.jpg":                       true,
		"":                            false,
		"/etc/passwd":                 false,
		"../a.jpg":                    false,
		"Takeout/../../a.jpg":         false,
		"Takeout//a.jpg":              false,
		"Takeout/./a.jpg":             false,
		"Takeout\\a.jpg":              false,
		"Takeout/":                    false,
	} {
		if got := SafePath(p); got != want {
			t.Errorf("SafePath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestSafeName(t *testing.T) {
	for name, want := range map[string]bool{
		"IMG_1234.JPG":                         true,
		"Urlaub am Meer.jpg":                   true,
		"":                                     false,
		".hidden.jpg":                          false,
		"a/b.jpg":                              false,
		"a\\b.jpg":                             false,
		"a\nb.jpg":                             false,
		"a�b.jpg":                              false,
		string(bytes.Repeat([]byte("a"), 256)): false,
	} {
		if got := SafeName(name); got != want {
			t.Errorf("SafeName(%q) = %v, want %v", name, got, want)
		}
	}
}

type file struct {
	name string
	body string
}

// writeZip builds a zip in dir the way Takeout does, storing each file.
func writeZip(t *testing.T, dir, name string, files ...file) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		if f.name[len(f.name)-1] == '/' {
			if _, err := w.Create(f.name); err != nil {
				t.Fatal(err)
			}
			continue
		}
		out, err := w.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(out, f.body)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func readAll(t *testing.T, a Archive, entry string) (string, error) {
	t.Helper()
	r, err := a.Open(entry)
	if err != nil {
		return "", err
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	return string(body), err
}

func TestZipListsAndReads(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, dir, "takeout-001.zip",
		file{"Takeout/", ""},
		file{"Takeout/Google Photos/Photos from 2019/IMG_2.JPG", "second"},
		file{"Takeout/Google Photos/Photos from 2019/IMG_1.JPG", "first"},
		file{"Takeout/Google Photos/Photos from 2019/IMG_1.JPG.json", `{"title":"IMG_1.JPG"}`},
		file{"../escape.jpg", "outside"},
		file{"/absolute.jpg", "outside"},
		file{"Takeout/Google Photos/Photos from 2019/IMG_1.JPG", "a duplicate"},
	)
	z, err := OpenZip(openRoot(t, dir), "takeout-001.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var paths []string
	for _, e := range z.Entries() {
		paths = append(paths, e.Path)
		if !e.HasCRC {
			t.Errorf("%s has no checksum", e.Path)
		}
	}
	want := []string{
		"Takeout/Google Photos/Photos from 2019/IMG_1.JPG",
		"Takeout/Google Photos/Photos from 2019/IMG_1.JPG.json",
		"Takeout/Google Photos/Photos from 2019/IMG_2.JPG",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("entries = %q, want %q", paths, want)
	}
	first := z.Entries()[0]
	if first.Size != 5 || first.CRC32 != crc32.ChecksumIEEE([]byte("first")) {
		t.Errorf("first entry = %+v", first)
	}
	if body, err := readAll(t, z, want[0]); err != nil || body != "first" {
		t.Errorf("read %q, %v; want the first copy of the name", body, err)
	}
	if _, err := z.Open("../escape.jpg"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an unsafe entry opened: %v", err)
	}
}

func TestZipChecksumFailsAtTheEnd(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	body := []byte("the photo")
	out, err := w.CreateRaw(&zip.FileHeader{
		Name: "Takeout/Google Photos/a.jpg", Method: zip.Store,
		CRC32: crc32.ChecksumIEEE(body) + 1, CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	out.Write(body)
	w.Close()
	os.WriteFile(filepath.Join(dir, "bad.zip"), buf.Bytes(), 0o644)
	z, err := OpenZip(openRoot(t, dir), "bad.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if _, err := readAll(t, z, "Takeout/Google Photos/a.jpg"); !errors.Is(err, zip.ErrChecksum) {
		t.Fatalf("read a damaged file without an error: %v", err)
	}
}

func TestZipRefusesSymlinksAndOddNames(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, dir, "real.zip", file{"a.jpg", "x"})
	if err := os.Symlink(filepath.Join(dir, "real.zip"), filepath.Join(dir, "link.zip")); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "folder.zip"), 0o755)
	root := openRoot(t, dir)
	if _, err := OpenZip(root, "link.zip"); err == nil {
		t.Error("opened a zip through a symlink")
	}
	if _, err := OpenZip(root, "folder.zip"); err == nil {
		t.Error("opened a folder as a zip")
	}
	if _, err := OpenZip(root, "../real.zip"); !errors.Is(err, ErrUnsafe) {
		t.Errorf("opened a zip outside the inbox: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "not.zip"), []byte("not a zip"), 0o644)
	if _, err := OpenZip(root, "not.zip"); err == nil {
		t.Error("opened a file that is not a zip")
	}
}

func TestFolder(t *testing.T) {
	dir := t.TempDir()
	export := filepath.Join(dir, "export", "Takeout", "Google Photos", "Trip")
	os.MkdirAll(export, 0o755)
	os.WriteFile(filepath.Join(export, "b.jpg"), []byte("bee"), 0o644)
	os.WriteFile(filepath.Join(export, "a.jpg"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(export, ".DS_Store"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "export", ".hidden"), 0o755)
	os.WriteFile(filepath.Join(dir, "export", ".hidden", "c.jpg"), []byte("c"), 0o644)
	outside := filepath.Join(dir, "outside.jpg")
	os.WriteFile(outside, []byte("outside"), 0o644)
	os.Symlink(outside, filepath.Join(export, "link.jpg"))
	os.Symlink(dir, filepath.Join(export, "loop"))

	f, err := OpenFolder(openRoot(t, dir), "export")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []Entry
	got = append(got, f.Entries()...)
	want := []Entry{
		{Path: "Takeout/Google Photos/Trip/a.jpg", Size: 1},
		{Path: "Takeout/Google Photos/Trip/b.jpg", Size: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	if body, err := readAll(t, f, want[1].Path); err != nil || body != "bee" {
		t.Errorf("read %q, %v", body, err)
	}
	if _, err := f.Open("Takeout/Google Photos/Trip/link.jpg"); err == nil {
		t.Error("read a file through a symlink")
	}
	if _, err := f.Open("../outside.jpg"); !errors.Is(err, ErrUnsafe) {
		t.Errorf("read outside the folder: %v", err)
	}
}

func TestInner(t *testing.T) {
	for entry, want := range map[string]string{
		"Takeout/Google Photos/a.jpg":              "Takeout/Google Photos/a.jpg",
		"takeout-2024/Takeout/Google Photos/a.jpg": "Takeout/Google Photos/a.jpg",
		"Google Photos/a.jpg":                      "Google Photos/a.jpg",
	} {
		if got := Inner(entry); got != want {
			t.Errorf("Inner(%q) = %q, want %q", entry, got, want)
		}
	}
}

func TestParseSidecar(t *testing.T) {
	s, ok := ParseSidecar([]byte(`{
		"title": "IMG_1234.JPG",
		"description": "Beach",
		"photoTakenTime": {"timestamp": "1565785800", "formatted": "14 Aug 2019"},
		"geoData": {"latitude": 50.1, "longitude": 8.6},
		"geoDataExif": {"latitude": 0, "longitude": 0},
		"people": [{"name": "Alex"}, {"name": ""}],
		"favorited": true
	}`))
	if !ok {
		t.Fatal("a photo's JSON was not read")
	}
	taken, ok := s.Taken()
	if !ok || !taken.Equal(time.Unix(1565785800, 0)) {
		t.Errorf("taken = %v, %v", taken, ok)
	}
	if place, ok := s.Place(); !ok || place.Latitude != 50.1 {
		t.Errorf("place = %+v, %v", place, ok)
	}
	if got := s.PeopleNames(); !reflect.DeepEqual(got, []string{"Alex"}) {
		t.Errorf("people = %q", got)
	}
	if !s.Favorited || s.Description != "Beach" {
		t.Errorf("sidecar = %+v", s)
	}
	for _, body := range []string{
		`{"title": "Trip", "date": {"timestamp": "1565785800"}}`,
		`{"photoTakenTime": {"timestamp": "1565785800"}}`,
		`not json`,
	} {
		if _, ok := ParseSidecar([]byte(body)); ok {
			t.Errorf("read %s as a photo's JSON", body)
		}
	}
	if _, ok := (Sidecar{PhotoTakenTime: &Timestamp{Timestamp: "0"}}).Taken(); ok {
		t.Error("a zero time was taken as known")
	}
}

func TestPair(t *testing.T) {
	long := "PXL_20230815_123456789.PORTRAIT.ORIGINAL.jpg"
	cut := "Screenshot_2023-01-01-12-00-00-000_com.example.l"
	tests := []struct {
		name     string
		media    []string
		sidecars map[string]string
		want     map[string]string
	}{
		{
			name:     "named in full",
			media:    []string{"IMG_1.JPG", "IMG_2.JPG"},
			sidecars: map[string]string{"IMG_1.JPG.json": "IMG_1.JPG", "IMG_2.JPG.supplemental-metadata.json": "IMG_2.JPG"},
			want:     map[string]string{"IMG_1.JPG": "IMG_1.JPG.json", "IMG_2.JPG": "IMG_2.JPG.supplemental-metadata.json"},
		},
		{
			name:  "a second file of the same name",
			media: []string{"IMG_1.JPG", "IMG_1(1).JPG", "IMG_2(1).JPG"},
			sidecars: map[string]string{
				"IMG_1.JPG.json": "IMG_1.JPG", "IMG_1.JPG(1).json": "IMG_1.JPG",
				"IMG_2.JPG.supplemental-metadata(1).json": "IMG_2.JPG",
			},
			want: map[string]string{
				"IMG_1.JPG": "IMG_1.JPG.json", "IMG_1(1).JPG": "IMG_1.JPG(1).json",
				"IMG_2(1).JPG": "IMG_2.JPG.supplemental-metadata(1).json",
			},
		},
		{
			name:     "the JSON's name cut short",
			media:    []string{long},
			sidecars: map[string]string{long + ".supp.json": long},
			want:     map[string]string{long: long + ".supp.json"},
		},
		{
			name:     "the photo's name cut short too",
			media:    []string{cut + ".jpg"},
			sidecars: map[string]string{cut + ".json": cut + "auncher.jpg"},
			want:     map[string]string{cut + ".jpg": cut + ".json"},
		},
		{
			name:     "an edited copy shares the original's",
			media:    []string{"IMG_1.JPG", "IMG_1-edited.JPG", "IMG_2-bearbeitet.jpg"},
			sidecars: map[string]string{"IMG_1.JPG.json": "IMG_1.JPG", "IMG_2.jpg.json": "IMG_2.jpg"},
			want:     map[string]string{"IMG_1.JPG": "IMG_1.JPG.json", "IMG_1-edited.JPG": "IMG_1.JPG.json", "IMG_2-bearbeitet.jpg": "IMG_2.jpg.json"},
		},
		{
			name:     "found by the title inside",
			media:    []string{"holiday.jpg"},
			sidecars: map[string]string{"something else.json": "holiday.jpg"},
			want:     map[string]string{"holiday.jpg": "something else.json"},
		},
		{
			name:     "a title two JSONs claim is no answer",
			media:    []string{"holiday.jpg"},
			sidecars: map[string]string{"one.json": "holiday.jpg", "two.json": "holiday.jpg"},
			want:     map[string]string{},
		},
		{
			name:     "a Live Photo's video takes the still's",
			media:    []string{"IMG_1.HEIC", "IMG_1.MOV", "IMG_9.MOV"},
			sidecars: map[string]string{"IMG_1.HEIC.json": "IMG_1.HEIC"},
			want:     map[string]string{"IMG_1.HEIC": "IMG_1.HEIC.json", "IMG_1.MOV": "IMG_1.HEIC.json"},
		},
		{
			name:     "a short name is never matched by a prefix",
			media:    []string{"IMG_12.JPG", "IMG_1.JPG"},
			sidecars: map[string]string{"IMG_1.J.json": "IMG_1.J", "metadata.json": "Trip"},
			want:     map[string]string{},
		},
		{
			name:     "a numbered file does not take the plain one's",
			media:    []string{"IMG_1(1).JPG"},
			sidecars: map[string]string{"IMG_1.JPG.json": "IMG_1.JPG"},
			want:     map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Pair(tt.media, tt.sidecars); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Pair = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKinds(t *testing.T) {
	for name, want := range map[string]string{"a.HEIC": "image", "a.jpg": "image", "a.dng": "raw", "a.MOV": "video", "a.mp4": "video"} {
		if !IsMedia(name) || Kind(name) != want {
			t.Errorf("%s: media %v, kind %s, want %s", name, IsMedia(name), Kind(name), want)
		}
	}
	for _, name := range []string{"a.json", "a.html", "a", "print-subscriptions.json"} {
		if IsMedia(name) {
			t.Errorf("%s taken as media", name)
		}
	}
	if !IsSidecar("a.JPG.JSON") || IsSidecar("a.jpg") {
		t.Error("IsSidecar is wrong")
	}
	if !IsVideo("a.m4v") || IsVideo("a.heic") {
		t.Error("IsVideo is wrong")
	}
}

func TestNameTime(t *testing.T) {
	for name, want := range map[string]string{
		"PXL_20210314_101530123.jpg":                 "2021-03-14 10:15:30",
		"IMG_20190814_123000.jpg":                    "2019-08-14 12:30:00",
		"2019-08-14 12.30.00.jpg":                    "2019-08-14 12:30:00",
		"Screenshot_2023-01-02-09-08-07-000_app.png": "2023-01-02 09:08:07",
		"VID-20200101-WA0001.mp4":                    "",
		"IMG_1234.JPG":                               "",
		"20191399_101010.jpg":                        "",
	} {
		got, ok := NameTime(name)
		if want == "" {
			if ok {
				t.Errorf("NameTime(%q) = %v, want none", name, got)
			}
			continue
		}
		if !ok || got.Format("2006-01-02 15:04:05") != want || got.Location() != time.UTC {
			t.Errorf("NameTime(%q) = %v, %v, want %s UTC", name, got, ok, want)
		}
	}
}

func TestInbox(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, dir, "takeout-001.zip", file{"Takeout/Google Photos/Trip/a.jpg", "a"})
	os.WriteFile(filepath.Join(dir, "takeout-002.tgz"), []byte("tar"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden.zip"), []byte("h"), 0o644)
	os.MkdirAll(filepath.Join(dir, "Unpacked", "Takeout", "Google Photos", "Trip"), 0o755)
	os.WriteFile(filepath.Join(dir, "Unpacked", "Takeout", "Google Photos", "Trip", "b.jpg"), []byte("bb"), 0o644)
	writeZip(t, filepath.Join(dir, "Unpacked"), "takeout-003.zip", file{"Takeout/Google Photos/Trip/c.jpg", "c"})
	os.MkdirAll(filepath.Join(dir, "Empty"), 0o755)
	os.Symlink(filepath.Join(dir, "takeout-001.zip"), filepath.Join(dir, "link.zip"))

	inbox, err := OpenInbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	found, err := inbox.List()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range found {
		got = append(got, f.Kind+" "+f.Name)
	}
	want := []string{"folder Unpacked", "zip Unpacked/takeout-003.zip", "zip takeout-001.zip", "unsupported takeout-002.tgz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("found %q, want %q", got, want)
	}
	if found[0].Size != 2 {
		t.Errorf("the folder counts %d bytes, want 2: its zip is an archive of its own", found[0].Size)
	}
	for _, c := range []struct{ name, kind, entry, body string }{
		{"takeout-001.zip", KindZip, "Takeout/Google Photos/Trip/a.jpg", "a"},
		{"takeout-001.zip", KindZip, "Takeout/Google Photos/Trip/a.jpg", "a"},
		{"Unpacked/takeout-003.zip", KindZip, "Takeout/Google Photos/Trip/c.jpg", "c"},
		{"Unpacked", KindFolder, "Takeout/Google Photos/Trip/b.jpg", "bb"},
	} {
		r, err := inbox.ReadEntry(c.name, c.kind, c.entry)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		body, _ := io.ReadAll(r)
		r.Close()
		if string(body) != c.body {
			t.Errorf("%s %s = %q", c.name, c.entry, body)
		}
	}
	if _, err := inbox.ReadEntry("link.zip", KindZip, "Takeout/Google Photos/Trip/a.jpg"); err == nil {
		t.Error("read through a symlink")
	}
	// A zip replaced on disk is read afresh rather than from the one held open.
	writeZip(t, dir, "takeout-001.zip", file{"Takeout/Google Photos/Trip/a.jpg", "changed"})
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(dir, "takeout-001.zip"), later, later)
	r, err := inbox.ReadEntry("takeout-001.zip", KindZip, "Takeout/Google Photos/Trip/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r)
	r.Close()
	if string(body) != "changed" {
		t.Errorf("read %q from a zip replaced on disk", body)
	}
}

func TestInboxKeepsFewOpen(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < keepOpen+3; i++ {
		writeZip(t, dir, fmt.Sprintf("t-%d.zip", i), file{"a.jpg", fmt.Sprint(i)})
	}
	inbox, _ := OpenInbox(dir)
	defer inbox.Close()
	// One reader is kept open across the evictions and must still read.
	first, err := inbox.ReadEntry("t-0.zip", KindZip, "a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < keepOpen+3; i++ {
		r, err := inbox.ReadEntry(fmt.Sprintf("t-%d.zip", i), KindZip, "a.jpg")
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	if len(inbox.open) != keepOpen {
		t.Errorf("%d archives open, want %d", len(inbox.open), keepOpen)
	}
	body, err := io.ReadAll(first)
	first.Close()
	if err != nil || string(body) != "0" {
		t.Errorf("an evicted archive in use read %q, %v", body, err)
	}
}

func TestFromPhotos(t *testing.T) {
	for entry, want := range map[string]bool{
		"Takeout/Google Photos/Trip/a.jpg":  true,
		"x/Takeout/Google Fotos/Trip/a.jpg": true,
		"Takeout/Drive/My Drive/a.jpg":      false,
		"Takeout/Google Photos":             false,
		"Photos from 2019/a.jpg":            true,
	} {
		if got := FromPhotos(entry); got != want {
			t.Errorf("FromPhotos(%q) = %v", entry, got)
		}
	}
}
