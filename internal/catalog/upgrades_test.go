package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportUpgradeReportMapsAndGroupsConfirmedRows(t *testing.T) {
	store := testStore(t)
	_, err := store.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2020/2020-01/2020-01-02/A.JPG',1,'image',4,'archive')")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	file := filepath.Join(root, "Takeout", "Album", "A.JPG")
	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("larger"), 0660); err != nil {
		t.Fatal(err)
	}
	report := "verdict\tgoogle_date\tarchive_day\tdays_apart\tratio\tgoogle_px\tarchive_px\tgoogle_file\tarchive_file\n" +
		"CONFIRMED\t2020:01:02 03:04:05\t2020-01-02\t0\t2.0\t200x200\t100x200\t/host/upgrades/Takeout/Album/A.JPG\t/host/archive/2020/2020-01/2020-01-02/A.JPG\n"
	count, err := store.ImportUpgradeReport(context.Background(), strings.NewReader(report), root, "/host/upgrades", "/host/archive")
	if err != nil || count != 1 {
		t.Fatalf("import failed: %d %v", count, err)
	}
	page, err := store.Upgrades(context.Background())
	if err != nil || page.Total != 1 || page.Pending != 1 || len(page.Groups[0].Copies) != 1 || !page.Groups[0].Copies[0].Available {
		t.Fatalf("unexpected page: %+v %v", page, err)
	}
}

func upgradeWriterFixture(t *testing.T) (*UpgradeWriter, *Store, string, string) {
	t.Helper()
	store := testStore(t)
	upgrades := t.TempDir()
	archive := t.TempDir()
	if err := os.MkdirAll(filepath.Join(upgrades, "Takeout", "Album"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(archive, "2020", "2020-01", "2020-01-02"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upgrades, "Takeout", "Album", "A.JPG"), []byte("higher resolution"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "2020", "2020-01", "2020-01-02", "A.JPG"), []byte("archive"), 0660); err != nil {
		t.Fatal(err)
	}
	_, err := store.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2020/2020-01/2020-01-02/A.JPG',1,'image',7,'archive'),
		(2,'/upgrades/Takeout/Album/A.JPG',1,'image',17,'takeout');
		UPDATE assets SET anchor_id=1 WHERE id=2;
		INSERT INTO upgrade_candidates VALUES(1,2,'2020:01:02 00:00:00','2020-01-02',2.0,'200x200','100x200','Album',1)`)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewUpgradeWriter(store, upgrades, archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	return writer, store, upgrades, archive
}

func TestUpgradeWriterCopiesVerifiesAndRetainsSource(t *testing.T) {
	writer, store, upgrades, archive := upgradeWriterFixture(t)
	plan, err := writer.Preview(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := writer.Run(context.Background(), plan.ID)
	if err != nil || result.State != "accepted" {
		t.Fatalf("accept failed: %+v %v", result, err)
	}
	if data, readErr := os.ReadFile(filepath.Join(archive, result.Destination)); readErr != nil || string(data) != "higher resolution" {
		t.Fatalf("destination differs: %q %v", data, readErr)
	}
	if data, readErr := os.ReadFile(filepath.Join(upgrades, result.Source)); readErr != nil || string(data) != "higher resolution" {
		t.Fatalf("source changed: %q %v", data, readErr)
	}
	var accepted string
	if err = store.read.QueryRow("SELECT accepted_as FROM upgrade_history WHERE archive_file='/archive/2020/2020-01/2020-01-02/A.JPG'").Scan(&accepted); err != nil || !strings.Contains(accepted, "A (hi-res).JPG") {
		t.Fatalf("receipt missing: %q %v", accepted, err)
	}
}

func TestUpgradeWriterCollisionAndInterruptedCopyFailSafe(t *testing.T) {
	writer, _, upgrades, archive := upgradeWriterFixture(t)
	dir := filepath.Join(archive, "2020", "2020-01", "2020-01-02")
	if err := os.WriteFile(filepath.Join(dir, "A (hi-res).JPG"), []byte("occupant"), 0660); err != nil {
		t.Fatal(err)
	}
	plan, err := writer.Preview(context.Background(), 1, 2)
	if err != nil || pathBase(plan.Destination) != "A (hi-res) (2).JPG" {
		t.Fatalf("free name missing: %+v %v", plan, err)
	}
	writer.checkpoint = func(label string) error {
		if label == "after-copy" {
			return os.ErrDeadlineExceeded
		}
		return nil
	}
	if _, err = writer.Run(context.Background(), plan.ID); err == nil {
		t.Fatal("interruption not injected")
	}
	writer.checkpoint = nil
	result, err := writer.Run(context.Background(), plan.ID)
	if err != nil || result.State != "accepted" {
		t.Fatalf("resume failed: %+v %v", result, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "A (hi-res).JPG")); string(data) != "occupant" {
		t.Fatal("existing archive file changed")
	}
	if _, err = os.Stat(filepath.Join(upgrades, "Takeout", "Album", "A.JPG")); err != nil {
		t.Fatal("Google source was removed")
	}
}

func pathBase(value string) string { return filepath.Base(filepath.FromSlash(value)) }
