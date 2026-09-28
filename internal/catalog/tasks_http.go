package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"daddy-cull/next/internal/api"
)

// taskFail answers a refused task request with the reason in words.
func taskFail(w http.ResponseWriter, err error, invalid string) {
	detail := func(prefix error) string {
		_, after, found := strings.Cut(err.Error(), prefix.Error()+": ")
		if !found {
			return ""
		}
		return sentence(after)
	}
	switch {
	case errors.Is(err, ErrQueued):
		api.Fail(w, 409, "Some of these files are already waiting in a task. Follow it under Tasks.")
	case errors.Is(err, ErrConflict):
		if said := detail(ErrConflict); said != "" {
			api.Fail(w, 409, said)
			return
		}
		api.Fail(w, 409, "This changed since the page read it. Read it again and retry.")
	case errors.Is(err, ErrInvalid):
		if said := detail(ErrInvalid); said != "" {
			api.Fail(w, 400, invalid+" "+said)
			return
		}
		api.Fail(w, 400, invalid)
	case errors.Is(err, sql.ErrNoRows):
		api.Fail(w, 404, "There is no such task. Finished tasks are kept for a month.")
	default:
		api.Fail(w, 503, "The catalogue could not be read. Try again in a moment.")
	}
}

// deletes says whether a kind of task deletes files, which needs the delete
// permission.
func deletes(kind string) bool { return kind == TaskBinDelete || kind == TaskBinPurge }

