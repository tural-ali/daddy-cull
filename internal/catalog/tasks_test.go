package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const taskSecret = "0123456789abcdef0123456789abcdef"

// taskFixture is the four-source Bin with three more screenshots waiting,
// and a runner that reaches the real writer handlers over HTTP.
type taskFixture struct {
	trashFixture
	runner *TaskRunner
	writer *httptest.Server
}

func newTaskFixture(t *testing.T) taskFixture {
	t.Helper()
	f := taskFixture{trashFixture: newTrashFixture(t)}
	for i, name := range []string{"2021-03-04_X.png", "2021-03-04_Y.png", "2021-03-05_Z.png"} {
		if err := os.WriteFile(filepath.Join(f.shots, name), []byte("shot bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		id := int64(20 + i)
		if _, err := f.s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',10,'screenshots');
			INSERT INTO screenshot_items(asset_id,path,day,name,size_bytes,mtime,state) VALUES(?,?,?,?,10,1,'waiting')`,
			id, "/screenshots/"+name, id, "/screenshots/"+name, name[:10], name); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/screenshot/", f.trash.shots.Handler(taskSecret))
	mux.Handle("/trash/", f.trash.Handler(taskSecret))
	f.writer = httptest.NewServer(mux)
	t.Cleanup(f.writer.Close)
	f.runner = f.s.NewTaskRunner(f.writer.URL, taskSecret)
	f.runner.pause = 10 * time.Millisecond
	return f
}

// drain runs every queued task to the end.
func (f taskFixture) drain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for {
		var id string
		if err := f.s.read.QueryRow("SELECT id FROM tasks WHERE state='queued' ORDER BY created_at,rowid LIMIT 1").Scan(&id); err != nil {
			return
		}
		f.runner.run(ctx, id)
	}
}

func (f taskFixture) task(t *testing.T, id string) Task {
	t.Helper()
	task, err := f.s.Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func waitingShots(t *testing.T, s *Store) map[int64]bool {
	t.Helper()
	page, err := s.ScreenshotPage(context.Background(), "", "all", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, item := range page.Items {
		ids[item.ID] = true
	}
	return ids
}

// Pressing X on many screenshots answers at once: they leave the page while
// the queue moves them, each into the Bin, and undo brings every one back.
func TestTaskMovesScreenshotsInTheBackgroundAndUndoes(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	task, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{20, 21, 22}, Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	if task.State != TaskQueued || task.Total != 3 || task.Label != "Move 3 screenshots to the Bin" || !task.Undoable {
		t.Fatalf("queued task: %+v", task)
	}
	if shots := waitingShots(t, f.s); len(shots) != 0 {
		t.Fatalf("queued screenshots still listed: %v", shots)
	}
	if _, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{21}, Action: "remove"}); !errors.Is(err, ErrQueued) {
		t.Fatalf("queued twice: %v", err)
	}
	f.drain(t)
	done := f.task(t, task.ID)
	if done.State != TaskDone || done.Done != 3 || done.Bytes != 30 || done.FinishedAt == "" {
		t.Fatalf("finished task: %+v", done)
	}
	for _, name := range []string{"2021-03-04_X.png", "2021-03-04_Y.png", "2021-03-05_Z.png"} {
		if exists(t, filepath.Join(f.shots, name)) {
			t.Fatalf("%s still in the holding area", name)
		}
	}
	if len(waitingShots(t, f.s)) != 0 {
		t.Fatal("moved screenshots listed")
	}

	undo, err := f.s.UndoTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Kind != TaskScreenshotsRestore || undo.Total != 3 || undo.UndoOf != task.ID {
		t.Fatalf("undo task: %+v", undo)
	}
	if f.task(t, task.ID).Undoable {
		t.Fatal("a task taken back can be taken back again")
	}
	f.drain(t)
	if restored := f.task(t, undo.ID); restored.State != TaskDone || restored.Done != 3 {
		t.Fatalf("undo finished: %+v", restored)
	}
	if shots := waitingShots(t, f.s); len(shots) != 3 {
		t.Fatalf("screenshots back: %v", shots)
	}
	for _, name := range []string{"2021-03-04_X.png", "2021-03-04_Y.png", "2021-03-05_Z.png"} {
		if !exists(t, filepath.Join(f.shots, name)) {
			t.Fatalf("%s not back in the holding area", name)
		}
	}
}

// Undo brings back only what is still in the Bin from that move: a
// screenshot deleted from the Bin since stays deleted, and once none is left
// the move can no longer be taken back.
func TestTaskUndoLeavesWhatLeftTheBin(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	task, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{20, 21, 22}, Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	var plans []string
	rows, err := f.s.read.Query("SELECT plan_id FROM task_items WHERE task_id=? ORDER BY seq", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var plan string
		if err := rows.Scan(&plan); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	rows.Close()
	if len(plans) != 3 {
		t.Fatalf("plans: %v", plans)
	}
	deletion, err := f.s.QueueBin(ctx, BinTask{Action: "delete", Keys: []string{"shot:" + plans[0]}, Confirmation: "DELETE 1"})
	if err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if got := f.task(t, deletion.ID); got.State != TaskDone || got.Done != 1 {
		t.Fatalf("deletion: %+v", got)
	}
	if !f.task(t, task.ID).Undoable {
		t.Fatal("two of its screenshots are still in the Bin")
	}
	undo, err := f.s.UndoTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.Total != 2 {
		t.Fatalf("undo brings back %d, want the 2 still in the Bin", undo.Total)
	}
	f.drain(t)
	if shots := waitingShots(t, f.s); len(shots) != 2 || shots[20] {
		t.Fatalf("back on the page: %v", shots)
	}

	// A move whose screenshots have all left the Bin offers no undo.
	again, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{21}, Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if _, err := f.s.QueueBin(ctx, BinTask{Action: "empty", Confirmation: DeleteConfirmation(len(mustTrash(t, f.s)))}); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	if f.task(t, again.ID).Undoable {
		t.Fatal("a move whose screenshots were all deleted can be taken back")
	}
	if _, err := f.s.UndoTask(ctx, again.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("undo of a deleted move: %v", err)
	}
}

func mustTrash(t *testing.T, s *Store) []TrashItem {
	t.Helper()
	items, err := s.Trash(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// A screenshot the writer refuses fails on its own; the rest carry on, and a
// retry tries only what failed.
func TestTaskFailsFileByFileAndRetries(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	task, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{20, 21, 22}, Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.shots, "2021-03-04_Y.png")); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	got := f.task(t, task.ID)
	if got.State != TaskFailed || got.Done != 2 || got.Failed != 1 || len(got.Failures) != 1 || got.Failures[0].Name != "2021-03-04_Y.png" || got.Failures[0].Error == "" {
		t.Fatalf("partly failed task: %+v", got)
	}
	if shots := waitingShots(t, f.s); len(shots) != 1 || !shots[21] {
		t.Fatalf("the failed screenshot is back on the page: %v", shots)
	}
	if err := os.WriteFile(filepath.Join(f.shots, "2021-03-04_Y.png"), []byte("shot bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	retry, err := f.s.RetryTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Total != 1 || retry.Label != "Move 1 screenshot to the Bin" {
		t.Fatalf("retry: %+v", retry)
	}
	f.drain(t)
	if got := f.task(t, retry.ID); got.State != TaskDone || got.Done != 1 {
		t.Fatalf("retried: %+v", got)
	}
}

// Deleting from the Bin is checked against the Bin at once, leaves it at
// once, and the writer deletes the cards in batches afterwards.
func TestTaskDeletesFromTheBin(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	keys := []string{"marked:1", "bin:" + f.binPlan + ":2"}
	if _, err := f.s.QueueBin(ctx, BinTask{Action: "delete", Keys: keys, Confirmation: "DELETE 2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a confirmation that ignores the widened batch was taken: %v", err)
	}
	task, err := f.s.QueueBin(ctx, BinTask{Action: "delete", Keys: keys, Confirmation: "DELETE 3"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Total != 3 || task.Kind != TaskBinDelete {
		t.Fatalf("queued: %+v", task)
	}
	visible, err := f.s.notQueued(ctx, mapValues(f.items(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range visible {
		if item.Key == "marked:1" || item.Group == "bin:"+f.binPlan {
			t.Fatalf("queued card still in the Bin: %s", item.Key)
		}
	}
	if _, err := f.s.QueueBin(ctx, BinTask{Action: "delete", Keys: []string{"marked:1"}, Confirmation: "DELETE 1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("a card already queued was queued again: %v", err)
	}
	f.drain(t)
	if got := f.task(t, task.ID); got.State != TaskDone || got.Done != 3 {
		t.Fatalf("deleted: %+v", got)
	}
	for _, name := range []string{"A.jpg", "B.jpg", "C.jpg"} {
		if exists(t, filepath.Join(f.archive, "2020/day", name)) {
			t.Fatalf("%s still in the archive", name)
		}
	}
	items := f.items(t)
	if _, ok := items["marked:1"]; ok {
		t.Fatal("deleted card still in the Bin")
	}
	if !exists(t, filepath.Join(f.archive, "2020/day/KEEP.jpg")) {
		t.Fatal("an unselected file was touched")
	}
}

// One card that left the Bin before its batch came up refuses the batch; the
// batch is split so only that card fails.
func TestTaskSplitsARefusedBatch(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	keys := []string{"marked:1", "shot:" + f.shot}
	task, err := f.s.QueueBin(ctx, BinTask{Action: "restore", Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.trash.Restore(ctx, []string{"shot:" + f.shot}); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	got := f.task(t, task.ID)
	if got.State != TaskFailed || got.Done != 1 || got.Failed != 1 {
		t.Fatalf("split batch: %+v", got)
	}
	if exists(t, filepath.Join(f.archive, ".culled")) && len(f.items(t)) == 0 {
		t.Fatal("unexpected empty Bin")
	}
}

// A writer that does not answer leaves the task waiting, not failed, and a
// cancel then leaves every file where it was.
func TestTaskWaitsForTheWriterAndCancels(t *testing.T) {
	f := newTaskFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.writer.Close()
	task, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{20, 21}, Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() { f.runner.run(ctx, task.ID); close(finished) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.task(t, task.ID).Note == "" {
		if time.Now().After(deadline) {
			t.Fatal("the task never said it was waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.task(t, task.ID); got.State != TaskRunning || got.Failed != 0 {
		t.Fatalf("waiting task: %+v", got)
	}
	if _, err := f.s.CancelTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled task kept running")
	}
	if got := f.task(t, task.ID); got.State != TaskCancelled || got.Cancelled != 2 || got.Done != 0 {
		t.Fatalf("cancelled: %+v", got)
	}
	if shots := waitingShots(t, f.s); !shots[20] || !shots[21] {
		t.Fatalf("cancelled screenshots are not back: %v", shots)
	}
}

// A restart finds a task it left running and carries on with it.
func TestTaskRunnerResumesAfterARestart(t *testing.T) {
	f := newTaskFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	task, err := f.s.QueueScreenshots(ctx, ScreenshotTask{AssetIDs: []int64{20, 21}, Action: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.write.Exec("UPDATE tasks SET state='running',chunk_now=0 WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	go f.runner.Run(ctx)
	defer cancel()
	deadline := time.Now().Add(5 * time.Second)
	for f.task(t, task.ID).State != TaskDone {
		if time.Now().After(deadline) {
			t.Fatalf("not resumed: %+v", f.task(t, task.ID))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mapValues(m map[string]TrashItem) []TrashItem {
	items := make([]TrashItem, 0, len(m))
	for _, item := range m {
		items = append(items, item)
	}
	return items
}

// Long videos go to the writer a gigabyte or so at a time, so their task shows
// progress as it goes; a group is never split, however big.
func TestBinChunksCloseAtAGigabyte(t *testing.T) {
	card := func(key, group string, size int64) TrashItem { return TrashItem{Key: key, Group: group, Size: size} }
	items := []TrashItem{
		card("marked:1", "marked:1", 600<<20), card("marked:2", "marked:2", 300<<20), card("marked:3", "marked:3", 300<<20),
		card("bin:p:4", "bin:p", 800<<20), card("bin:p:5", "bin:p", 800<<20),
		card("marked:6", "marked:6", 1<<20),
	}
	chunks := []int{}
	for _, item := range binChunks(items) {
		chunks = append(chunks, item.chunk)
	}
	if want := []int{0, 0, 1, 2, 2, 3}; !slices.Equal(chunks, want) {
		t.Fatalf("chunks %v, want %v", chunks, want)
	}
}
