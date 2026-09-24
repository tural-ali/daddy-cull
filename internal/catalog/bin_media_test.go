package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// culledArchive lays out an archive mount holding the files a Bin row names,
// and returns the mount root.
func culledArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A culled file has not left the archive share, so it can still be looked at.
// Deciding whether to restore or destroy a photograph from its filename alone is
// not a real choice, which is the whole reason this route exists.
func TestLegacyBinServesTheStoredFile(t *testing.T) {
	s := testStore(t)
	archive := culledArchive(t, map[string]string{".culled/2021-12-18/IMG_0016.mp4": "the culled clip"})
	if _, err := s.write.Exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,size_bytes,culled_at) VALUES
		(1,'b','media','/disks/disk1/2021/2021-12/2021-12-18/IMG_0016.mp4','/disks/disk1/.culled/2021-12-18/IMG_0016.mp4',15,'2026-08-30T00:00:00+00:00')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LegacyBinMediaHandler(MediaRoots{Archive: archive})
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "the culled clip" {
		t.Fatalf("served %d %q", got.Code, got.Body.String())
	}
}

// Two Bin rows can name the same path on two disks. The merged share exposes
// only one of them and nothing here can tell which, so the card stays honestly
// blank rather than putting one photograph's picture on another's delete button.
func TestLegacyBinRefusesAShadowedPair(t *testing.T) {
	s := testStore(t)
	archive := culledArchive(t, map[string]string{
		".culled/2022-03-24/B612.mp4": "whichever half the share picked",
		".culled/2022-03-06/B612.mp4": "the only copy of this one",
	})
	if _, err := s.write.Exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,size_bytes,culled_at,restored_at) VALUES
		(1,'b','media','/x','/disks/cache/.culled/2022-03-24/B612.mp4',15,'2026-08-30T00:00:00+00:00',NULL),
		(2,'b','media','/x','/disks/disk1/.culled/2022-03-24/B612.mp4',15,'2026-08-30T00:00:00+00:00',NULL),
		(3,'b','media','/x','/disks/cache/.culled/2022-03-06/B612.mp4',15,'2026-08-30T00:00:00+00:00',NULL),
		(4,'b','media','/x','/disks/disk1/.culled/2022-03-06/B612.mp4',15,'2026-08-30T00:00:00+00:00','2026-08-30T01:00:00+00:00')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LegacyBinMediaHandler(MediaRoots{Archive: archive})
	for _, id := range []string{"1", "2"} {
		if got := serveMedia(t, handler, id, "original", nil); got.Code != 404 {
			t.Fatalf("shadowed row %s served %d %q, wanted an honest miss", id, got.Code, got.Body.String())
		}
	}
	// Row 4 was restored, so its file has already left that path and it cannot
	// be the rival that hides row 3.
	if got := serveMedia(t, handler, "3", "original", nil); got.Code != 200 || got.Body.String() != "the only copy of this one" {
		t.Fatalf("a restored twin hid a file still in the Bin: %d %q", got.Code, got.Body.String())
	}
}

// With the physical disks mounted there is nothing to guess: each row names its
// own disk, so both halves of a shadowed pair are shown as themselves.
func TestLegacyBinServesEachHalfFromItsOwnDisk(t *testing.T) {
	s := testStore(t)
	archive := culledArchive(t, map[string]string{".culled/2022-03-24/B612.mp4": "whichever half the share picked"})
	disks := culledArchive(t, map[string]string{
		"cache/.culled/2022-03-24/B612.mp4": "the cache copy",
		"disk1/.culled/2022-03-24/B612.mp4": "the disk1 copy",
	})
	if _, err := s.write.Exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,size_bytes,culled_at) VALUES
		(1,'b','media','/x','/disks/cache/.culled/2022-03-24/B612.mp4',15,'2026-08-30T00:00:00+00:00'),
		(2,'b','media','/x','/disks/disk1/.culled/2022-03-24/B612.mp4',15,'2026-08-30T00:00:00+00:00')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LegacyBinMediaHandler(MediaRoots{Archive: archive, Disks: disks})
	for id, want := range map[string]string{"1": "the cache copy", "2": "the disk1 copy"} {
		if got := serveMedia(t, handler, id, "original", nil); got.Code != 200 || got.Body.String() != want {
			t.Fatalf("row %s served %d %q, wanted %q", id, got.Code, got.Body.String(), want)
		}
	}
}

// Unraid's mover migrates the cache onto the array, so a file the history
// recorded on the cache can since have moved to a disk. The recorded disk is
// tried first, and when the file is no longer there the share still finds it.
func TestLegacyBinFindsAFileTheMoverMoved(t *testing.T) {
	s := testStore(t)
	archive := culledArchive(t, map[string]string{".culled/2022-03-06/B612.mp4": "moved to disk1 overnight"})
	disks := culledArchive(t, map[string]string{"disk1/.culled/2022-03-06/B612.mp4": "moved to disk1 overnight"})
	if _, err := s.write.Exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,size_bytes,culled_at) VALUES
		(1,'b','media','/x','/disks/cache/.culled/2022-03-06/B612.mp4',24,'2026-08-30T00:00:00+00:00')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LegacyBinMediaHandler(MediaRoots{Archive: archive, Disks: disks})
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "moved to disk1 overnight" {
		t.Fatalf("a file the mover moved went missing: %d %q", got.Code, got.Body.String())
	}
}

// A restored or purged row names a path its file has left, so the route must not
// serve whatever happens to sit there now.
func TestLegacyBinServesOnlyWhatItStillHolds(t *testing.T) {
	s := testStore(t)
	archive := culledArchive(t, map[string]string{".culled/2021-12-18/IMG_0016.mp4": "something else entirely"})
	if _, err := s.write.Exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,size_bytes,culled_at,restored_at,purged_at) VALUES
		(1,'b','media','/x','/disks/disk1/.culled/2021-12-18/IMG_0016.mp4',15,'2026-08-30T00:00:00+00:00','2026-08-30T01:00:00+00:00',NULL),
		(2,'b','media','/x','/disks/disk1/.culled/2021-12-18/IMG_0016.mp4',15,'2026-08-30T00:00:00+00:00',NULL,'2026-08-30T01:00:00+00:00')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LegacyBinMediaHandler(MediaRoots{Archive: archive})
	for _, id := range []string{"1", "2"} {
		if got := serveMedia(t, handler, id, "original", nil); got.Code != 404 {
			t.Fatalf("row %s is no longer in the Bin but served %d", id, got.Code)
		}
	}
}

// The Bin and the catalogue both count from one, and their previews share one
// cache directory, so row 7 and asset 7 must not be able to collide there.
func TestBinPreviewsAreCachedApartFromAssetPreviews(t *testing.T) {
	cache := t.TempDir()
	build := func(subject, body string) {
		if _, err := cachedBytes(cache, "tile", subject, 15, 99, func() ([]byte, error) { return []byte(body), nil }); err != nil {
			t.Fatal(err)
		}
	}
	build("7", "the asset")
	build("bin/7", "the Bin row")
	got, err := cachedBytes(cache, "tile", "7", 15, 99, func() ([]byte, error) { return nil, os.ErrNotExist })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the asset" {
		t.Fatalf("a Bin preview overwrote an asset preview: %q", got)
	}
}
