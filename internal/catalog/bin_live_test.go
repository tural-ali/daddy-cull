package catalog

import (
	"context"
	"errors"
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

// A batch that went to the Bin before the Bin took Live Photo videos left
// them in the archive. They join it, come back when it is restored, and are
// deleted with it; one a photograph still in the archive shares stays.
func TestLiveClipsLeftBehindJoinTheirPhotosInTheBin(t *testing.T) {
	b, s, root := liveBinFixture(t)
	ctx := context.Background()
	filed := ".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4"
	inline := "2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV"
	shared := ".live-photos/2022/2022-01/2022-01-07/IMG_6947_HEVC.MOV"
	// The batches as the Bin made them before: the photos alone.
	hidden := filepath.Join(t.TempDir(), "hidden")
	for _, name := range []string{filed, inline, shared} {
		if e := os.MkdirAll(filepath.Join(hidden, filepath.Dir(name)), 0o700); e != nil {
			t.Fatal(e)
		}
		if e := os.Rename(filepath.Join(root, name), filepath.Join(hidden, name)); e != nil {
			t.Fatal(e)
		}
	}
	old, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if old, e = b.Run(ctx, old.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	heic, e := b.Preview(ctx, []int64{3})
	if e != nil {
		t.Fatal(e)
	}
	if heic, e = b.Run(ctx, heic.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{filed, inline, shared} {
		if e := os.Rename(filepath.Join(hidden, name), filepath.Join(root, name)); e != nil {
			t.Fatal(e)
		}
	}

	// A move cut short is finished when asked again.
	cut := true
	b.checkpoint = func(label string) error {
		if label == "after-link" && cut {
			cut = false
			return errors.New("power cut")
		}
		return nil
	}
	if _, e = b.AdoptAllLiveClips(ctx); e == nil {
		t.Fatal("the cut was not noticed")
	}
	b.checkpoint = nil
	n, e := b.AdoptAllLiveClips(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if n != 2 {
		t.Errorf("%d videos moved in after the cut, want both of the photo's", n)
	}
	if old, e = b.load(old.ID); e != nil {
		t.Fatal(e)
	}
	if got := strings.Join(liveFiles(old), " "); got != filed+" "+inline {
		t.Fatalf("the batch took %q", got)
	}
	for _, f := range old.Files {
		if f.Phase != "bin" || old.Error != "" {
			t.Fatalf("%s is %s, error %q", f.Original, f.Phase, old.Error)
		}
	}
	if onDisk(t, root, filed) || onDisk(t, root, inline) {
		t.Fatal("a video stayed in the archive")
	}
	if state := fileState(t, s, 2); state != "bin" {
		t.Errorf("the catalogued video is %q", state)
	}
	// The JPEG of the other exposure is in the archive, so their video stays.
	if heic, e = b.load(heic.ID); e != nil {
		t.Fatal(e)
	}
	if len(liveFiles(heic)) != 0 || !onDisk(t, root, shared) {
		t.Fatal("the video the JPEG shares left with the HEIC")
	}
	if n, e = b.AdoptAllLiveClips(ctx); e != nil || n != 0 {
		t.Fatalf("asked again, %d joined: %v", n, e)
	}

	if old, e = b.Run(ctx, old.ID, "restore", ""); e != nil {
		t.Fatal(e)
	}
	if !onDisk(t, root, filed) || !onDisk(t, root, inline) {
		t.Fatal("restoring the photo left its videos in the Bin")
	}
}

// Deleting a batch from the Bin deletes the videos its photos left behind.
func TestDeletingABatchTakesTheVideosItLeftBehind(t *testing.T) {
	b, s, root := liveBinFixture(t)
	ctx := context.Background()
	clip := ".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4"
	aside := filepath.Join(t.TempDir(), "IMG_5472.MP4")
	if e := os.Rename(filepath.Join(root, clip), aside); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(root, "2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.write.Exec("DELETE FROM assets WHERE id=2"); e != nil {
		t.Fatal(e)
	}
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(aside, filepath.Join(root, clip)); e != nil {
		t.Fatal(e)
	}
	trash := NewTrashWriter(s, b, nil, nil)
	if e = trash.binPlan(ctx, p.ID, true); e != nil {
		t.Fatal(e)
	}
	if onDisk(t, root, clip) {
		t.Fatal("the video outlived its photo")
	}
	if p, e = b.load(p.ID); e != nil || p.State != "purged" || len(liveFiles(p)) != 1 {
		t.Fatalf("the batch: %+v %v", p, e)
	}
}

// A Live Photo video whose photo has gone goes to the Bin by itself, as a card
// of its own that comes back or is deleted like any other. One a photo in the
// archive still has is refused, and so is anything outside .live-photos.
func TestALiveVideoWhosePhotoHasGoneGoesToTheBinAlone(t *testing.T) {
	b, s, root := liveBinFixture(t)
	ctx := context.Background()
	orphan := ".live-photos/2022/2022-08/2022-08-03/IMG_6390_HEVC.MOV"
	full := filepath.Join(root, filepath.FromSlash(orphan))
	if e := os.MkdirAll(filepath.Dir(full), 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, []byte("a Live Photo video"), 0o600); e != nil {
		t.Fatal(e)
	}
	for _, refused := range [][]string{
		{".live-photos/2022/2022-01/2022-01-07/IMG_6947_HEVC.MOV"},
		{"2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV"},
		{".live-photos/../2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV"},
		{".live-photos/2022/2022-08/2022-08-03/IMG_6390.HEIC"},
		{orphan, orphan},
		{},
	} {
		if p, e := b.PreviewVideos(ctx, refused); e == nil {
			t.Fatalf("%q was planned: %+v", refused, p)
		}
	}

	p, e := b.PreviewVideos(ctx, []string{orphan})
	if e != nil {
		t.Fatal(e)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil || p.State != "bin" || onDisk(t, root, orphan) {
		t.Fatalf("quarantine: %+v %v", p, e)
	}
	items, e := s.Trash(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var card *TrashItem
	for i := range items {
		if items[i].Group == "bin:"+p.ID {
			card = &items[i]
		}
	}
	if card == nil || card.Key != "bin:"+p.ID+":v0" || card.Kind != "video" || !card.Live || card.Original != "/archive/"+orphan || card.Preview != "/api/binned-media/bin/"+p.ID+"/0" {
		t.Fatalf("the Bin shows %+v", card)
	}
	if relative, name, found := s.binnedFile(ctx, "bin", p.ID, 0); !found || name != orphan || !strings.HasPrefix(relative, "/archive/.culled/next/"+p.ID+"/") {
		t.Fatalf("binned media %q %q %v", relative, name, found)
	}

	trash := NewTrashWriter(s, b, nil, nil)
	if result, e := trash.RestoreFile(ctx, card.Key); e != nil || result.Done != 1 || !onDisk(t, root, orphan) {
		t.Fatalf("restore: %+v %v", result, e)
	}
	if p, e = b.load(p.ID); e != nil || p.State != "restored" {
		t.Fatalf("restored batch: %+v %v", p, e)
	}

	// A photo that turns up before the move keeps its video.
	p, e = b.PreviewVideos(ctx, []string{orphan})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(root, "2022/2022-08/2022-08-03"), 0o700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "2022/2022-08/2022-08-03/IMG_6390.HEIC"), []byte("its photo"), 0o600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(9,'/archive/2022/2022-08/2022-08-03/IMG_6390.HEIC',1,'image',9,'archive')"); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil || !onDisk(t, root, orphan) {
		t.Fatalf("the video left without its photo: %v", e)
	}
	if _, e = s.write.Exec("DELETE FROM assets WHERE id=9"); e != nil {
		t.Fatal(e)
	}

	// Deleted, it is gone for good, the batch saying so for the Drive clone.
	p, e = b.PreviewVideos(ctx, []string{orphan})
	if e != nil {
		t.Fatal(e)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	if e = trash.binPlan(ctx, p.ID, true); e != nil {
		t.Fatal(e)
	}
	if p, e = b.load(p.ID); e != nil || p.State != "purged" || p.Files[0].Phase != "purged" || onDisk(t, root, orphan) {
		t.Fatalf("purge: %+v %v", p, e)
	}
}
