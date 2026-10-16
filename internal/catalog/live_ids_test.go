package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeLiveIDs reads a fixture's Live Photo identifier from its contents,
// written as "live:<identifier>", and counts the files it reads.
func fakeLiveIDs(reads *int) LiveIDReader {
	return func(_ context.Context, files []string) (map[string]string, error) {
		found := map[string]string{}
		for _, file := range files {
			*reads++
			body, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			id, _ := strings.CutPrefix(string(body), "live:")
			if id == string(body) {
				id = ""
			}
			found[file] = id
		}
		return found, nil
	}
}

// Synthetic identifiers, in the form Apple writes them.
const (
	liveA = "AAAAAAAA-0000-4000-8000-000000000001"
	liveB = "BBBBBBBB-0000-4000-8000-000000000002"
	liveC = "CCCCCCCC-0000-4000-8000-000000000003"
)

func writeFixtures(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A Takeout clip filed under the name of a photo of another exposure, which
// a second phone numbered the same, is not that photo's Live Photo. A clip
// whose identifier matches, or whose photo records none, still is. Every
// name is a synthetic fixture.
func TestALiveClipOfAnotherExposureIsNotItsNamesakes(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	reads := 0
	s.liveIDReader = fakeLiveIDs(&reads)
	root := t.TempDir()
	writeFixtures(t, root, map[string]string{
		"2026/2026-03/2026-03-25/IMG_3043.HEIC":             "live:" + liveA,
		".live-photos/2026/2026-03/2026-03-25/IMG_3043.MP4": "live:" + liveB,
		"2026/2026-03/2026-03-25/IMG_3040.HEIC":             "live:" + liveC,
		".live-photos/2026/2026-03/2026-03-25/IMG_3040.MP4": "live:" + liveC,
		"2026/2026-03/2026-03-25/IMG_3041.JPG":              "no identifier",
		".live-photos/2026/2026-03/2026-03-25/IMG_3041.MP4": "live:" + liveA,
	})
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	names, _ := dayNames(t, s, "03-25")
	if want := []string{"IMG_3040.HEIC live", "IMG_3041.JPG live", "IMG_3043.HEIC"}; !slices.Equal(sorted(names), want) {
		t.Fatalf("the day shows %v, want %v", names, want)
	}
	if reads != 6 {
		t.Errorf("read %d identifiers, want the 6 files that pair by name", reads)
	}

	// The next scan reads nothing it has read, unless the file has changed.
	reads = 0
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	if reads != 0 {
		t.Errorf("a second scan read %d identifiers again", reads)
	}
	writeFixtures(t, root, map[string]string{".live-photos/2026/2026-03/2026-03-25/IMG_3043.MP4": "live:" + liveA})
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Errorf("read %d identifiers after one file changed", reads)
	}
	if names, _ = dayNames(t, s, "03-25"); !slices.Contains(names, "IMG_3043.HEIC live") {
		t.Errorf("the clip that now matches is not paired: %v", names)
	}
}

