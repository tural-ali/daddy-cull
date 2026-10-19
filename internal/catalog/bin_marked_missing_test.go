package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// markedArchive lays out a synthetic day as the host left it: a Live Photo
// whose clip was first filed beside its photo and later moved into
// .live-photos, and two plain photos.
func markedArchive(t *testing.T) (string, *Store) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	write := func(name, body string) {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	day := "2026/2026-08/2026-08-30/"
	write(day+"IMG_5209.HEIC", "still")
	write(day+"IMG_5209_HEVC.MOV", "inline clip")
	write(day+"IMG_5210.JPG", "photo")
	write(day+"IMG_5211.JPG", "photo two")
	s := testStore(t)
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".live-photos", day), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, day, "IMG_5209_HEVC.MOV"), filepath.Join(root, ".live-photos", day, "IMG_5209_HEVC.MOV")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	return root, s
}

// The phone reports the clip where it was first filed. That row now names a
// file that is not there, so it is not marked: the photo takes the clip, from
// where it is now, to the Bin. Emptying the Bin then removes both.
func TestAPhoneDeletionOfAClipThatMovedEmptiesTheBin(t *testing.T) {
	ctx := context.Background()
	root, s := markedArchive(t)
	day := "2026/2026-08/2026-08-30/"
	line := func(rel string, size int) string {
		return root + "/" + rel + "\t" + strconv.Itoa(size) + "\tsam\t" + rel + "\t2026-10-17T23:00:00+00:00\n"
	}
	got, err := s.MarkPhoneDeletions(ctx, root, strings.NewReader(line(day+"IMG_5209.HEIC", 5)+line(day+"IMG_5209_HEVC.MOV", 11)+line(day+"IMG_5210.JPG", 5)))
	if err != nil {
		t.Fatal(err)
	}
	if want := (PhoneDeletionResult{Marked: 2, NotCatalogued: 1}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	bin, err := NewBinEngine(s, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bin.Close() })
	trash := NewTrashWriter(s, bin, nil, nil)
	if err = s.SetGraceDays(ctx, 0); err != nil {
		t.Fatal(err)
	}
	items, err := s.Trash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("the Bin shows %d files, want the 2 photos: %+v", len(items), items)
	}
	result, err := trash.Empty(ctx, DeleteConfirmation(len(items)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Done != 2 || len(result.Failures) != 0 {
		t.Fatalf("empty: %+v", result)
	}
	for _, name := range []string{day + "IMG_5209.HEIC", ".live-photos/" + day + "IMG_5209_HEVC.MOV", day + "IMG_5210.JPG"} {
		if exists(t, filepath.Join(root, name)) {
			t.Errorf("%s is still in the archive", name)
		}
	}
	if !exists(t, filepath.Join(root, day, "IMG_5211.JPG")) {
		t.Fatal("a photo nobody removed was deleted")
	}
}

// A file marked before it went missing is not offered for deletion: there is
// nothing to move. One that goes missing between two scans fails on its own,
// in plain words, and the files moved with it in the same run still go,
// whether the Bin keeps them for a while or deletes them at once.
func TestOneMissingFileDoesNotHoldBackTheRest(t *testing.T) {
	ctx := context.Background()
	day := "2026/2026-08/2026-08-30/"
	for _, grace := range []int{7, 0} {
		t.Run("grace "+strconv.Itoa(grace), func(t *testing.T) {
			root, s := markedArchive(t)
			var stale int64
			if err := s.read.QueryRow("SELECT id FROM assets WHERE relative_path=?", "/archive/"+day+"IMG_5209_HEVC.MOV").Scan(&stale); err != nil {
				t.Fatal(err)
			}
			// The stale row is marked the way an older import marked it.
			decisions := []Decision{{RequestID: "stale-clip", AssetID: stale, Status: "cull"}}
			for _, name := range []string{"IMG_5210.JPG", "IMG_5211.JPG"} {
				decisions = append(decisions, Decision{RequestID: "mark-" + name, AssetID: assetID(t, s, day+name), Status: "cull"})
			}
			if _, err := s.DecideBatch(ctx, decisions); err != nil {
				t.Fatal(err)
			}
			if err := s.SetGraceDays(ctx, grace); err != nil {
				t.Fatal(err)
			}
			items, err := s.Trash(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 {
				t.Fatalf("the Bin shows %d files, want the 2 on disk: %+v", len(items), items)
			}
			// IMG_5211 goes before the next scan notices.
			if err = os.Remove(filepath.Join(root, day, "IMG_5211.JPG")); err != nil {
				t.Fatal(err)
			}
			bin, err := NewBinEngine(s, root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { bin.Close() })
			result, err := NewTrashWriter(s, bin, nil, nil).Empty(ctx, DeleteConfirmation(len(items)))
			if err != nil {
				t.Fatal(err)
			}
			if result.Done != 1 || len(result.Failures) != 1 || result.Failures[0].Name != "IMG_5211.JPG" || result.Failures[0].Error != errNotInArchive.Error() {
				t.Fatalf("empty: %+v", result)
			}
			if exists(t, filepath.Join(root, day, "IMG_5210.JPG")) {
				t.Fatal("IMG_5210 was held back by a missing neighbour")
			}
			if items, err = s.Trash(ctx); err != nil || len(items) != 1 || items[0].Name != "IMG_5211.JPG" {
				t.Fatalf("after emptying the Bin holds %+v %v", items, err)
			}
		})
	}
}
