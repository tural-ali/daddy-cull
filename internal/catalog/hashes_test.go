package catalog

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// Files that share a size with another are hashed through the read-only
// mount until the Duplicates page can prove which are copies. A file of a
// size no other file has, one in the Bin, one missing, or one whose size on
// disk is not the size catalogued, is left alone. Every name is a synthetic
// fixture.
func TestDuplicateCandidatesAreHashed(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2019", "2019-08", "2019-08-14")
	other := filepath.Join(root, "2019", "2019-12", "2019-12-26")
	for _, dir := range []string{day, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	same := []byte("the same twelve bytes")
	files := []struct {
		id   int64
		path string
		body []byte
	}{
		{1, filepath.Join(day, "IMG_0001.JPG"), same},
		{2, filepath.Join(other, "Copy of IMG_0001.JPG"), same},
		{3, filepath.Join(day, "IMG_0002.JPG"), []byte("different bytes, same size")[:len(same)]},
		{4, filepath.Join(day, "ALONE.JPG"), []byte("a size of its own")},
		{5, filepath.Join(day, "BINNED.JPG"), same},
		{6, filepath.Join(day, "CHANGED.JPG"), []byte("now longer than catalogued")},
	}
	for _, f := range files {
		if err := os.WriteFile(f.path, f.body, 0o644); err != nil {
			t.Fatal(err)
		}
		size := int64(len(same))
		if f.id == 4 {
			size = int64(len(f.body))
		}
		relative, _ := filepath.Rel(root, f.path)
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',?,'archive')", f.id, "/archive/"+filepath.ToSlash(relative), size); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,?)", f.id, filepath.Base(filepath.Dir(f.path))); err != nil {
			t.Fatal(err)
		}
	}
	// Catalogued, but gone from the disk.
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(7,'/archive/2019/2019-08/2019-08-14/GONE.JPG',1,'image',?,'archive')", len(same)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) VALUES(5,'bin','plan')"); err != nil {
		t.Fatal(err)
	}
	roots := MediaRoots{Archive: root}

	hashed, err := s.FillHashes(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	if hashed != 3 {
		t.Fatalf("hashed %d, want the copy, its original and the file of the same size", hashed)
	}
	report, err := s.DuplicateOverview(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Groups) != 1 || len(report.Groups[0].Members) != 2 {
		t.Fatalf("groups %+v, want the one pair of copies", report.Groups)
	}
	var hash string
	want := md5.Sum(same)
	if err = s.read.QueryRowContext(ctx, "SELECT full_hash FROM asset_evidence WHERE asset_id=1").Scan(&hash); err != nil || hash != hex.EncodeToString(want[:]) {
		t.Fatalf("hash %q (%v), want the MD5 that imported evidence holds", hash, err)
	}
	if report.Settled {
		t.Fatal("settled while the changed and missing files are unhashed")
	}

	// Nothing new, so nothing is read again.
	if hashed, err = s.FillHashes(ctx, roots); err != nil || hashed != 0 {
		t.Fatalf("second pass hashed %d (%v), want 0", hashed, err)
	}
	// Once the catalogue has the changed file's real size, it has a size of its
	// own and needs no hash; the missing file is marked missing by a scan.
	if _, err = s.write.ExecContext(ctx, "UPDATE assets SET size_bytes=? WHERE id=6", len("now longer than catalogued")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO missing_assets(asset_id,since) VALUES(7,datetime('now'))"); err != nil {
		t.Fatal(err)
	}
	if report, err = s.DuplicateOverview(ctx, "", 50); err != nil || !report.Settled {
		t.Fatalf("settled %v (%v), want every candidate hashed", report.Settled, err)
	}
}
