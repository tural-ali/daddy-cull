package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root may change any folder, so the refusal cannot be observed")
	}
}

// A folder the app may not change is found before anything moves, so the
// photograph stays where it is and stays in the Bin, instead of a batch being
// left half moved, as it was on the first server Cull ran on, when 895 folders belonged to another user.
func TestTrashRefusesAFolderItMayNotChangeBeforeMovingAnything(t *testing.T) {
	skipAsRoot(t)
	f := newTrashFixture(t)
	day := filepath.Join(f.archive, "2020", "day")
	if err := os.Chmod(day, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(day, 0o700) })
	result, err := f.trash.Delete(context.Background(), []string{"marked:1"}, "DELETE 1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Done != 0 || len(result.Failures) != 1 || !strings.Contains(result.Failures[0].Error, "not allowed to change the folder 2020/day") {
		t.Fatalf("the refusal was not reported plainly: %+v", result)
	}
	if !exists(t, filepath.Join(day, "A.jpg")) || !exists(t, filepath.Join(day, "A.jpg.xmp")) {
		t.Fatal("a file left its folder although the folder could not be changed")
	}
	if _, listed := f.items(t)["marked:1"]; !listed {
		t.Fatal("the photograph dropped out of the Bin after a refused deletion")
	}
	var stuck int
	if err = f.s.read.QueryRow("SELECT count(*) FROM file_plans WHERE json_extract(body,'$.state')='quarantining'").Scan(&stuck); err != nil || stuck != 0 {
		t.Fatalf("a batch was left half moved: %d %v", stuck, err)
	}
}

// halfMoved leaves asset 1's batch the way that server's was: the photograph in the
// Bin, its sidecar linked into the Bin but not yet removed from its folder.
func halfMoved(t *testing.T, f trashFixture) string {
	t.Helper()
	ctx := context.Background()
	plan, err := f.trash.bin.Preview(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	links := 0
	f.trash.bin.checkpoint = func(label string) error {
		if label == "after-link" {
			if links++; links == 2 {
				return os.ErrPermission
			}
		}
		return nil
	}
	if _, err = f.trash.bin.Run(ctx, plan.ID, "quarantine", ""); err == nil {
		t.Fatal("the interruption did not happen")
	}
	f.trash.bin.checkpoint = nil
	return plan.ID
}

// A batch interrupted on its way into the Bin is still shown, from wherever
// each file is now, and Delete finishes moving it before deleting it.
func TestTrashShowsAndDeletesAHalfMovedBatch(t *testing.T) {
	f := newTrashFixture(t)
	id := halfMoved(t, f)
	items := f.items(t)
	item, listed := items["bin:"+id+":1"]
	if !listed {
		t.Fatalf("the half-moved batch is invisible in the Bin: %+v", items)
	}
	if _, twice := items["marked:1"]; twice {
		t.Fatal("the half-moved photograph is listed twice")
	}
	if !strings.HasPrefix(item.Preview, "/api/binned-media/bin/"+id+"/") || item.Sidecars != 1 {
		t.Fatalf("the half-moved photograph is not shown from the Bin: %+v", item)
	}
	result, err := f.trash.Delete(context.Background(), []string{item.Key}, "DELETE 1")
	if err != nil || result.Done != 1 || len(result.Failures) != 0 {
		t.Fatalf("the half-moved batch was not deleted: %+v %v", result, err)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp"} {
		if exists(t, filepath.Join(f.archive, "2020", "day", name)) {
			t.Fatalf("%s is still in its folder", name)
		}
	}
	if !exists(t, filepath.Join(f.archive, "2020", "day", "KEEP.jpg")) {
		t.Fatal("a file outside the batch was touched")
	}
}

// Restoring a half-moved batch puts every file back, whichever side it was on.
func TestTrashRestoresAHalfMovedBatch(t *testing.T) {
	f := newTrashFixture(t)
	id := halfMoved(t, f)
	result, err := f.trash.Restore(context.Background(), []string{"bin:" + id + ":1"})
	if err != nil || result.Done != 1 || len(result.Failures) != 0 {
		t.Fatalf("the half-moved batch was not restored: %+v %v", result, err)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp"} {
		if !exists(t, filepath.Join(f.archive, "2020", "day", name)) {
			t.Fatalf("%s did not come back", name)
		}
	}
	entries, err := os.ReadDir(filepath.Join(f.archive, ".culled", "next", id))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "receipt.json" {
			t.Fatalf("%s was left behind in the Bin folder", entry.Name())
		}
	}
}

// moveLegacyBatch does what Unraid's mover does: it carries the earlier tool's
// .culled folder from the disk the history recorded to another one.
func moveLegacyBatch(t *testing.T, f trashFixture, disks ...string) {
	t.Helper()
	from := filepath.Join(f.disks, "disk1", ".culled", "2020-01-02")
	for _, disk := range disks {
		to := filepath.Join(f.disks, disk, ".culled", "2020-01-02")
		if err := os.MkdirAll(to, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"L.jpg", "L.jpg.xmp"} {
			body, err := os.ReadFile(filepath.Join(from, name))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(to, name), body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.RemoveAll(from); err != nil {
		t.Fatal(err)
	}
}

// A file the mover carried to another disk is followed there and deleted.
func TestTrashFollowsALegacyFileTheMoverRelocated(t *testing.T) {
	f := newTrashFixture(t)
	moveLegacyBatch(t, f, "disk2")
	result, err := f.trash.Delete(context.Background(), []string{"legacy:1"}, "DELETE 1")
	if err != nil || result.Done != 1 || len(result.Failures) != 0 {
		t.Fatalf("the relocated batch was not deleted: %+v %v", result, err)
	}
	for _, name := range []string{"L.jpg", "L.jpg.xmp"} {
		if exists(t, filepath.Join(f.disks, "disk2", ".culled", "2020-01-02", name)) {
			t.Fatalf("%s is still on the disk it moved to", name)
		}
	}
	var held int
	if err = f.s.read.QueryRow("SELECT count(*) FROM legacy_culled WHERE purged_at IS NULL").Scan(&held); err != nil || held != 0 {
		t.Fatalf("the history still holds %d rows: %v", held, err)
	}
}

// When two disks hold a copy, neither is guessed at.
func TestTrashRefusesALegacyFileFoundOnTwoDisks(t *testing.T) {
	f := newTrashFixture(t)
	moveLegacyBatch(t, f, "disk2", "disk3")
	result, err := f.trash.Delete(context.Background(), []string{"legacy:1"}, "DELETE 1")
	if err != nil || result.Done != 0 || len(result.Failures) != 1 || !strings.Contains(result.Failures[0].Error, "more than one disk") {
		t.Fatalf("an ambiguous copy was not refused: %+v %v", result, err)
	}
	for _, disk := range []string{"disk2", "disk3"} {
		if !exists(t, filepath.Join(f.disks, disk, ".culled", "2020-01-02", "L.jpg")) {
			t.Fatalf("the copy on %s was touched", disk)
		}
	}
}