// Routes serves the task queue.
func (t *TaskRunner) Routes(m *api.Mux) {
	s := t.s
	m.Book().Tag("Tasks", "Long file operations run in the background, one after another, so the page that asked for one can carry on. Each is recorded file by file: the list shows how far it has got, a failure is told file by file, and a restart carries on where it stopped. A file waiting in a task has already left Screenshots or the Bin.")
	notFound := api.Error{Status: 404, When: "There is no such task"}
	conflict := api.Error{Status: 409, When: "The request no longer matches the files, or the files are already waiting in another task. The body says which."}
	idParam := api.Path("id", "The task's id, from GET /api/tasks or the answer that queued it.", "0123456789abcdef0123456789abcdef")
	lookup := func(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, id string) (Task, error)) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		task, err := do(ctx, r.PathValue("id"))
		if err != nil {
			taskFail(w, err, "")
			return
		}
		t.Wake()
		writeJSON(w, task)
	}
	queued := func(w http.ResponseWriter, task Task, err error, invalid string) {
		if err != nil {
			taskFail(w, err, invalid)
			return
		}
		t.Wake()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(task)
	}

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/tasks", Tag: "Tasks", Needs: api.Read,
		Summary: "List tasks",
		Doc:     "What is queued or running, and what finished and has not been cleared, newest first. Poll it while Active is above 0.",
		Returns: TaskList{}, Errors: []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		list, err := s.Tasks(ctx)
		if err != nil {
			taskFail(w, err, "")
			return
		}
		writeJSON(w, list)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/tasks/{id}", Tag: "Tasks", Needs: api.Read,
		Summary: "Get a task",
		Params:  []api.Param{idParam},
		Returns: Task{}, Errors: []api.Error{notFound, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		task, err := s.Task(ctx, r.PathValue("id"))
		if err != nil {
			taskFail(w, err, "")
			return
		}
		writeJSON(w, task)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/tasks/screenshots", Addon: AddonScreenshots, Tag: "Tasks", Needs: api.Bin,
		Summary: "Move or copy screenshots in the background",
		Doc: "Queues moving screenshots into the recoverable Bin, or copying kept ones into the archive, and answers at once. " +
			"They leave GET /api/screenshots straight away. Each is planned and carried out as POST /api/screenshot-actions/preview and execute would, one after another.",
		Body: ScreenshotTask{}, Returns: Task{}, Status: http.StatusAccepted,
		Errors: []api.Error{{Status: 400, When: "The action is not remove or keep, or no screenshots were named"}, conflict, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var request ScreenshotTask
		if !decodeBody(w, r, 1<<20, &request, "Send the screenshots' ids and an action of remove or keep.") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err := s.QueueScreenshots(ctx, request)
		queued(w, task, err, "Send the screenshots' ids and an action of remove or keep.")
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/tasks/bin", Tag: "Tasks", Needs: api.Bin,
		Summary: "Restore or delete from the Bin in the background",
		Doc: "Queues restoring cards, deleting them, deleting cards already waiting out their grace period now, or emptying the Bin, and answers at once. " +
			"The request is checked against the Bin as it is, and the cards leave GET /api/trash straight away. Deleting, as the synchronous routes under /api/trash, also needs the delete permission and the confirmation.",
		Body: BinTask{}, Returns: Task{}, Status: http.StatusAccepted,
		Errors: []api.Error{{Status: 400, When: "The action is not one of restore, delete, purge-now or empty"}, {Status: 403, When: "An addon asked to delete without the delete permission"}, conflict, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var request BinTask
		if !decodeBody(w, r, trashBodyLimit, &request, "Send an action of restore, delete, purge-now or empty, with the cards' keys.") {
			return
		}
		if request.Action != "restore" && !api.Allowed(r.Context(), api.Delete) {
			api.Fail(w, 403, "Deleting from the Bin needs the delete permission.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err := s.QueueBin(ctx, request)
		queued(w, task, err, "Send an action of restore, delete, purge-now or empty, with the cards' keys.")
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/tasks/{id}/cancel", Tag: "Tasks", Needs: api.Bin,
		Summary: "Stop a task",
		Doc:     "Files the task has not started on are left alone and come back to their page. A batch already at the writer is finished, since a move is never cut short.",
		Params:  []api.Param{idParam},
		Returns: Task{}, Errors: []api.Error{notFound, {Status: 409, When: "The task has already finished"}, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		lookup(w, r, s.CancelTask)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/tasks/{id}/retry", Tag: "Tasks", Needs: api.Bin,
		Summary: "Try a task's leftovers again",
		Doc:     "Queues a new task for the files a finished task failed on or left alone. Retrying a deletion needs the delete permission.",
		Params:  []api.Param{idParam},
		Returns: Task{}, Status: http.StatusAccepted,
		Errors: []api.Error{notFound, {Status: 403, When: "An addon asked to retry a deletion without the delete permission"}, {Status: 409, When: "The task is still running, or nothing is left to do"}, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if !api.Allowed(ctx, api.Delete) {
			if task, err := s.Task(ctx, r.PathValue("id")); err == nil && deletes(task.Kind) {
				api.Fail(w, 403, "Retrying a deletion needs the delete permission.")
				return
			}
		}
		task, err := s.RetryTask(ctx, r.PathValue("id"))
		queued(w, task, err, "")
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/tasks/{id}/undo", Tag: "Tasks", Needs: api.Bin,
		Summary: "Take back moving screenshots to the Bin",
		Doc:     "Stops the move if it is still running and queues bringing back, last first, every screenshot it moved. Answers with the new task; when it is done the screenshots wait for review again.",
		Params:  []api.Param{idParam},
		Returns: Task{}, Status: http.StatusAccepted,
		Errors: []api.Error{notFound, {Status: 409, When: "The task is not a move of screenshots to the Bin, or was taken back already"}, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		task, err := s.UndoTask(ctx, r.PathValue("id"))
		queued(w, task, err, "")
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/tasks/clear", Tag: "Tasks", Needs: api.Bin,
		Summary: "Clear finished tasks",
		Doc:     "Takes every finished task off the list. What they did stays done, and the Log still has it.",
		Returns: TaskList{}, Errors: []api.Error{offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := s.ClearTasks(ctx); err != nil {
			taskFail(w, err, "")
			return
		}
		list, err := s.Tasks(ctx)
		if err != nil {
			taskFail(w, err, "")
			return
		}
		t.announce()
		writeJSON(w, list)
	})
}
