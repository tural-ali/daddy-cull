package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func screenshotWriterFixture(t *testing.T) (*ScreenshotWriter, *Store, string, string) {
	t.Helper()
	store := testStore(t)
	shots := t.TempDir()
	archive := t.TempDir()
	for name, content := range map[string]string{"2020-01-02_A.png": "image bytes", "2020-01-02_A.png.xmp": "sidecar bytes"} {
		if err := os.WriteFile(filepath.Join(shots, name), []byte(content), 0660); err != nil {
			t.Fatal(err)
		}
	}
	_, err := store.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/screenshots/2020-01-02_A.png',1,'image',11,'screenshots');
		INSERT INTO screenshot_items(asset_id,path,day,name,size_bytes,mtime,state) VALUES(1,'/screenshots/2020-01-02_A.png','2020-01-02','2020-01-02_A.png',11,1,'waiting')`)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewScreenshotWriter(store, shots, archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	return writer, store, shots, archive
}

func TestScreenshotKeepCopiesVerifiedFileAndSidecar(t *testing.T) {
	writer, store, shots, archive := screenshotWriterFixture(t)
	plan, err := writer.Preview(context.Background(), 1, "keep")
	if err != nil || len(plan.Files) != 2 {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
	result, err := writer.Run(context.Background(), plan.ID)
	if err != nil || result.State != "kept" {
		t.Fatalf("keep failed: %+v %v", result, err)
	}
	for name, expected := range map[string]string{"A.png": "image bytes", "A.png.xmp": "sidecar bytes"} {
		data, readErr := os.ReadFile(filepath.Join(archive, "2020", "2020-01", "2020-01-02", name))
		if readErr != nil || string(data) != expected {
			t.Fatalf("%s differs: %q %v", name, data, readErr)
		}
	}
	if _, err = os.Stat(filepath.Join(shots, "2020-01-02_A.png")); !os.IsNotExist(err) {
		t.Fatal("holding-area source remained")
	}
	var state string
	if err = store.read.QueryRow("SELECT state FROM screenshot_items WHERE asset_id=1").Scan(&state); err != nil || state != "kept" {
		t.Fatalf("state not recorded: %s %v", state, err)
	}
}

func TestScreenshotKeepNeverOverwritesAndUsesFreeName(t *testing.T) {
	writer, _, shots, archive := screenshotWriterFixture(t)
	dir := filepath.Join(archive, "2020", "2020-01", "2020-01-02")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A.png"), []byte("keeper"), 0660); err != nil {
		t.Fatal(err)
	}
	plan, err := writer.Preview(context.Background(), 1, "keep")
	if err != nil || filepath.Base(plan.Files[0].Destination) != "A (2).png" {
		t.Fatalf("free name missing: %+v %v", plan, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "A (2).png"), []byte("new occupant"), 0660); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Run(context.Background(), plan.ID); err == nil {
		t.Fatal("occupied destination was overwritten")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "A (2).png")); string(data) != "new occupant" {
		t.Fatal("occupied destination changed")
	}
	if _, err = os.Stat(filepath.Join(shots, "2020-01-02_A.png")); err != nil {
		t.Fatal("source was not retained")
	}
}

func TestScreenshotKeepResumesAfterVerifiedCopy(t *testing.T) {
	writer, _, _, archive := screenshotWriterFixture(t)
	plan, err := writer.Preview(context.Background(), 1, "keep")
	if err != nil {
		t.Fatal(err)
	}
	writer.checkpoint = func(label string) error {
		if label == "after-copy" {
			return errors.New("simulated interruption")
		}
		return nil
	}
	if _, err = writer.Run(context.Background(), plan.ID); err == nil {
		t.Fatal("interruption not injected")
	}
	writer.checkpoint = nil
	result, err := writer.Run(context.Background(), plan.ID)
	if err != nil || result.State != "kept" {
		t.Fatalf("resume failed: %+v %v", result, err)
	}
	if data, err := os.ReadFile(filepath.Join(archive, "2020", "2020-01", "2020-01-02", "A.png")); err != nil || string(data) != "image bytes" {
		t.Fatalf("destination differs: %q %v", data, err)
	}
}

func TestScreenshotRemoveMovesToRecoverableBin(t *testing.T) {
	writer, store, shots, _ := screenshotWriterFixture(t)
	plan, err := writer.Preview(context.Background(), 1, "remove")
	if err != nil {
		t.Fatal(err)
	}
	result, err := writer.Run(context.Background(), plan.ID)
	if err != nil || result.State != "bin" {
		t.Fatalf("remove failed: %+v %v", result, err)
	}
	for _, file := range result.Files {
		if _, err = os.Stat(filepath.Join(shots, file.Destination)); err != nil {
			t.Fatalf("Bin member missing: %v", err)
		}
	}
	var state string
	if err = store.read.QueryRow("SELECT state FROM screenshot_items WHERE asset_id=1").Scan(&state); err != nil || state != "bin" {
		t.Fatalf("state not recorded: %s %v", state, err)
	}
	result, err = writer.UndoRemove(context.Background(), plan.ID)
	if err != nil || result.State != "restored" {
		t.Fatalf("undo failed: %+v %v", result, err)
	}
	if data, err := os.ReadFile(filepath.Join(shots, "2020-01-02_A.png")); err != nil || string(data) != "image bytes" {
		t.Fatalf("undo content differs: %q %v", data, err)
	}
}

func TestScreenshotRemoveResumesAfterLink(t *testing.T) {
	writer, _, shots, _ := screenshotWriterFixture(t)
	plan, err := writer.Preview(context.Background(), 1, "remove")
	if err != nil {
		t.Fatal(err)
	}
	writer.checkpoint = func(label string) error {
		if label == "after-link" {
			return errors.New("simulated interruption")
		}
		return nil
	}
	if _, err = writer.Run(context.Background(), plan.ID); err == nil {
		t.Fatal("interruption not injected")
	}
	writer.checkpoint = nil
	result, err := writer.Run(context.Background(), plan.ID)
	if err != nil || result.State != "bin" {
		t.Fatalf("remove did not resume: %+v %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(shots, "2020-01-02_A.png")); !os.IsNotExist(err) {
		t.Fatal("source remained after resumed remove")
	}
}

func TestScreenshotPurgeRequiresConfirmationAndRecoversIntent(t *testing.T) {
	writer, store, _, _ := screenshotWriterFixture(t)
	plan, err := writer.Preview(context.Background(), 1, "remove")
	if err != nil {
		t.Fatal(err)
	}
	plan, err = writer.Run(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.PurgeRemove(context.Background(), plan.ID, "DELETE 1"); err == nil {
		t.Fatal("incorrect confirmation accepted")
	}
	writer.checkpoint = func(label string) error {
		if label == "after-delete" {
			return errors.New("simulated interruption")
		}
		return nil
	}
	if _, err = writer.PurgeRemove(context.Background(), plan.ID, "DELETE 2"); err == nil {
		t.Fatal("interruption not injected")
	}
	writer.checkpoint = nil
	result, err := writer.PurgeRemove(context.Background(), plan.ID, "DELETE 2")
	if err != nil || result.State != "purged_recovered" {
		t.Fatalf("purge recovery failed: %+v %v", result, err)
	}
	var state string
	if err = store.read.QueryRow("SELECT state FROM screenshot_items WHERE asset_id=1").Scan(&state); err != nil || state != "missing" {
		t.Fatalf("purge state not recorded: %s %v", state, err)
	}
}
