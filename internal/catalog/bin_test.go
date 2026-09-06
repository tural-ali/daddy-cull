package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func binFixture(t *testing.T) (*BinEngine, *Store, string) {
	t.Helper()
	s := testStore(t)
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "2020/day"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp", "KEEP.jpg"} {
		if e := os.WriteFile(filepath.Join(root, "2020/day", name), []byte("family original"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	_, e := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2020/day/A.jpg',1,'image',15,'archive'); INSERT INTO decisions VALUES(1,'cull',0,1)")
	if e != nil {
		t.Fatal(e)
	}
	// Fixture string is 15 bytes.
	b, e := NewBinEngine(s, root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	return b, s, root
}
func TestBinMoveRestoreAndPurge(t *testing.T) {
	b, s, root := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Files) != 2 {
		t.Fatal("sidecar missing")
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "2020/day/A.jpg")); !os.IsNotExist(e) {
		t.Fatal("source still present")
	}
	if _, e = s.Decide(ctx, Decision{AssetID: 1, RequestID: "blocked-bin-edit", ExpectedRevision: 1, Status: "keep"}); e == nil {
		t.Fatal("Bin asset decision could change")
	}
	if _, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
		t.Fatal(e)
	}
	if data, e := os.ReadFile(filepath.Join(root, "2020/day/A.jpg")); e != nil || string(data) != "family original" {
		t.Fatal("restore mismatch", e)
	}
	if _, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
		t.Fatal("restore retry", e)
	}
	var rev int64
	s.read.QueryRow("SELECT revision FROM decisions WHERE asset_id=1").Scan(&rev)
	if _, e = s.Decide(ctx, Decision{AssetID: 1, RequestID: "mark-again-test", ExpectedRevision: rev, Status: "cull"}); e != nil {
		t.Fatal(e)
	}
	p, e = b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 1"); e == nil {
		t.Fatal("wrong confirmation accepted")
	}
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 2"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "2020/day/KEEP.jpg")); e != nil {
		t.Fatal("unselected file affected")
	}
}
func TestBinInterruptedMoveRecovery(t *testing.T) {
	for _, phase := range []string{"after-link", "after-unlink"} {
		t.Run(phase, func(t *testing.T) {
			b, _, root := binFixture(t)
			ctx := context.Background()
			p, e := b.Preview(ctx, []int64{1})
			if e != nil {
				t.Fatal(e)
			}
			b.checkpoint = func(s string) error {
				if s == phase {
					return errors.New("simulated interruption")
				}
				return nil
			}
			if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
				t.Fatal("injection missed")
			}
			b.checkpoint = nil
			if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
				t.Fatal("recovery failed", e)
			}
			if _, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
				t.Fatal(e)
			}
			data, _ := os.ReadFile(filepath.Join(root, "2020/day/A.jpg"))
			if string(data) != "family original" {
				t.Fatal("bytes changed")
			}
		})
	}
}
func TestBinPreflightMissingAndCollision(t *testing.T) {
	b, _, root := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "2020/day/A.jpg"), []byte("new keeper"), 0600)
	if _, e = b.Run(ctx, p.ID, "restore", ""); e == nil {
		t.Fatal("overwrote restore destination")
	}
	data, _ := os.ReadFile(filepath.Join(root, "2020/day/A.jpg"))
	if string(data) != "new keeper" {
		t.Fatal("collision overwritten")
	}
	os.Remove(filepath.Join(root, stored(p, 1)))
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 2"); e == nil {
		t.Fatal("missing member treated as deleted")
	}
	if _, e = os.Stat(filepath.Join(root, stored(p, 0))); e != nil {
		t.Fatal("media deleted before full preflight")
	}
}
func TestBinRejectsChangedDecisionAndSymlink(t *testing.T) {
	b, s, root := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	s.write.Exec("UPDATE decisions SET status='keep',revision=2 WHERE asset_id=1")
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("stale decision accepted")
	}
	if _, e = os.Stat(filepath.Join(root, "2020/day/A.jpg")); e != nil {
		t.Fatal("unselected bytes moved")
	}
	s.write.Exec("UPDATE decisions SET status='cull' WHERE asset_id=1")
	os.Remove(filepath.Join(root, "2020/day/A.jpg"))
	os.Symlink(filepath.Join(root, "2020/day/KEEP.jpg"), filepath.Join(root, "2020/day/A.jpg"))
	if _, e = b.Preview(ctx, []int64{1}); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestBinJournalFailurePreventsMove(t *testing.T) {
	b, s, root := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	s.write.Exec("CREATE TRIGGER reject_plan BEFORE UPDATE ON file_plans BEGIN SELECT RAISE(ABORT,'test storage failure'); END")
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("failed journal accepted")
	}
	if _, e = os.Stat(filepath.Join(root, "2020/day/A.jpg")); e != nil {
		t.Fatal("file moved without journal")
	}
	var n int
	s.read.QueryRow("SELECT count(*) FROM file_state").Scan(&n)
	if n != 0 {
		t.Fatal("failed reservation persisted")
	}
	s.write.Exec("DROP TRIGGER reject_plan")
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal("retry after journal recovery", e)
	}
}
func TestBinContentChangeAndExclusiveWriter(t *testing.T) {
	b, s, root := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	other, e := NewBinEngine(s, root)
	if e == nil {
		other.Close()
		t.Fatal("second writer acquired archive")
	}
	os.WriteFile(filepath.Join(root, "2020/day/A.jpg"), []byte("changed content"), 0600)
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("changed same-size content accepted")
	}
}
func TestBinSharedSidecarRetained(t *testing.T) {
	b, _, root := binFixture(t)
	os.WriteFile(filepath.Join(root, "2020/day/A.raw"), []byte("RAW original"), 0600)
	os.WriteFile(filepath.Join(root, "2020/day/A.xmp"), []byte("shared metadata"), 0600)
	p, e := b.Preview(context.Background(), []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Files) != 2 || len(p.Warnings) != 1 {
		t.Fatal("shared metadata ownership incorrect")
	}
}
func TestBinInterruptedPurgeNotReportedAsComplete(t *testing.T) {
	b, _, _ := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	b.checkpoint = func(label string) error {
		if label == "after-delete" {
			return errors.New("crash")
		}
		return nil
	}
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 2"); e == nil {
		t.Fatal("missed failure")
	}
	b.checkpoint = nil
	p, e = b.Run(ctx, p.ID, "purge", "DELETE 2")
	if e != nil || p.State != "purged_recovered" || len(p.Warnings) == 0 {
		t.Fatal("interrupted deletion did not recover with explicit uncertainty", e)
	}
}

