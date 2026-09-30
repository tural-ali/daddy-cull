package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// liveBinFixture is a Live Photo whose clip graduated beside it and was
// catalogued, with a second clip filed in .live-photos, and the HEIC and JPEG
// of one exposure that share a filed clip. Every name is a synthetic fixture.
func liveBinFixture(t *testing.T) (*BinEngine, *Store, string) {
	t.Helper()
	s := testStore(t)
	root := t.TempDir()
	for _, name := range []string{
		"2026/2026-09/2026-09-26/IMG_5472.HEIC", "2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV",
		".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4",
		"2022/2022-01/2022-01-07/IMG_6947.HEIC", "2022/2022-01/2022-01-07/IMG_6947.JPG",
		".live-photos/2022/2022-01/2022-01-07/IMG_6947_HEVC.MOV",
	} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if e := os.MkdirAll(filepath.Dir(full), 0o700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(full, []byte("family original"), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2026/2026-09/2026-09-26/IMG_5472.HEIC',1,'image',15,'archive'),
		(2,'/archive/2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV',1,'video',15,'archive'),
		(3,'/archive/2022/2022-01/2022-01-07/IMG_6947.HEIC',1,'image',15,'archive'),
		(4,'/archive/2022/2022-01/2022-01-07/IMG_6947.JPG',1,'image',15,'archive');
		INSERT INTO decisions VALUES(1,'cull',0,1),(3,'cull',0,1),(4,'cull',0,1)`); e != nil {
		t.Fatal(e)
	}
	b, e := NewBinEngine(s, root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	return b, s, root
}

func onDisk(t *testing.T, root, name string) bool {
	t.Helper()
	_, e := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	return e == nil
}

func liveFiles(p *BinPlan) []string {
	var clips []string
	for _, f := range p.Files {
		if f.Live {
			clips = append(clips, f.Original)
		}
	}
	return clips
}

// A photo takes its Live Photo clips to the Bin, wherever they are filed, and
// brings them back; the catalogued clip is in the Bin with it meanwhile.
func TestLivePhotoClipsGoWhereThePhotoGoes(t *testing.T) {
	b, s, root := liveBinFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if got := strings.Join(liveFiles(p), " "); got != ".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4 2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV" {
		t.Fatalf("the batch takes clips %q", got)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"2026/2026-09/2026-09-26/IMG_5472.HEIC", "2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV", ".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4"} {
		if onDisk(t, root, name) {
			t.Errorf("%s stayed behind", name)
		}
	}
	if state := fileState(t, s, 2); state != "bin" {
		t.Errorf("the catalogued clip is %q, not in the Bin", state)
	}
	items := binPlanItems(*p)
	if len(items) != 1 || !items[0].Live || items[0].Sidecars != 0 || items[0].Size != 45 {
		t.Errorf("the Bin card: %+v", items)
	}
	// The Bin can show the clip, filed in .live-photos or not.
	for i, f := range p.Files {
		if f.Live {
			if _, _, found := s.binnedFile(ctx, "bin", p.ID, i); !found {
				t.Errorf("the Bin cannot show %s", f.Original)
			}
		}
	}
	if p, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV", ".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4"} {
		if !onDisk(t, root, name) {
			t.Errorf("%s did not come back", name)
		}
	}
	if state := fileState(t, s, 2); state != "restored" {
		t.Errorf("the restored clip is %q", state)
	}
}

// A clip the HEIC and JPEG of one exposure share stays while either is in the
// archive, comes back with whichever is given back first, and is deleted only
// with the last of them.
func TestASharedLiveClipLeavesWithTheLastPhoto(t *testing.T) {
	b, _, root := liveBinFixture(t)
	ctx := context.Background()
	clip := ".live-photos/2022/2022-01/2022-01-07/IMG_6947_HEVC.MOV"
	p, e := b.Preview(ctx, []int64{3})
	if e != nil {
		t.Fatal(e)
	}
	if len(liveFiles(p)) != 0 || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], clip) {
		t.Fatalf("the HEIC alone takes %v, warning %v", liveFiles(p), p.Warnings)
	}
	p, e = b.Preview(ctx, []int64{3, 4})
	if e != nil {
		t.Fatal(e)
	}
	if got := liveFiles(p); len(got) != 1 || got[0] != clip {
		t.Fatalf("both take %v", got)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	if onDisk(t, root, clip) {
		t.Fatal("the clip stayed behind with both photos gone")
	}
	// The JPEG comes back, and brings the clip it shares with the HEIC.
	if p, e = b.Return(ctx, p.ID, 4); e != nil {
		t.Fatal(e)
	}
	if !onDisk(t, root, clip) || !onDisk(t, root, "2022/2022-01/2022-01-07/IMG_6947.JPG") {
		t.Fatal("the JPEG came back without its clip")
	}
	if p, e = b.Run(ctx, p.ID, "purge", "DELETE 3"); e != nil {
		t.Fatal(e)
	}
	if !onDisk(t, root, clip) {
		t.Fatal("deleting the HEIC deleted the JPEG's clip")
	}
}

// A photo that returns to the archive while its twin's batch waits in the Bin
// keeps the clip from being deleted.
func TestALiveClipIsNotDeletedFromUnderAPhoto(t *testing.T) {
	b, s, root := liveBinFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	// Another copy of the photo graduates and is catalogued.
	if e = os.WriteFile(filepath.Join(root, "2026/2026-09/2026-09-26/IMG_5472.JPG"), []byte("new copy"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(5,'/archive/2026/2026-09/2026-09-26/IMG_5472.JPG',1,'image',8,'archive')"); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 3"); e == nil || !strings.Contains(e.Error(), "Live Photo video") {
		t.Fatalf("the clip was deleted from under the new copy: %v", e)
	}
	if p, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
		t.Fatal(e)
	}
	if !onDisk(t, root, ".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4") {
		t.Fatal("the clip did not come back")
	}
}