// A reader that fails leaves the pairs as the last scan found them, rather
// than trusting names unchecked.
func TestUnreadLiveIdentifiersChangeNoPairs(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	writeFixtures(t, root, map[string]string{
		"2026/2026-03/2026-03-25/IMG_3043.HEIC":             "live:" + liveA,
		".live-photos/2026/2026-03/2026-03-25/IMG_3043.MP4": "live:" + liveB,
	})
	reads := 0
	s.liveIDReader = fakeLiveIDs(&reads)
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	s.liveIDReader = func(context.Context, []string) (map[string]string, error) {
		return nil, errors.New("exiftool is not installed")
	}
	if _, err := s.write.Exec("DELETE FROM live_ids"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IndexLiveClips(ctx, root); err == nil {
		t.Fatal("pairs were rebuilt without their identifiers")
	}
	s.liveIDReader = func(_ context.Context, files []string) (map[string]string, error) { return map[string]string{}, nil }
	if _, err := s.IndexLiveClips(ctx, root); err == nil || !strings.Contains(err.Error(), "IMG_3043") {
		t.Fatalf("a file left unread was trusted: %v", err)
	}
	if names, _ := dayNames(t, s, "03-25"); !slices.Equal(names, []string{"IMG_3043.HEIC"}) {
		t.Errorf("the day shows %v", names)
	}
	// At start, pairing with what is known reads nothing, and a pair it
	// cannot confirm stands by name until the full pairing reads it.
	if _, err := s.IndexKnownLiveClips(ctx, root); err != nil {
		t.Fatalf("pairing with what is known read something: %v", err)
	}
}

// The Bin reads the identifiers again on disk. A photo leaves without a clip
// of another exposure filed under its name, and that clip can go to the Bin
// on its own; a batch planned before pairs were confirmed is refused rather
// than taking the wrong clip with it.
func TestTheBinTakesOnlyAPhotosOwnLiveClip(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	reads := 0
	s.liveIDReader = fakeLiveIDs(&reads)
	root := t.TempDir()
	wrong := ".live-photos/2026/2026-03/2026-03-25/IMG_3043.MP4"
	writeFixtures(t, root, map[string]string{
		"2026/2026-03/2026-03-25/IMG_3043.HEIC": "live:" + liveA,
		wrong:                                   "live:" + liveB,
		"2026/2026-03/2026-03-25/IMG_3040.HEIC": "live:" + liveC,
		".live-photos/2026/2026-03/2026-03-25/IMG_3040.MP4": "live:" + liveC,
	})
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2026/2026-03/2026-03-25/IMG_3043.HEIC',1,'image',41,'archive'),
		(2,'/archive/2026/2026-03/2026-03-25/IMG_3040.HEIC',1,'image',41,'archive');
		INSERT INTO decisions VALUES(1,'cull',0,1),(2,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	b, err := NewBinEngine(s, root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	p, err := b.Preview(ctx, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := liveFiles(p); !slices.Equal(got, []string{".live-photos/2026/2026-03/2026-03-25/IMG_3040.MP4"}) {
		t.Fatalf("the batch takes clips %v", got)
	}
	if p, err = b.Run(ctx, p.ID, "quarantine", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Run(ctx, p.ID, "purge", "DELETE 3"); err != nil {
		t.Fatal(err)
	}
	if !onDisk(t, root, wrong) {
		t.Fatal("the clip of another exposure was deleted with its namesake")
	}

	// With its namesake gone, that clip is a video of its own to bin.
	alone, err := b.PreviewVideos(ctx, []string{wrong})
	if err != nil {
		t.Fatal(err)
	}
	if len(alone.Files) != 1 || alone.Files[0].Original != wrong {
		t.Fatalf("the clip's batch: %+v", alone.Files)
	}

	// A batch planned by names alone, before this check, holds the wrong
	// clip. Moving it into the Bin is refused.
	writeFixtures(t, root, map[string]string{"2026/2026-03/2026-03-26/IMG_3050.HEIC": "live:" + liveA, ".live-photos/2026/2026-03/2026-03-26/IMG_3050.MP4": "live:" + liveB})
	if _, err = s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(3,'/archive/2026/2026-03/2026-03-26/IMG_3050.HEIC',1,'image',41,'archive');
		INSERT INTO decisions VALUES(3,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	s.liveIDReader = nil
	old, err := b.Preview(ctx, []int64{3})
	if err != nil {
		t.Fatal(err)
	}
	if len(liveFiles(old)) != 1 {
		t.Fatalf("by names alone the batch takes %v", liveFiles(old))
	}
	s.liveIDReader = fakeLiveIDs(&reads)
	if _, err = b.Run(ctx, old.ID, "quarantine", ""); err == nil || !strings.Contains(err.Error(), "not the Live Photo") {
		t.Fatalf("the wrong clip went into the Bin: %v", err)
	}
	for _, name := range []string{"2026/2026-03/2026-03-26/IMG_3050.HEIC", ".live-photos/2026/2026-03/2026-03-26/IMG_3050.MP4"} {
		if !onDisk(t, root, name) {
			t.Fatalf("%s left the archive", name)
		}
	}
}
