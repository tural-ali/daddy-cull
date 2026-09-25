package catalog

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const day = 24 * time.Hour

// graceFixture is the four-source Bin with the production default: deleting
// from the Bin only schedules the files.
func graceFixture(t *testing.T, days int) trashFixture {
	t.Helper()
	f := newTrashFixture(t)
	if err := f.s.SetGraceDays(context.Background(), days); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f trashFixture) deleting(t *testing.T) DeletingReport {
	t.Helper()
	report, err := f.s.Deleting(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func (f trashFixture) binFiles(t *testing.T, plan string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.archive, ".culled/next", plan))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if entry.Name() != "receipt.json" {
			names = append(names, entry.Name())
		}
	}
	return names
}

func (f trashFixture) scheduled(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.s.read.QueryRow("SELECT count(*) FROM trash_deletions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The whole promise: a deleted file stays on disk for the grace period, and
// once it has passed the reaper deletes it without anyone asking.
func TestDeletedFilesAreKeptForTheGracePeriodThenDeletedAutomatically(t *testing.T) {
	f := graceFixture(t, 30)
	ctx := context.Background()
	before := time.Now()
	result, err := f.trash.Delete(ctx, []string{"bin:" + f.binPlan + ":2"}, "DELETE 2")
	if err != nil || result.Done != 2 || result.KeptDays != 30 || len(result.Failures) != 0 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	if got := f.binFiles(t, f.binPlan); len(got) != 2 {
		t.Fatalf("a scheduled deletion removed files at once: %v", got)
	}
	if left := f.items(t); len(left) != 3 {
		t.Fatalf("the Bin still lists what was deleted from it: %d items", len(left))
	}
	report := f.deleting(t)
	if len(report.Items) != 2 || report.GraceDays != 30 {
		t.Fatalf("the Log does not list the waiting files: %+v", report)
	}
	due, err := time.Parse(time.RFC3339, report.Items[0].DueAt)
	if err != nil || due.Before(before.Add(30*day).Add(-time.Second)) || due.After(time.Now().Add(30*day)) {
		t.Fatalf("due %v, want 30 days after the deletion: %v", due, err)
	}

	deleted, err := f.trash.Reap(ctx, time.Now().Add(29*day))
	if err != nil || deleted != 0 || len(f.binFiles(t, f.binPlan)) != 2 {
		t.Fatalf("the reaper deleted %d files a day early: %v", deleted, err)
	}
	deleted, err = f.trash.Reap(ctx, time.Now().Add(30*day+time.Minute))
	if err != nil || deleted != 2 {
		t.Fatalf("the reaper deleted %d files once due: %v", deleted, err)
	}
	if got := f.binFiles(t, f.binPlan); len(got) != 0 {
		t.Fatalf("files outlived their grace period: %v", got)
	}
	report = f.deleting(t)
	if len(report.Items) != 0 || f.scheduled(t) != 0 {
		t.Fatalf("a deleted file is still listed as waiting: %+v", report)
	}
	if report.LastDeleted != 2 || report.LastRun == "" || report.LastError != "" {
		t.Fatalf("the last automatic run was not recorded: %+v", report)
	}
	if !exists(t, filepath.Join(f.archive, "2020/day/KEEP.jpg")) || !exists(t, filepath.Join(f.archive, "2020/day/A.jpg")) {
		t.Fatal("a file outside the deletion was touched")
	}
}

// Restoring from the Log before the deadline puts the files back where they
// came from and cancels the deletion.
func TestADeletedFileCanBeRestoredUntilItsDeadline(t *testing.T) {
	f := graceFixture(t, 30)
	ctx := context.Background()
	if _, err := f.trash.Delete(ctx, []string{"shot:" + f.shot}, "DELETE 1"); err != nil {
		t.Fatal(err)
	}
	report := f.deleting(t)
	if len(report.Items) != 1 {
		t.Fatalf("waiting: %+v", report.Items)
	}
	result, err := f.trash.Restore(ctx, []string{report.Items[0].Key})
	if err != nil || result.Done != 1 || len(result.Failures) != 0 {
		t.Fatalf("restore: %+v %v", result, err)
	}
	if !exists(t, filepath.Join(f.shots, "2020-01-02_S.png")) {
		t.Fatal("the restored screenshot is not back in its folder")
	}
	if f.scheduled(t) != 0 || len(f.deleting(t).Items) != 0 {
		t.Fatal("a restored file is still scheduled for deletion")
	}
	if deleted, err := f.trash.Reap(ctx, time.Now().Add(365*day)); err != nil || deleted != 0 {
		t.Fatalf("the reaper deleted %d restored files: %v", deleted, err)
	}
	if !exists(t, filepath.Join(f.shots, "2020-01-02_S.png")) {
		t.Fatal("the reaper deleted a restored file")
	}
}

// A photograph marked but never moved is moved into the Bin first, so it waits
// somewhere the writer owns, and is then deleted like any other.
func TestDeletingAMarkedPhotographMovesItOutOfTheArchiveFirst(t *testing.T) {
	f := graceFixture(t, 7)
	ctx := context.Background()
	result, err := f.trash.Delete(ctx, []string{"marked:1"}, "DELETE 1")
	if err != nil || result.Done != 1 || result.KeptDays != 7 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp"} {
		if exists(t, filepath.Join(f.archive, "2020/day", name)) {
			t.Fatalf("%s is still in the archive after being deleted", name)
		}
	}
	report := f.deleting(t)
	if len(report.Items) != 1 || report.Items[0].Source != "bin" || report.Items[0].Preview == "" {
		t.Fatalf("the moved photograph is not listed with a preview: %+v", report.Items)
	}
	plan := strings.TrimPrefix(report.Items[0].Group, "bin:")
	if got := f.binFiles(t, plan); len(got) != 2 {
		t.Fatalf("photograph and sidecar should wait in the Bin: %v", got)
	}
	if deleted, err := f.trash.Reap(ctx, time.Now().Add(7*day+time.Minute)); err != nil || deleted != 1 {
		t.Fatalf("reaped %d: %v", deleted, err)
	}
	if got := f.binFiles(t, plan); len(got) != 0 {
		t.Fatalf("left on disk: %v", got)
	}
}

// Shortening the grace period applies to what is already waiting, which is
// what a setting called "keep deleted files for N days" says.
func TestShorteningTheGracePeriodAppliesToFilesAlreadyWaiting(t *testing.T) {
	f := graceFixture(t, 30)
	ctx := context.Background()
	if _, err := f.trash.Delete(ctx, []string{"bin:" + f.binPlan + ":2"}, "DELETE 2"); err != nil {
		t.Fatal(err)
	}
	if deleted, _ := f.trash.Reap(ctx, time.Now().Add(8*day)); deleted != 0 {
		t.Fatal("deleted before the setting changed")
	}
	if err := f.s.SetGraceDays(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if deleted, err := f.trash.Reap(ctx, time.Now().Add(8*day)); err != nil || deleted != 2 {
		t.Fatalf("reaped %d after shortening to 7 days: %v", deleted, err)
	}
}

// Empty the Bin schedules everything in it, from every source.
func TestEmptyingTheBinSchedulesEverySource(t *testing.T) {
	f := graceFixture(t, 30)
	ctx := context.Background()
	result, err := f.trash.Empty(ctx, "DELETE 5")
	if err != nil || result.Done != 5 || result.KeptDays != 30 {
		t.Fatalf("empty: %+v %v", result, err)
	}
	if left := f.items(t); len(left) != 0 {
		t.Fatalf("the Bin is not empty: %d", len(left))
	}
	sources := map[string]int{}
	for _, item := range f.deleting(t).Items {
		sources[item.Source]++
	}
	if sources["bin"] != 3 || sources["legacy"] != 1 || sources["screenshot"] != 1 {
		t.Fatalf("waiting by source: %v", sources)
	}
	if !exists(t, filepath.Join(f.disks, "disk1/.culled/2020-01-02/L.jpg")) {
		t.Fatal("the earlier tool's file was deleted at once")
	}
	if deleted, err := f.trash.Reap(ctx, time.Now().Add(31*day)); err != nil || deleted != 5 {
		t.Fatalf("reaped %d: %v", deleted, err)
	}
	if exists(t, filepath.Join(f.disks, "disk1/.culled/2020-01-02/L.jpg")) || exists(t, filepath.Join(f.shots, "2020-01-02_S.png")) {
		t.Fatal("a file survived the reaper")
	}
}

// Delete now skips the wait, but only for files already deleted from the Bin,
// and only with the exact count confirmed.
func TestDeleteNowSkipsTheWaitForConfirmedFiles(t *testing.T) {
	f := graceFixture(t, 30)
	ctx := context.Background()
	if _, err := f.trash.PurgeNow(ctx, []string{"shot:" + f.shot}, "DELETE 1"); err == nil {
		t.Fatal("a file still in the Bin was deleted without going through the Bin's own deletion")
	}
	if _, err := f.trash.Delete(ctx, []string{"shot:" + f.shot}, "DELETE 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.trash.PurgeNow(ctx, []string{"shot:" + f.shot}, "DELETE 2"); err == nil {
		t.Fatal("a wrong confirmation was accepted")
	}
	result, err := f.trash.PurgeNow(ctx, []string{"shot:" + f.shot}, "DELETE 1")
	if err != nil || result.Done != 1 || result.KeptDays != 0 {
		t.Fatalf("delete now: %+v %v", result, err)
	}
	if f.scheduled(t) != 0 || len(f.deleting(t).Items) != 0 {
		t.Fatal("a file deleted now is still listed as waiting")
	}
}

// A group the engine refuses is kept, retried next time, and the reason is shown
// rather than the file silently staying or silently counting as gone.
func TestTheReaperKeepsARefusedGroupAndSaysWhy(t *testing.T) {
	f := graceFixture(t, 1)
	ctx := context.Background()
	if _, err := f.trash.Delete(ctx, []string{"legacy:1"}, "DELETE 1"); err != nil {
		t.Fatal(err)
	}
	moveLegacyBatch(t, f, "disk2", "disk3")
	deleted, err := f.trash.Reap(ctx, time.Now().Add(2*day))
	if err != nil || deleted != 0 {
		t.Fatalf("reaped %d of an ambiguous batch: %v", deleted, err)
	}
	report := f.deleting(t)
	if len(report.Items) != 1 || report.Items[0].Attempts != 1 || !strings.Contains(report.Items[0].LastError, "more than one disk") {
		t.Fatalf("the refusal is not shown on the waiting file: %+v", report.Items)
	}
	if !strings.Contains(report.LastError, "tried again") {
		t.Fatalf("the run's summary does not say it will retry: %q", report.LastError)
	}
	for _, disk := range []string{"disk2", "disk3"} {
		if !exists(t, filepath.Join(f.disks, disk, ".culled", "2020-01-02", "L.jpg")) {
			t.Fatalf("the copy on %s was touched", disk)
		}
	}
}

// A grace period of zero keeps the old behaviour, and a stored value nobody
// could have saved stops the reaper instead of being guessed at.
func TestGracePeriodIsValidated(t *testing.T) {
	f := graceFixture(t, 30)
	ctx := context.Background()
	for _, bad := range []int{-1, MaxGraceDays + 1} {
		if err := f.s.SetGraceDays(ctx, bad); err != ErrInvalid {
			t.Fatalf("%d days accepted: %v", bad, err)
		}
	}
	if _, err := f.trash.Delete(ctx, []string{"bin:" + f.binPlan + ":2"}, "DELETE 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.write.Exec("UPDATE settings SET value='soon' WHERE key=?", graceSetting); err != nil {
		t.Fatal(err)
	}
	if deleted, err := f.trash.Reap(ctx, time.Now().Add(365*day)); err == nil || deleted != 0 || len(f.binFiles(t, f.binPlan)) != 2 {
		t.Fatalf("the reaper acted on an unreadable setting: %d %v", deleted, err)
	}
	if report := f.deleting(t); report.GraceError == "" || !strings.Contains(report.LastError, "soon") {
		t.Fatalf("the broken setting is not reported: %+v", report)
	}
	if err := f.s.SetGraceDays(ctx, 0); err != nil {
		t.Fatal(err)
	}
	result, err := f.trash.Delete(ctx, []string{"shot:" + f.shot}, "DELETE 1")
	if err != nil || result.KeptDays != 0 || exists(t, filepath.Join(f.shots, "2020-01-02_S.png")) || len(f.deleting(t).Items) != 2 {
		t.Fatalf("zero days did not delete at once: %+v %v", result, err)
	}
}

// The setting is saved by the preview process, same-origin JSON only.
func TestGracePeriodSettingRoute(t *testing.T) {
	f := graceFixture(t, 30)
	handler := f.s.Handler()
	post := func(body string, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/settings/bin", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, request)
		return got
	}
	got := post(`{"graceDays":14}`, "")
	var report DeletingReport
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &report) != nil || report.GraceDays != 14 || report.CheckInterval != 15 {
		t.Fatalf("save: %d %s", got.Code, got.Body)
	}
	for _, body := range []string{`{"graceDays":400}`, `{"graceDays":-2}`, `{}`, `{"graceDays":"7"}`, `{"graceDays":7,"other":1}`} {
		if got := post(body, ""); got.Code != 400 {
			t.Fatalf("%s accepted: %d", body, got.Code)
		}
	}
	if got := post(`{"graceDays":1}`, "https://elsewhere.example"); got.Code != 403 {
		t.Fatalf("a cross-site request changed the setting: %d", got.Code)
	}
	if days, _ := f.s.GraceDays(context.Background()); days != 14 {
		t.Fatalf("grace is %d days, want 14", days)
	}
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest("GET", "/api/trash/deleting", nil))
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), `"graceDays":14`) {
		t.Fatalf("deleting: %d %s", listed.Code, listed.Body)
	}
}
