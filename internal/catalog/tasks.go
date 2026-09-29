package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Tasks are the file operations that take a while: moving many screenshots
// to the Bin, deleting, emptying or restoring a large selection in the Bin.
// The page that asks gets its answer at once and carries on; the task waits
// in a queue, and one runner works through the queue oldest first, file by
// file or batch by batch through the private writer. Each step is recorded
// as it is done, so the Tasks list shows exact progress, a failure is told
// file by file, and a restart carries on where it stopped.
//
// A file waiting in a task has already left the page it was on: Screenshots
// and the Bin leave out what a task still has to do, so reading the page
// again does not bring it back.

// The kinds of task.
const (
	TaskScreenshotsRemove  = "screenshots.remove"
	TaskScreenshotsKeep    = "screenshots.keep"
	TaskScreenshotsRestore = "screenshots.restore"
	TaskBinRestore         = "bin.restore"
	TaskBinDelete          = "bin.delete"
	TaskBinPurge           = "bin.purge-now"
	TaskGooglePhotosAdd    = "google-photos.add"
)

// The states of a task, and of each file in it.
const (
	TaskQueued    = "queued"
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

// taskBatch is how many cards of the Bin go to the writer at once: the most
// one writer plan takes.
const taskBatch = engineBatch

// taskLimit bounds one task, so a single request cannot queue without end.
const taskLimit = 20000

// Task is a queued file operation and how far it has got.
type Task struct {
	// ID names the task in the routes below.
	ID string `json:"id"`
	// Kind is what the task does: screenshots.remove, screenshots.keep,
	// screenshots.restore, bin.restore, bin.delete, bin.purge-now or
	// google-photos.add.
	Kind string `json:"kind"`
	// Label says what the task does in words, such as Move 12 screenshots to
	// the Bin.
	Label string `json:"label"`
	// State is queued, running, done, failed or cancelled. failed means it
	// ran to the end but some files could not be handled; Failures says
	// which. cancelled means it was stopped before the end.
	State string `json:"state"`
	// Total counts the files in the task.
	Total int `json:"total"`
	// Done counts the files handled.
	Done int `json:"done"`
	// Failed counts the files that could not be handled.
	Failed int `json:"failed"`
	// Cancelled counts the files left alone because the task was stopped.
	Cancelled int `json:"cancelled"`
	// Bytes is the size of the files handled, in bytes.
	Bytes int64 `json:"bytes"`
	// KeptDays is set when files were deleted from the Bin with a grace
	// period: they stay on disk this many days, restorable from the Log.
	KeptDays int `json:"keptDays,omitempty"`
	// Note says why a running task is waiting, such as the writer not
	// answering; it carries on by itself once it can.
	Note string `json:"note,omitempty"`
	// Failures names up to 20 files that could not be handled, and why.
	Failures []TaskFailure `json:"failures"`
	// CreatedAt is when the task was queued, in RFC 3339 and UTC.
	CreatedAt string `json:"createdAt"`
	// StartedAt is when the runner first got to the task, in RFC 3339 and
	// UTC; absent while it waits its turn.
	StartedAt string `json:"startedAt,omitempty"`
	// FinishedAt is when the task ended, however it ended, in RFC 3339 and
	// UTC; absent while it is queued or running.
	FinishedAt string `json:"finishedAt,omitempty"`
	// Undoable says POST /api/tasks/{id}/undo can take the task back.
	Undoable bool `json:"undoable"`
	// UndoOf is the task this one takes back, when it is an undo.
	UndoOf string `json:"undoOf,omitempty"`
}

// TaskFailure is a file a task could not handle.
type TaskFailure struct {
	// Name is the file's name.
	Name string `json:"name"`
	// Error says why, in a sentence.
	Error string `json:"error"`
}

// TaskList is the Tasks list: what is queued or running, and what finished
// recently, newest first.
type TaskList struct {
	// Tasks are the tasks not cleared yet, newest first, at most 50.
	Tasks []Task `json:"tasks"`
	// Active counts the tasks queued or running.
	Active int `json:"active"`
}

// ScreenshotTask asks for screenshots to be moved to the Bin or copied into
// the archive, in the background.
type ScreenshotTask struct {
	// AssetIDs are the screenshots' ids in the catalogue, in the order to
	// handle them.
	AssetIDs []int64 `json:"assetIds"`
	// Action is remove, which moves each into the Bin, or keep, which copies
	// each kept screenshot into the archive under the date in its name.
	Action string `json:"action"`
}

// BinTask asks for cards of the Bin to be restored or deleted in the
// background.
type BinTask struct {
	// Action is restore, delete, purge-now for cards already deleted and
	// waiting out their grace period, or empty for everything in the Bin.
	Action string `json:"action"`
	// Keys are the cards' keys from GET /api/trash, or from GET
	// /api/trash/deleting for purge-now and restore. empty takes none. A
	// card that holds several files is acted on whole.
	Keys []string `json:"keys,omitempty"`
	// Confirmation is required for delete, purge-now and empty: DELETE and
	// the number of cards, counted after widening to whole batches, as in
	// DELETE 3.
	Confirmation string `json:"confirmation,omitempty"`
}

// ErrQueued is returned for files a task still has to handle.
var ErrQueued = errors.New("some of these files are already waiting in a task")

// taskItem is one file of a task as it is queued.
type taskItem struct {
	seq        int
	chunk      int
	assetID    int64
	key        string
	group      string
	sourceTask string
	sourceSeq  int
	name       string
	size       int64
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// createTask queues a task, refusing files another task still has to handle.
func (s *Store) createTask(ctx context.Context, kind, label, undoOf string, items []taskItem) (Task, error) {
	if len(items) == 0 {
		return Task{}, ErrInvalid
	}
	if len(items) > taskLimit {
		return Task{}, fmt.Errorf("%w: at most %d files at once", ErrInvalid, taskLimit)
	}
	id, err := randomID()
	if err != nil {
		return Task{}, err
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	busy, err := tx.PrepareContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_items WHERE state='queued' AND ((?<>0 AND asset_id=?) OR (?<>'' AND trash_key=?)))")
	if err != nil {
		return Task{}, err
	}
	defer busy.Close()
	if kind != TaskScreenshotsRestore {
		for _, item := range items {
			var taken bool
			if err := busy.QueryRowContext(ctx, item.assetID, item.assetID, item.key, item.key).Scan(&taken); err != nil {
				return Task{}, err
			}
			if taken {
				return Task{}, ErrQueued
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO tasks(id,kind,label,state,created_at,undo_of) VALUES(?,?,?,?,?,?)", id, kind, label, TaskQueued, nowUTC(), nullable(undoOf)); err != nil {
		return Task{}, err
	}
	insert, err := tx.PrepareContext(ctx, "INSERT INTO task_items(task_id,seq,chunk,asset_id,trash_key,trash_group,source_task,source_seq,name,size,state) VALUES(?,?,?,?,?,?,?,?,?,?,'queued')")
	if err != nil {
		return Task{}, err
	}
	defer insert.Close()
	for seq, item := range items {
		var asset any
		if item.assetID != 0 {
			asset = item.assetID
		}
		if _, err := insert.ExecContext(ctx, id, seq, item.chunk, asset, nullable(item.key), nullable(item.group), nullable(item.sourceTask), item.sourceSeq, item.name, item.size); err != nil {
			return Task{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Task(ctx, id)
}

const taskColumns = `t.id,t.kind,t.label,t.state,t.created_at,COALESCE(t.started_at,''),COALESCE(t.finished_at,''),t.note,t.kept_days,COALESCE(t.undo_of,''),
	(SELECT count(*) FROM task_items i WHERE i.task_id=t.id),
	(SELECT count(*) FROM task_items i WHERE i.task_id=t.id AND i.state='done'),
	(SELECT count(*) FROM task_items i WHERE i.task_id=t.id AND i.state='failed'),
	(SELECT count(*) FROM task_items i WHERE i.task_id=t.id AND i.state='cancelled'),
	(SELECT COALESCE(sum(i.size),0) FROM task_items i WHERE i.task_id=t.id AND i.state='done'),
	EXISTS(SELECT 1 FROM tasks u WHERE u.undo_of=t.id),
	EXISTS(SELECT 1 FROM task_items i WHERE i.task_id=t.id AND i.state='done' AND ` + stillInBin + `)`

// stillInBin holds for a task item whose screenshot is still in the Bin by
// the plan that item moved it with: not brought back, moved again or
// deleted from the Bin since.
const stillInBin = `EXISTS(SELECT 1 FROM screenshot_plans p WHERE p.id=i.plan_id AND json_extract(p.body,'$.state')='bin'
	AND NOT EXISTS(SELECT 1 FROM trash_deletions d WHERE d.grp='shot:'||p.id))`

func (s *Store) scanTasks(ctx context.Context, where string, args ...any) ([]Task, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT "+taskColumns+" FROM tasks t "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		var t Task
		var undone, inBin bool
		if err := rows.Scan(&t.ID, &t.Kind, &t.Label, &t.State, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.Note, &t.KeptDays, &t.UndoOf, &t.Total, &t.Done, &t.Failed, &t.Cancelled, &t.Bytes, &undone, &inBin); err != nil {
			return nil, err
		}
		t.Undoable = t.Kind == TaskScreenshotsRemove && !undone && (inBin || t.State == TaskQueued || t.State == TaskRunning)
		t.Failures = []TaskFailure{}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].Failed == 0 {
			continue
		}
		failures, err := s.read.QueryContext(ctx, "SELECT name,error FROM task_items WHERE task_id=? AND state='failed' ORDER BY seq LIMIT 20", tasks[i].ID)
		if err != nil {
			return nil, err
		}
		for failures.Next() {
			var f TaskFailure
			if err := failures.Scan(&f.Name, &f.Error); err != nil {
				failures.Close()
				return nil, err
			}
			tasks[i].Failures = append(tasks[i].Failures, f)
		}
		failures.Close()
	}
	return tasks, nil
}

// Task is one task by its id.
func (s *Store) Task(ctx context.Context, id string) (Task, error) {
	tasks, err := s.scanTasks(ctx, "WHERE t.id=?", id)
	if err != nil {
		return Task{}, err
	}
	if len(tasks) == 0 {
		return Task{}, sql.ErrNoRows
	}
	return tasks[0], nil
}

// Tasks lists the tasks not cleared, newest first.
func (s *Store) Tasks(ctx context.Context) (TaskList, error) {
	tasks, err := s.scanTasks(ctx, "WHERE t.cleared=0 ORDER BY t.created_at DESC, t.rowid DESC LIMIT 50")
	if err != nil {
		return TaskList{}, err
	}
	list := TaskList{Tasks: tasks}
	err = s.read.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE state IN ('queued','running')").Scan(&list.Active)
	return list, err
}

// CancelTask stops a task: files it has not started on are left alone. A
// batch already at the writer is finished, since a move is never cut short.
func (s *Store) CancelTask(ctx context.Context, id string) (Task, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	var state string
	var inFlight sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT state,chunk_now FROM tasks WHERE id=?", id).Scan(&state, &inFlight); err != nil {
		return Task{}, err
	}
	if state != TaskQueued && state != TaskRunning {
		return Task{}, fmt.Errorf("%w: the task has already finished", ErrConflict)
	}
	flying := int64(-1)
	if inFlight.Valid {
		flying = inFlight.Int64
	}
	if _, err := tx.ExecContext(ctx, "UPDATE task_items SET state='cancelled' WHERE task_id=? AND state='queued' AND chunk<>?", id, flying); err != nil {
		return Task{}, err
	}
	if state == TaskQueued {
		if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state='cancelled',finished_at=? WHERE id=?", nowUTC(), id); err != nil {
			return Task{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Task(ctx, id)
}

// RetryTask queues again the files a finished task failed on or left alone.
func (s *Store) RetryTask(ctx context.Context, id string) (Task, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.State == TaskQueued || task.State == TaskRunning {
		return Task{}, fmt.Errorf("%w: the task is still running", ErrConflict)
	}
	items, err := s.taskItems(ctx, "task_id=? AND state IN ('failed','cancelled')", id)
	if err != nil {
		return Task{}, err
	}
	if len(items) == 0 {
		return Task{}, fmt.Errorf("%w: nothing in the task is left to do", ErrConflict)
	}
	rechunk(items)
	return s.createTask(ctx, task.Kind, retryLabel(task.Kind, len(items)), task.UndoOf, items)
}

// UndoTask takes back a move of screenshots to the Bin: what is not moved
// yet is left where it is, and what was moved, or is moving now, is brought
// back by a task of its own.
func (s *Store) UndoTask(ctx context.Context, id string) (Task, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if !task.Undoable {
		return Task{}, fmt.Errorf("%w: this task cannot be taken back", ErrConflict)
	}
	if task.State == TaskQueued || task.State == TaskRunning {
		if _, err := s.CancelTask(ctx, id); err != nil && !errors.Is(err, ErrConflict) {
			return Task{}, err
		}
	}
	// Only what is still in the Bin comes back: a screenshot brought back,
	// moved again or deleted from the Bin since is left as it is.
	rows, err := s.read.QueryContext(ctx, "SELECT seq,name,size FROM task_items i WHERE task_id=? AND (state='queued' OR (state='done' AND "+stillInBin+")) ORDER BY seq DESC", id)
	if err != nil {
		return Task{}, err
	}
	var items []taskItem
	for rows.Next() {
		item := taskItem{sourceTask: id}
		if err := rows.Scan(&item.sourceSeq, &item.name, &item.size); err != nil {
			rows.Close()
			return Task{}, err
		}
		item.chunk = len(items)
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Task{}, err
	}
	if len(items) == 0 {
		return s.Task(ctx, id)
	}
	return s.createTask(ctx, TaskScreenshotsRestore, "Bring back "+counted(len(items), "screenshot", "screenshots")+" from the Bin", id, items)
}

// ClearTasks takes finished tasks off the list.
func (s *Store) ClearTasks(ctx context.Context) error {
	_, err := s.write.ExecContext(ctx, "UPDATE tasks SET cleared=1 WHERE state NOT IN ('queued','running')")
	return err
}

func (s *Store) taskItems(ctx context.Context, where string, args ...any) ([]taskItem, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT seq,chunk,COALESCE(asset_id,0),COALESCE(trash_key,''),COALESCE(trash_group,''),COALESCE(source_task,''),source_seq,name,size FROM task_items WHERE "+where+" ORDER BY seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []taskItem
	for rows.Next() {
		var item taskItem
		if err := rows.Scan(&item.seq, &item.chunk, &item.assetID, &item.key, &item.group, &item.sourceTask, &item.sourceSeq, &item.name, &item.size); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// rechunk numbers the chunks from 0 again, keeping items that shared one
// together.
func rechunk(items []taskItem) {
	next, seen := 0, map[int]int{}
	for i := range items {
		n, ok := seen[items[i].chunk]
		if !ok {
			n = next
			seen[items[i].chunk] = n
			next++
		}
		items[i].chunk = n
	}
}

func retryLabel(kind string, n int) string {
	switch kind {
	case TaskScreenshotsRemove:
		return "Move " + counted(n, "screenshot", "screenshots") + " to the Bin"
	case TaskScreenshotsKeep:
		return "Copy " + counted(n, "screenshot", "screenshots") + " into the archive"
	case TaskScreenshotsRestore:
		return "Bring back " + counted(n, "screenshot", "screenshots") + " from the Bin"
	case TaskBinRestore:
		return "Restore " + counted(n, "file", "files") + " from the Bin"
	case TaskBinPurge:
		return "Delete " + counted(n, "file", "files") + " now"
	case TaskGooglePhotosAdd:
		return "Add " + counted(n, "photo", "photos") + " from Google Photos to the library"
	default:
		return "Delete " + counted(n, "file", "files") + " from the Bin"
	}
}

// queuedAssets and queuedKeys are what tasks still have to do, which the
// pages leave out.
func (s *Store) queuedKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT trash_key FROM task_items WHERE state='queued' AND trash_key IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = true
	}
	return keys, rows.Err()
}

// notQueued leaves out the cards a task still has to do.
func (s *Store) notQueued(ctx context.Context, items []TrashItem) ([]TrashItem, error) {
	queued, err := s.queuedKeys(ctx)
	if err != nil || len(queued) == 0 {
		return items, err
	}
	kept := make([]TrashItem, 0, len(items))
	for _, item := range items {
		if !queued[item.Key] {
			kept = append(kept, item)
		}
	}
	return kept, nil
}

// deletingNotQueued leaves out the waiting files a task still has to do.
func (s *Store) deletingNotQueued(ctx context.Context, items []DeletingItem) ([]DeletingItem, error) {
	queued, err := s.queuedKeys(ctx)
	if err != nil || len(queued) == 0 {
		return items, err
	}
	kept := make([]DeletingItem, 0, len(items))
	for _, item := range items {
		if !queued[item.Key] {
			kept = append(kept, item)
		}
	}
	return kept, nil
}

// screenshotNotQueued is the condition that leaves out screenshots a task
// still has to move.
const screenshotNotQueued = " AND NOT EXISTS(SELECT 1 FROM task_items ti WHERE ti.asset_id=a.id AND ti.state='queued')"

// QueueScreenshots checks a request to move or copy screenshots and queues it.
func (s *Store) QueueScreenshots(ctx context.Context, request ScreenshotTask) (Task, error) {
	if request.Action != "remove" && request.Action != "keep" {
		return Task{}, ErrInvalid
	}
	if len(request.AssetIDs) == 0 || len(request.AssetIDs) > taskLimit {
		return Task{}, ErrInvalid
	}
	lookup, err := s.read.PrepareContext(ctx, "SELECT shots.name,a.size_bytes FROM assets a JOIN screenshot_items shots ON shots.asset_id=a.id WHERE a.id=? AND shots.state='waiting'")
	if err != nil {
		return Task{}, err
	}
	defer lookup.Close()
	seen := map[int64]bool{}
	items := make([]taskItem, 0, len(request.AssetIDs))
	for _, id := range request.AssetIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		item := taskItem{chunk: len(items), assetID: id}
		if err := lookup.QueryRowContext(ctx, id).Scan(&item.name, &item.size); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Task{}, fmt.Errorf("%w: a screenshot is no longer waiting; read the page again", ErrConflict)
			}
			return Task{}, err
		}
		items = append(items, item)
	}
	kind, label := TaskScreenshotsRemove, "Move "+counted(len(items), "screenshot", "screenshots")+" to the Bin"
	if request.Action == "keep" {
		kind, label = TaskScreenshotsKeep, "Copy "+counted(len(items), "screenshot", "screenshots")+" into the archive"
	}
	return s.createTask(ctx, kind, label, "", items)
}

// QueueBin checks a request for the Bin against the Bin as it is now, as the
// writer will again, and queues it in batches of whole cards.
func (s *Store) QueueBin(ctx context.Context, request BinTask) (Task, error) {
	var list func(context.Context) ([]TrashItem, error)
	kind := ""
	switch request.Action {
	case "restore":
		kind, list = TaskBinRestore, s.held
	case "delete", "empty":
		kind, list = TaskBinDelete, s.Trash
	case "purge-now":
		kind, list = TaskBinPurge, s.deletingTrash
	default:
		return Task{}, ErrInvalid
	}
	visible := func(ctx context.Context) ([]TrashItem, error) {
		items, err := list(ctx)
		if err != nil {
			return nil, err
		}
		return s.notQueued(ctx, items)
	}
	var items []TrashItem
	var err error
	if request.Action == "empty" {
		if len(request.Keys) > 0 {
			return Task{}, ErrInvalid
		}
		if items, err = visible(ctx); err == nil && len(items) == 0 {
			return Task{}, fmt.Errorf("%w: the Bin is already empty", ErrConflict)
		}
	} else {
		items, err = resolveTrash(ctx, request.Keys, visible)
	}
	if err != nil {
		if errors.Is(err, errTrashChanged) || errors.Is(err, errTrashEmptySelection) {
			return Task{}, fmt.Errorf("%w: %s", ErrConflict, err.Error())
		}
		return Task{}, err
	}
	if kind != TaskBinRestore && request.Confirmation != DeleteConfirmation(len(items)) {
		return Task{}, fmt.Errorf("%w: the Bin now holds a different number of these files; read it again and confirm %s", ErrConflict, DeleteConfirmation(len(items)))
	}
	queued := binChunks(items)
	label := retryLabel(kind, len(items))
	if request.Action == "empty" {
		label = "Empty the Bin of " + counted(len(items), "file", "files")
	}
	return s.createTask(ctx, kind, label, "", queued)
}

// binChunks packs cards into batches for the writer, keeping each group
// whole: a group bigger than a batch goes on its own.
func binChunks(items []TrashItem) []taskItem {
	var groups [][]TrashItem
	at := map[string]int{}
	for _, item := range items {
		i, ok := at[item.Group]
		if !ok {
			i = len(groups)
			at[item.Group] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], item)
	}
	queued := make([]taskItem, 0, len(items))
	chunk, size := 0, 0
	for _, group := range groups {
		if size > 0 && size+len(group) > taskBatch {
			chunk++
			size = 0
		}
		for _, item := range group {
			queued = append(queued, taskItem{chunk: chunk, key: item.Key, group: item.Group, name: item.Name, size: item.Size})
		}
		size += len(group)
	}
	return queued
}

// TaskRunner works through the queue, one task and one batch at a time.
type TaskRunner struct {
	s        *Store
	upstream string
	secret   string
	client   *http.Client
	wake     chan struct{}
	// pause is how long to wait before asking a writer that did not answer
	// again; it doubles up to a minute.
	pause time.Duration

	mu       sync.Mutex
	changed  chan struct{}
	finished map[string]func(context.Context)
}

// NewTaskRunner runs tasks through the private writer at upstream.
func (s *Store) NewTaskRunner(upstream, secret string) *TaskRunner {
	return &TaskRunner{s: s, upstream: upstream, secret: secret, pause: 5 * time.Second,
		client: &http.Client{Timeout: 60 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		wake:   make(chan struct{}, 1), changed: make(chan struct{})}
}

// Wake tells the runner a task was queued.
func (t *TaskRunner) Wake() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
	t.announce()
}

// OnFinished has done called each time a task of this kind ends, however it
// ended, such as to catalogue the files a task added to the archive.
func (t *TaskRunner) OnFinished(kind string, done func(context.Context)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished == nil {
		t.finished = map[string]func(context.Context){}
	}
	t.finished[kind] = done
}

// Changed is closed the next time a task moves on.
func (t *TaskRunner) Changed() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.changed
}

func (t *TaskRunner) announce() {
	t.mu.Lock()
	close(t.changed)
	t.changed = make(chan struct{})
	t.mu.Unlock()
}

// Run works through the queue until ctx ends. A task left running by a
// restart carries on from the batch it was on: every writer step checks the
// files again, so a batch finished just before the restart is refused rather
// than done twice, and its files are told as such.
func (t *TaskRunner) Run(ctx context.Context) {
	if _, err := t.s.write.ExecContext(ctx, "UPDATE tasks SET state='queued',chunk_now=NULL,note='' WHERE state='running'"); err != nil && ctx.Err() == nil {
		log.Printf("tasks: %v", err)
	}
	// Finished tasks are kept a month, for the list and for undo.
	t.s.write.ExecContext(ctx, "DELETE FROM tasks WHERE state NOT IN ('queued','running') AND finished_at<?", time.Now().UTC().AddDate(0, -1, 0).Format(time.RFC3339))
	for ctx.Err() == nil {
		var id string
		err := t.s.read.QueryRowContext(ctx, "SELECT id FROM tasks WHERE state='queued' ORDER BY created_at,rowid LIMIT 1").Scan(&id)
		if err == nil {
			t.run(ctx, id)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-t.wake:
		case <-time.After(30 * time.Second):
		}
	}
}

func (t *TaskRunner) run(ctx context.Context, id string) {
	var kind string
	if err := t.s.write.QueryRowContext(ctx, "UPDATE tasks SET state='running',started_at=COALESCE(started_at,?) WHERE id=? AND state='queued' RETURNING kind", nowUTC(), id).Scan(&kind); err != nil {
		return
	}
	t.announce()
	pause := t.pause
	for ctx.Err() == nil {
		var chunk int
		err := t.s.read.QueryRowContext(ctx, "SELECT min(chunk) FROM task_items WHERE task_id=? AND state='queued' HAVING count(*)>0", id).Scan(&chunk)
		if errors.Is(err, sql.ErrNoRows) {
			t.finish(ctx, id)
			t.mu.Lock()
			done := t.finished[kind]
			t.mu.Unlock()
			if done != nil {
				done(ctx)
				t.announce()
			}
			return
		}
		if err != nil {
			return
		}
		// The batch is marked in flight before it is read, so a cancel from
		// now on leaves it to finish rather than racing it.
		if _, err := t.s.write.ExecContext(ctx, "UPDATE tasks SET chunk_now=? WHERE id=?", chunk, id); err != nil {
			return
		}
		items, err := t.s.taskItems(ctx, "task_id=? AND chunk=? AND state='queued'", id, chunk)
		if err != nil {
			return
		}
		if len(items) == 0 {
			continue
		}
		outcome, unreachable := t.step(ctx, kind, items)
		if unreachable != nil {
			// The writer did not answer: nothing is known to have happened,
			// so the batch waits and is asked again, and the task can still
			// be cancelled meanwhile.
			t.s.write.ExecContext(ctx, "UPDATE tasks SET chunk_now=NULL,note=? WHERE id=?", "Waiting for the Bin service to answer. It carries on by itself.", id)
			t.announce()
			select {
			case <-ctx.Done():
				return
			case <-time.After(pause):
			}
			pause = min(2*pause, time.Minute)
			continue
		}
		pause = t.pause
		if outcome.split {
			// One card of the batch was refused, which refuses the batch;
			// each group is tried on its own so only that card fails.
			t.s.splitChunk(ctx, id, items)
			continue
		}
		t.s.record(ctx, id, outcome)
		t.announce()
	}
}

func (t *TaskRunner) finish(ctx context.Context, id string) {
	t.s.write.ExecContext(ctx, `UPDATE tasks SET chunk_now=NULL,note='',finished_at=?,
		state=CASE WHEN EXISTS(SELECT 1 FROM task_items WHERE task_id=tasks.id AND state='cancelled') THEN 'cancelled'
		WHEN EXISTS(SELECT 1 FROM task_items WHERE task_id=tasks.id AND state='failed') THEN 'failed' ELSE 'done' END WHERE id=?`, nowUTC(), id)
	t.announce()
}

// stepOutcome is what one batch came to.
type stepOutcome struct {
	// items are the batch's files.
	items []taskItem
	// failed maps a file's place in the batch to why it failed; the rest
	// were done.
	failed map[int]string
	// plans are the writer's plans for each file, by place, for undo.
	plans    map[int]string
	keptDays int
	// split asks for the batch to be tried group by group.
	split bool
}

// errUnreachable is a writer that did not answer, or answered in a way that
// says nothing about the files.
var errUnreachable = errors.New("the writer did not answer")

func (t *TaskRunner) step(ctx context.Context, kind string, items []taskItem) (stepOutcome, error) {
	outcome := stepOutcome{items: items, failed: map[int]string{}, plans: map[int]string{}}
	switch kind {
	case TaskScreenshotsRemove, TaskScreenshotsKeep:
		action := "remove"
		if kind == TaskScreenshotsKeep {
			action = "keep"
		}
		for i, item := range items {
			var plan ScreenshotPlan
			refused, err := t.post(ctx, "/screenshot/preview", ScreenshotChoice{AssetID: item.assetID, Action: action}, &plan)
			if err == nil && refused == "" {
				refused, err = t.post(ctx, "/screenshot/execute", PlanRef{ID: plan.ID}, &plan)
			}
			if err != nil {
				return outcome, err
			}
			if refused != "" {
				outcome.failed[i] = refused
				continue
			}
			outcome.plans[i] = plan.ID
		}
	case TaskScreenshotsRestore:
		for i, item := range items {
			var planID, state string
			err := t.s.read.QueryRowContext(ctx, "SELECT plan_id,state FROM task_items WHERE task_id=? AND seq=?", item.sourceTask, item.sourceSeq).Scan(&planID, &state)
			if err != nil || state != TaskDone || planID == "" {
				// It never reached the Bin, so there is nothing to bring back.
				outcome.failed[i] = ""
				continue
			}
			var plan ScreenshotPlan
			refused, err := t.post(ctx, "/screenshot/undo", PlanRef{ID: planID}, &plan)
			if err != nil {
				return outcome, err
			}
			if refused != "" {
				outcome.failed[i] = refused
			}
		}
	case TaskGooglePhotosAdd:
		for i, item := range items {
			id, ok := takeoutTaskID(item.key)
			if !ok {
				outcome.failed[i] = "This photo is not one from Google Photos."
				continue
			}
			var plan GooglePhotosPlan
			refused, err := t.post(ctx, "/google-photos/preview", GooglePhotosChoice{ItemID: id}, &plan)
			if err == nil && refused == "" {
				refused, err = t.post(ctx, "/google-photos/execute", PlanRef{ID: plan.ID}, &plan)
			}
			if err != nil {
				return outcome, err
			}
			if refused != "" {
				outcome.failed[i] = refused
				continue
			}
			outcome.plans[i] = plan.ID
		}
	case TaskBinRestore, TaskBinDelete, TaskBinPurge:
		route := map[string]string{TaskBinRestore: "/trash/restore", TaskBinDelete: "/trash/delete", TaskBinPurge: "/trash/purge-now"}[kind]
		request := TrashSelection{Keys: make([]string, len(items))}
		for i, item := range items {
			request.Keys[i] = item.key
		}
		if kind != TaskBinRestore {
			request.Confirmation = DeleteConfirmation(len(items))
		}
		var result TrashResult
		refused, err := t.post(ctx, route, request, &result)
		if err != nil {
			return outcome, err
		}
		if refused != "" {
			groups := map[string]bool{}
			for _, item := range items {
				groups[item.group] = true
			}
			if len(groups) > 1 {
				outcome.split = true
				return outcome, nil
			}
			for i := range items {
				outcome.failed[i] = refused
			}
			return outcome, nil
		}
		outcome.keptDays = result.KeptDays
		reasons := map[string]string{}
		for _, f := range result.Failures {
			reasons[f.Name] = f.Error
		}
		for i, item := range items {
			if reason, ok := reasons[item.name]; ok {
				outcome.failed[i] = sentence(reason)
			}
		}
	}
	return outcome, nil
}

// post sends one request to the writer. refused is the writer's sentence
// when it turned the request down, which is about the files; err is set
// when nothing is known about them.
func (t *TaskRunner) post(ctx context.Context, path string, body, into any) (refused string, err error) {
	if t.upstream == "" || len(t.secret) < 32 {
		return "", errUnreachable
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", t.upstream+path, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Bin-Key", t.secret)
	response, err := t.client.Do(request)
	if err != nil {
		return "", errUnreachable
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return "", errUnreachable
	}
	switch {
	case response.StatusCode == http.StatusOK:
		if json.Unmarshal(answer, into) != nil {
			return "", errUnreachable
		}
		return "", nil
	case response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusBadRequest:
		var said struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(answer, &said) != nil || said.Error == "" {
			return "The writer refused this file.", nil
		}
		return sentence(said.Error), nil
	default:
		return "", errUnreachable
	}
}

// sentence starts the writer's lower-case reason with a capital and ends it
// with a full stop.
func sentence(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return reason
	}
	reason = strings.ToUpper(reason[:1]) + reason[1:]
	if !strings.HasSuffix(reason, ".") {
		reason += "."
	}
	return reason
}