func TestBinOwnershipChangesBeforeExecution(t *testing.T) {
	b, _, root := binFixture(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(root, "2020/day/A.xmp"), []byte("metadata"), 0600)
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "2020/day/A.raw"), []byte("RAW"), 0600)
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("stale ownership accepted")
	}
	if _, e = os.Stat(filepath.Join(root, "2020/day/A.jpg")); e != nil {
		t.Fatal("media moved before ownership preflight")
	}
}
func TestBinRollbackInterruptedMove(t *testing.T) {
	for _, phase := range []string{"after-link", "after-unlink"} {
		t.Run(phase, func(t *testing.T) {
			b, _, root := binFixture(t)
			ctx := context.Background()
			p, e := b.Preview(ctx, []int64{1})
			if e != nil {
				t.Fatal(e)
			}
			b.checkpoint = func(s string) error {
				if s == phase {
					return errors.New("interrupted")
				}
				return nil
			}
			if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
				t.Fatal("missed interruption")
			}
			b.checkpoint = nil
			if _, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
				t.Fatal(e)
			}
			for _, n := range []string{"A.jpg", "A.jpg.xmp", "KEEP.jpg"} {
				if data, e := os.ReadFile(filepath.Join(root, "2020/day", n)); e != nil || string(data) != "family original" {
					t.Fatal(n, e)
				}
			}
		})
	}
}
func TestBinOldActivePlansRemainVisible(t *testing.T) {
	b, _, _ := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 110; i++ {
		if e = b.save(&BinPlan{ID: fmt.Sprintf("%032x", i), State: "restored"}); e != nil {
			t.Fatal(e)
		}
	}
	ps, e := b.List()
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range ps {
		if v.ID == p.ID {
			return
		}
	}
	t.Fatal("active Bin lost")
}
func TestBinRejectsRootAliases(t *testing.T) {
	s := testStore(t)
	for _, root := range []string{"/", "/.", "/tmp/..", "//"} {
		if b, e := NewBinEngine(s, root); e == nil {
			b.Close()
			t.Fatal("accepted root", root)
		}
	}
	root := filepath.Join(t.TempDir(), "root")
	os.Symlink("/", root)
	if b, e := NewBinEngine(s, root); e == nil {
		b.Close()
		t.Fatal("accepted symlink root")
	}
}

