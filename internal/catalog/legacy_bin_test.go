package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func legacyBinFixture(t *testing.T) (*LegacyBinEngine, *Store, string) {
	t.Helper()
	store := testStore(t)
	root := t.TempDir()
	binDir := filepath.Join(root, "disk1", ".culled", "2020-01-02")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"A.jpg": "photo bytes", "A.jpg.xmp": "sidecar bytes"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := store.write.Exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,day,size_bytes,reason,culled_at) VALUES
		(1,'batch-a','media','/disks/disk1/2020/2020-01/2020-01-02/A.jpg','/disks/disk1/.culled/2020-01-02/A.jpg','2020-01-02',11,'review','2026-01-01T00:00:00Z'),
		(2,'batch-a','sidecar','/disks/disk1/2020/2020-01/2020-01-02/A.jpg.xmp','/disks/disk1/.culled/2020-01-02/A.jpg.xmp','2020-01-02',13,'review','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewLegacyBinEngine(store, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	return engine, store, root
}

func TestLegacyBinRestoreIncludesBatchAndNeverOverwrites(t *testing.T) {
	engine, store, root := legacyBinFixture(t)
	ctx := context.Background()
	plan, err := engine.Preview(ctx, []int64{1})
	if err != nil || len(plan.Files) != 2 {
		t.Fatalf("batch was not expanded: %+v %v", plan, err)
	}
	destination := filepath.Join(root, "disk1", "2020", "2020-01", "2020-01-02", "A.jpg")
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(destination, []byte("new keeper"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Run(ctx, plan.ID, "restore", ""); err == nil {
		t.Fatal("restore overwrote an occupied destination")
	}
	if data, _ := os.ReadFile(destination); string(data) != "new keeper" {
		t.Fatal("occupied destination changed")
	}
	if err = os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(ctx, plan.ID, "restore", "")
	if err != nil || result.State != "restored" {
		t.Fatalf("restore failed: %+v %v", result, err)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp"} {
		if _, err = os.Stat(filepath.Join(filepath.Dir(destination), name)); err != nil {
			t.Fatalf("%s missing after restore: %v", name, err)
		}
	}
	var active int
	if err = store.read.QueryRow("SELECT count(*) FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL").Scan(&active); err != nil || active != 0 {
		t.Fatalf("ledger not completed: %d %v", active, err)
	}
}

func TestLegacyBinPurgeRequiresExactConfirmationAndRecoversIntent(t *testing.T) {
	engine, store, _ := legacyBinFixture(t)
	ctx := context.Background()
	plan, err := engine.Preview(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Run(ctx, plan.ID, "purge", "DELETE 1"); err == nil {
		t.Fatal("incorrect deletion confirmation accepted")
	}
	if _, err = engine.Run(ctx, plan.ID, "purge", "DELETE 2"); err != nil {
		t.Fatal(err)
	}
	var active int
	if err = store.read.QueryRow("SELECT count(*) FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL").Scan(&active); err != nil || active != 0 {
		t.Fatalf("purge ledger not completed: %d %v", active, err)
	}
}

func TestLegacyBinInterruptedRestoreResumes(t *testing.T) {
	engine, _, root := legacyBinFixture(t)
	ctx := context.Background()
	plan, err := engine.Preview(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	engine.checkpoint = func(label string) error {
		if label == "after-link" {
			return errors.New("simulated interruption")
		}
		return nil
	}
	if _, err = engine.Run(ctx, plan.ID, "restore", ""); err == nil {
		t.Fatal("interruption not injected")
	}
	engine.checkpoint = nil
	result, err := engine.Run(ctx, plan.ID, "restore", "")
	if err != nil || result.State != "restored" {
		t.Fatalf("restore did not resume: %+v %v", result, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "disk1", "2020", "2020-01", "2020-01-02", "A.jpg")); err != nil || string(data) != "photo bytes" {
		t.Fatalf("restored content differs: %q %v", data, err)
	}
}
