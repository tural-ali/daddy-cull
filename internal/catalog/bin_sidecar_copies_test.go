package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An export kept beside the camera's file, as Photos writes one with its
// sidecars, goes to the Bin with them, and the camera's file, which is kept,
// gets a copy of each under its own name. Restoring the export brings its own
// back, and the copies stay. Every name is a synthetic fixture.
func TestBinCopiesSidecarsToTheCopyKept(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2022/2022-07/2022-07-03")
	if e := os.MkdirAll(day, 0o700); e != nil {
		t.Fatal(e)
	}
	files := map[string]string{
		"IMG_9001.MOV":                  "camera footage",
		"IMG_9001 (2022-07-03).MOV":     "export footage",
		"IMG_9001 (2022-07-03).MOV.xmp": "<x:xmpmeta>a person</x:xmpmeta>",
		"IMG_9001 (2022-07-03).json":    `[{"XMP:PersonInImage":["Test Person"]}]`,
		"IMG_9001 (2022-07-03).MOV.thm": "a thumbnail",
		"IMG_9001 (2).MOV":              "camera footage",
	}
	for name, body := range files {
		if e := os.WriteFile(filepath.Join(day, name), []byte(body), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	for _, row := range []struct {
		id     int64
		name   string
		status string
	}{{1, "IMG_9001.MOV", "keep"}, {2, "IMG_9001 (2022-07-03).MOV", "cull"}, {3, "IMG_9001 (2).MOV", "cull"}} {
		if _, e := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'video',?,'archive')", row.id, "/archive/2022/2022-07/2022-07-03/"+row.name, len(files[row.name])); e != nil {
			t.Fatal(e)
		}
		if _, e := s.write.Exec("INSERT INTO decisions VALUES(?,?,0,1)", row.id, row.status); e != nil {
			t.Fatal(e)
		}
		if _, e := s.write.Exec("INSERT INTO asset_footage(asset_id,size_bytes,mtime,media_bytes,playback_hash,footage_hash,read_at) VALUES(?,?,0,10,'p','same','now')", row.id, len(files[row.name])); e != nil {
			t.Fatal(e)
		}
	}
	b, e := NewBinEngine(s, root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	// With two copies staying there is no one copy to give the sidecars to,
	// whether or not they have been reviewed.
	if _, e = s.write.Exec("UPDATE decisions SET status='unreviewed' WHERE asset_id=3"); e != nil {
		t.Fatal(e)
	}
	if p, e := b.Preview(ctx, []int64{2}); e != nil || len(p.Copies) != 0 {
		t.Fatalf("copies planned with two copies kept: %+v %v", p, e)
	}
	if _, e = s.write.Exec("UPDATE decisions SET status='cull' WHERE asset_id=3"); e != nil {
		t.Fatal(e)
	}
	p, e := b.Preview(ctx, []int64{2, 3})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Copies) != 2 || p.Copies[0].Keeper != "2022/2022-07/2022-07-03/IMG_9001.MOV" {
		t.Fatalf("copies: %+v", p.Copies)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]string{
		"IMG_9001.MOV.xmp":  files["IMG_9001 (2022-07-03).MOV.xmp"],
		"IMG_9001.MOV.json": files["IMG_9001 (2022-07-03).json"],
	} {
		if got, e := os.ReadFile(filepath.Join(day, name)); e != nil || string(got) != want {
			t.Errorf("%s: %q, %v", name, got, e)
		}
	}
	for _, name := range []string{"IMG_9001.MOV.thm", "IMG_9001 (2022-07-03).MOV.xmp", "IMG_9001 (2022-07-03).json"} {
		if _, e := os.Stat(filepath.Join(day, name)); !os.IsNotExist(e) {
			t.Errorf("%s: %v", name, e)
		}
	}
	if _, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"IMG_9001 (2022-07-03).MOV.xmp", "IMG_9001.MOV.xmp", "IMG_9001.MOV.json"} {
		if _, e := os.Stat(filepath.Join(day, name)); e != nil {
			t.Errorf("after restoring, %s: %v", name, e)
		}
	}
	// The kept copy has sidecars now, so a second time nothing is copied.
	var revision int64
	s.read.QueryRow("SELECT revision FROM decisions WHERE asset_id=2").Scan(&revision)
	if _, e = s.Decide(ctx, Decision{AssetID: 2, RequestID: "cull-again", ExpectedRevision: revision, Status: "cull"}); e != nil {
		t.Fatal(e)
	}
	if p, e = b.Preview(ctx, []int64{2}); e != nil || len(p.Copies) != 0 {
		t.Fatalf("copies planned again: %+v %v", p, e)
	}
}