func TestBinReusedMediaPathRetainsSidecar(t *testing.T) {
	b, _, root := binFixture(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(root, "2020/day/A.xmp"), []byte("metadata"), 0600)
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	b.checkpoint = func(label string) error {
		if label == "after-unlink" {
			return os.WriteFile(filepath.Join(root, "2020/day/A.jpg"), []byte("new unrelated file"), 0600)
		}
		return nil
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("reused media path accepted")
	}
	if _, e = os.Stat(filepath.Join(root, "2020/day/A.xmp")); e != nil {
		t.Fatal("shared metadata moved", e)
	}
}
func TestBinRecoveryRechecksCompletedMembers(t *testing.T) {
	for _, action := range []string{"quarantine", "restore"} {
		t.Run(action, func(t *testing.T) {
			b, _, root := binFixture(t)
			ctx := context.Background()
			p, e := b.Preview(ctx, []int64{1})
			if e != nil {
				t.Fatal(e)
			}
			if action == "restore" {
				if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
					t.Fatal(e)
				}
			}
			n := 0
			b.checkpoint = func(label string) error {
				if label == "after-link" {
					n++
					if n == 2 {
						return errors.New("second file interrupted")
					}
				}
				return nil
			}
			if _, e = b.Run(ctx, p.ID, action, ""); e == nil {
				t.Fatal("injection missed")
			}
			b.checkpoint = nil
			first := stored(p, 0)
			if action == "restore" {
				first = p.Files[0].Original
			}
			if e = os.WriteFile(filepath.Join(root, first), []byte("changed completed member"), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = b.Run(ctx, p.ID, action, ""); e == nil {
				t.Fatal("changed completed member accepted")
			}
		})
	}
}
func TestBinInterruptedSidecarRevalidatesOwnership(t *testing.T) {
	b, _, root := binFixture(t)
	ctx := context.Background()
	os.Remove(filepath.Join(root, "2020/day/A.jpg.xmp"))
	os.WriteFile(filepath.Join(root, "2020/day/A.xmp"), []byte("metadata"), 0600)
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	b.checkpoint = func(label string) error {
		if label == "after-unlink" {
			n++
			if n == 2 {
				os.WriteFile(filepath.Join(root, "2020/day/A.raw"), []byte("new RAW"), 0600)
				return errors.New("crash")
			}
		}
		return nil
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("injection missed")
	}
	b.checkpoint = nil
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e == nil {
		t.Fatal("new owner missed")
	}
	if _, e = b.Run(ctx, p.ID, "restore", ""); e != nil {
		t.Fatal(e)
	}
	if data, e := os.ReadFile(filepath.Join(root, "2020/day/A.xmp")); e != nil || string(data) != "metadata" {
		t.Fatal("sidecar restore", e)
	}
}
func TestBinInterruptedPurgeReusedOriginalRetainsMetadata(t *testing.T) {
	b, _, root := binFixture(t)
	ctx := context.Background()
	p, e := b.Preview(ctx, []int64{1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Run(ctx, p.ID, "quarantine", ""); e != nil {
		t.Fatal(e)
	}
	b.checkpoint = func(label string) error {
		if label == "after-delete" {
			return errors.New("crash")
		}
		return nil
	}
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 2"); e == nil {
		t.Fatal("injection missed")
	}
	b.checkpoint = nil
	os.WriteFile(filepath.Join(root, "2020/day/A.jpg"), []byte("new file"), 0600)
	if _, e = b.Run(ctx, p.ID, "purge", "DELETE 2"); e == nil {
		t.Fatal("reused original accepted")
	}
	if _, e = os.Stat(filepath.Join(root, stored(p, 1))); e != nil {
		t.Fatal("metadata removed", e)
	}
}