// record saves how a batch went. What the writer did is recorded whatever
// happened to the task meanwhile, since it is what is now true on disk.
func (s *Store) record(ctx context.Context, id string, outcome stepOutcome) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for i, item := range outcome.items {
		reason, failed := outcome.failed[i]
		switch {
		case failed && reason == "":
			// Nothing to do after all: an undo of a file that never moved.
			tx.ExecContext(ctx, "DELETE FROM task_items WHERE task_id=? AND seq=?", id, item.seq)
		case failed:
			tx.ExecContext(ctx, "UPDATE task_items SET state='failed',error=? WHERE task_id=? AND seq=?", reason, id, item.seq)
		default:
			tx.ExecContext(ctx, "UPDATE task_items SET state='done',error='',plan_id=? WHERE task_id=? AND seq=?", outcome.plans[i], id, item.seq)
		}
	}
	if outcome.keptDays > 0 {
		tx.ExecContext(ctx, "UPDATE tasks SET kept_days=? WHERE id=?", outcome.keptDays, id)
	}
	tx.ExecContext(ctx, "UPDATE tasks SET chunk_now=NULL,note='' WHERE id=?", id)
	tx.Commit()
}

// splitChunk gives each group of a batch a chunk of its own, after the
// task's last.
func (s *Store) splitChunk(ctx context.Context, id string, items []taskItem) {
	var last int
	if s.read.QueryRowContext(ctx, "SELECT max(chunk) FROM task_items WHERE task_id=?", id).Scan(&last) != nil {
		return
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	groups := map[string]int{}
	for _, item := range items {
		n, ok := groups[item.group]
		if !ok {
			last++
			n = last
			groups[item.group] = n
		}
		tx.ExecContext(ctx, "UPDATE task_items SET chunk=? WHERE task_id=? AND seq=?", n, id, item.seq)
	}
	tx.ExecContext(ctx, "UPDATE tasks SET chunk_now=NULL WHERE id=?", id)
	tx.Commit()
}
