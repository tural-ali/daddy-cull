package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// returnFixture is one writer batch of two photographs, each with a sidecar,
// already in the Bin, beside a file that was never selected.
func returnFixture(t *testing.T) (*BinEngine, *Store, string, *BinPlan) {
	t.Helper()
	s := testStore(t)
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "2020/day"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp", "B.jpg", "B.jpg.xmp", "KEEP.jpg"} {
		if e := os.WriteFile(filepath.Join(root, "2020/day", name), []byte("family original"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2020/day/A.jpg',1,'image',15,'archive'),
		(2,'/archive/2020/day/B.jpg',1,'image',15,'archive');
		INSERT INTO decisions VALUES(1,'cull',0,1),(2,'cull',0,1)`); e != nil {
		t.Fatal(e)
	}
	b, e := NewBinEngine(s, root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1, 2})
	if e != nil {
		t.Fatal(e)
	}
	if p, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	return b, s, root, p
}

func present(t *testing.T, root, name string) bool {
	t.Helper()
	_, e := os.Stat(filepath.Join(root, "2020/day", name))
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	return e == nil
}

func fileState(t *testing.T, s *Store, asset int64) string {
	t.Helper()
	var state string
	if e := s.read.QueryRow("SELECT state FROM file_state WHERE asset_id=?", asset).Scan(&state); e != nil {
		t.Fatal(e)
	}
	return state
}

func TestBinReturnGivesBackOnePhotographAndItsSidecar(t *testing.T) {
	b, s, root, p := returnFixture(t)
	ctx := context.Background()
	got, e := b.Return(ctx, p.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if !present(t, root, "A.jpg") || !present(t, root, "A.jpg.xmp") {
		t.Fatal("the photograph or its sidecar did not come back")
	}
	if present(t, root, "B.jpg") || present(t, root, "B.jpg.xmp") {
		t.Fatal("the rest of the batch left the Bin")
	}
	if got.State != "bin" {
		t.Fatalf("batch state %q, want bin", got.State)
	}
	if fileState(t, s, 1) != "restored" || fileState(t, s, 2) != "bin" {
		t.Fatal("catalogue state does not match the files")
	}
	var status string
	s.read.QueryRow("SELECT status FROM decisions WHERE asset_id=1").Scan(&status)
	if status != "unreviewed" {
		t.Fatalf("returned photograph is %q, want unreviewed", status)
	}
	items := binPlanItems(*got)
	if len(items) != 1 || items[0].assetID != 2 || items[0].Sidecars != 1 || items[0].Size != 30 {
		t.Fatalf("Bin cards after return: %+v", items)
	}
	if _, e = b.Return(ctx, p.ID, 1); e != nil {
		t.Fatal("asking again should be harmless", e)
	}
}

func TestBinPurgeAfterReturnLeavesTheReturnedPhotograph(t *testing.T) {
	b, s, root, p := returnFixture(t)
	ctx := context.Background()
	if _, e := b.Return(ctx, p.ID, 1); e != nil {
		t.Fatal(e)
	}
	got, e := b.Run(ctx, p.ID, "purge", DeleteConfirmation(len(p.Files)))
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "purged" {
		t.Fatalf("batch state %q, want purged", got.State)
	}
	if !present(t, root, "A.jpg") || !present(t, root, "A.jpg.xmp") || !present(t, root, "KEEP.jpg") {
		t.Fatal("purging the rest touched a file that was not in the Bin")
	}
	if fileState(t, s, 1) != "restored" {
		t.Fatal("finishing the batch claimed the returned photograph back")
	}
	if fileState(t, s, 2) != "purged" {
		t.Fatal("the purged photograph is not recorded as purged")
	}
}

func TestBinRestoreAfterReturnIgnoresTheReturnedPhotograph(t *testing.T) {
	b, s, root, p := returnFixture(t)
	ctx := context.Background()
	if _, e := b.Return(ctx, p.ID, 1); e != nil {
		t.Fatal(e)
	}
	// Once back, the photograph is the reviewer's to edit or decide again.
	if e := os.WriteFile(filepath.Join(root, "2020/day/A.jpg"), []byte("edited since"), 0600); e != nil {
		t.Fatal(e)
	}
	var revision int64
	s.read.QueryRow("SELECT revision FROM decisions WHERE asset_id=1").Scan(&revision)
	if _, e := s.Decide(ctx, Decision{AssetID: 1, RequestID: "kept-after-return", ExpectedRevision: revision, Status: "keep"}); e != nil {
		t.Fatal(e)
	}
	got, e := b.Run(ctx, p.ID, "restore", "")
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "restored" || !present(t, root, "B.jpg") || !present(t, root, "B.jpg.xmp") {
		t.Fatal("the rest of the batch did not come back")
	}
	var status string
	s.read.QueryRow("SELECT status FROM decisions WHERE asset_id=1").Scan(&status)
	if status != "keep" {
		t.Fatalf("restoring the batch reset a decision made after the return: %q", status)
	}
}

func TestBinReturningEveryPhotographFinishesTheBatch(t *testing.T) {
	b, _, _, p := returnFixture(t)
	ctx := context.Background()
	if _, e := b.Return(ctx, p.ID, 2); e != nil {
		t.Fatal(e)
	}
	got, e := b.Return(ctx, p.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if got.State != "restored" {
		t.Fatalf("batch state %q, want restored", got.State)
	}
}

func TestBinReturnRefusesAnOccupiedDestination(t *testing.T) {
	b, _, root, p := returnFixture(t)
	if e := os.WriteFile(filepath.Join(root, "2020/day/A.jpg.xmp"), []byte("someone else's"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := b.Return(context.Background(), p.ID, 1); e == nil {
		t.Fatal("an occupied destination was accepted")
	}
	if present(t, root, "A.jpg") {
		t.Fatal("a file moved although the preflight failed")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "2020/day/A.jpg.xmp")); string(data) != "someone else's" {
		t.Fatal("the occupying file was overwritten")
	}
}

func TestBinReturnRefusesAnotherBatchesPhotograph(t *testing.T) {
	b, _, _, p := returnFixture(t)
	if _, e := b.Return(context.Background(), p.ID, 99); e == nil {
		t.Fatal("a photograph outside the batch was accepted")
	}
}

func TestBinInterruptedReturnResumes(t *testing.T) {
	for _, phase := range []string{"after-link", "after-unlink"} {
		t.Run(phase, func(t *testing.T) {
			b, s, root, p := returnFixture(t)
			ctx := context.Background()
			b.checkpoint = func(label string) error {
				if label == phase {
					return errors.New("simulated interruption")
				}
				return nil
			}
			if _, e := b.Return(ctx, p.ID, 1); e == nil {
				t.Fatal("injection missed")
			}
			b.checkpoint = nil
			// Half moved, the batch's own actions refuse rather than guess.
			if _, e := b.Run(ctx, p.ID, "purge", DeleteConfirmation(len(p.Files))); e == nil {
				t.Fatal("purge went ahead over a half-returned photograph")
			}
			if _, e := b.Return(ctx, p.ID, 1); e != nil {
				t.Fatal("resume failed", e)
			}
			if data, _ := os.ReadFile(filepath.Join(root, "2020/day/A.jpg")); string(data) != "family original" {
				t.Fatal("bytes changed")
			}
			if !present(t, root, "A.jpg.xmp") || fileState(t, s, 1) != "restored" {
				t.Fatal("the return did not finish")
			}
		})
	}
}

func TestTrashRestoreFileKeepsTheRestOfTheBatchScheduled(t *testing.T) {
	f := newTrashFixture(t)
	ctx := context.Background()
	group := "bin:" + f.binPlan
	if err := f.s.scheduleDeletion(ctx, []string{group}, time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := f.trash.RestoreFile(ctx, group+":2")
	if err != nil || result.Done != 1 || len(result.Failures) != 0 {
		t.Fatalf("restore one: %+v %v", result, err)
	}
	if !exists(t, filepath.Join(f.archive, "2020/day/B.jpg")) || exists(t, filepath.Join(f.archive, "2020/day/C.jpg")) {
		t.Fatal("restore one moved the wrong files")
	}
	scheduled, err := f.s.scheduledGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := scheduled[group]; !ok {
		t.Fatal("the rest of the batch lost its deletion date")
	}
	if _, err = f.trash.RestoreFile(ctx, group+":2"); err == nil {
		t.Fatal("a file no longer in the Bin was accepted")
	}
	if _, err = f.trash.RestoreFile(ctx, group+":3"); err != nil {
		t.Fatal(err)
	}
	if scheduled, err = f.s.scheduledGroups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := scheduled[group]; ok {
		t.Fatal("an emptied batch is still scheduled for deletion")
	}
}
