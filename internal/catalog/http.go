package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"daddy-cull/next/internal/api"
)

// viewerLocation is the time zone the page says it is in, so a day of review
// ends at the viewer's midnight rather than the server's.
func viewerLocation(r *http.Request) *time.Location {
	if name := r.URL.Query().Get("tz"); name != "" && len(name) < 64 {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.Local
}

// Handler serves the catalogue's routes on their own, as the tests use them.
// The app serves them with every other route in one api.Mux (see Routes).
func (s *Store) Handler() http.Handler {
	m := api.NewMux(api.NewBook())
	m.Guard(api.SameOrigin)
	s.Routes(m)
	return m
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(value)
}

// failFor answers an error from the catalogue: a request it refused, one
// that lost a race, a record that is not there, or a catalogue that could not
// be read.
func failFor(w http.ResponseWriter, err error, invalid string) {
	switch {
	case errors.Is(err, ErrInvalid):
		api.Fail(w, 400, invalid)
	case errors.Is(err, ErrConflict):
		api.Fail(w, 409, "This changed since the page read it, perhaps in another tab. Read it again and retry.")
	case errors.Is(err, sql.ErrNoRows):
		api.Fail(w, 404, "There is no such file in the catalogue.")
	default:
		api.Fail(w, 503, "The catalogue could not be read. Try again in a moment.")
	}
}

// intParam reads an optional integer from the query, or def when it is not
// given. A value that is not a number is refused.
func intParam(w http.ResponseWriter, r *http.Request, name string, def int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		api.Fail(w, 400, name+" should be a whole number.")
		return 0, false
	}
	return n, true
}

// decodeBody reads exactly one JSON value of at most limit bytes, refusing
// fields the route does not take.
func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, into any, problem string) bool {
	if !api.IsJSON(r) {
		api.Fail(w, 415, "Send JSON, with Content-Type: application/json.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		api.Fail(w, 400, problem)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		api.Fail(w, 400, "Send one JSON value.")
		return false
	}
	return true
}

var (
	unreadable = api.Error{Status: 503, When: "The catalogue could not be read"}
	refused    = api.Error{Status: 400, When: "A parameter is not one the route takes"}
	notJSON    = api.Error{Status: 415, When: "The body is not sent as application/json"}
	stale      = api.Error{Status: 409, When: "The record changed since it was read; read it again and retry"}
	offSite    = api.Error{Status: 403, When: "The request came from a page on another site"}
	zoneParam  = api.Query("tz", "string", "The reader's IANA time zone, such as Europe/London, so a day of review ends at their midnight. The server's zone by default.")
)

// Turn is the body that turns files.
type Turn struct {
	// IDs are the files to turn, up to 500.
	IDs []int64 `json:"ids"`
	// Quarters is how far to turn them: 1 to 3 quarter turns clockwise, or
	// -1 to -3 anticlockwise.
	Quarters int `json:"quarters"`
}

// Turned is how far each file is now turned from how it was taken.
type Turned struct {
	// Turns maps each file's id to its quarter turns clockwise, 0 to 3.
	Turns map[string]int `json:"turns"`
}

// PairChoice joins or splits a RAW+JPEG pair.
type PairChoice struct {
	// RawID is the RAW file's id.
	RawID int64 `json:"rawId"`
	// PartnerID is the id of the JPEG taken with it. The two ids may be sent
	// either way round.
	PartnerID int64 `json:"partnerId"`
	// Paired is true to show the two as one photo again, false to show them apart.
	Paired *bool `json:"paired"`
}

// Paired is whether a pair now shows as one photo.
type Paired struct {
	// Paired is true when the two show as one photo, false when apart.
	Paired bool `json:"paired"`
}

// GraceChoice sets how long a deleted file waits before it is gone.
type GraceChoice struct {
	// GraceDays is how many days a file deleted from the Bin waits on disk, from 0 to 365.
	GraceDays *int `json:"graceDays"`
}

// SoundChoice sets whether videos start muted.
type SoundChoice struct {
	// Muted is true for videos to start without sound, false for them to
	// start with it. It is required.
	Muted *bool `json:"muted"`
}

// ReviewRequest marks a date reviewed.
type ReviewRequest struct {
	// RequestID makes a retry safe: the same id, 8 to 80 characters, is only acted on once.
	RequestID string `json:"requestId"`
}

// Done says a request was carried out.
type Done struct {
	// OK is always true; a request that fails answers with an error instead.
	OK bool `json:"ok"`
}

// Generation is a number that changes whenever files arrive in or leave the catalogue.
type Generation struct {
	// Generation is the current value. Compare it only with a value read
	// earlier: a different one means the catalogue changed, not how.
	Generation int64 `json:"generation"`
}

// ReadThrough marks notifications read.
type ReadThrough struct {
	// Through is the id of the newest notification read; it and every older one are marked read.
	Through int64 `json:"through"`
}

// Arrivals marks newly arrived files seen.
type Arrivals struct {
	// IDs are the files seen, 1 to 10,000 of them. An id that is not a new
	// arrival, or was already seen, is passed over.
	IDs []int64 `json:"ids"`
}

// Routes serves the catalogue: the calendar and dates, choices, duplicates,
// the Bin's listing, the log and the settings, plus the routes of the
// addons whose records it keeps.
func (s *Store) Routes(m *api.Mux) {
	book := m.Book()
	book.Tag("Library", "Counts across the whole library, and whether the catalogue has changed.")
	book.Tag("Calendar", "The year's calendar, each date's files from every year, and the review streak.")
	book.Tag("Choices", "Keeping and removing files, favourites, turns and pairs. A choice is only a record: files move when the Bin is emptied into, never before.")
	book.Tag("Duplicates", "Files that are byte-for-byte copies of one another.")
	book.Tag("Bin", "What is in the Bin, from every source, and what waits to be deleted for good.")
	book.Tag("Log", "Every choice ever saved, newest first.")
	book.Tag("Notifications", "What arrived in the archive and what happened while nobody was looking.")
	book.Tag("Settings", "Cull's settings.")

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/stats", Tag: "Library", Needs: api.Read,
		Summary: "Count the library",
		Doc:     "Everything the frame of the app shows: how many files there are, how many dates are reviewed, the streak, the Bin's count and each addon's count. Cheap enough to ask after every change.",
		Params:  []api.Param{zoneParam},
		Returns: Stats{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		stats, err := s.Stats(ctx, viewerLocation(r))
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, stats)
	})
	// Cheap enough to ask every minute: an open page compares it with the
	// value it loaded with to learn that the archive changed under it.
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/catalogue", Tag: "Library", Needs: api.Read,
		Summary: "Read the catalogue's generation",
		Doc:     "A number that changes whenever files arrive in the archive or leave it. A page compares it with the one it was read at to know when to read itself again. For a stream of changes as they happen, use the event stream instead.",
		Returns: Generation{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		generation, err := s.CatalogueGeneration(ctx)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, Generation{Generation: generation})
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/assets", Tag: "Library", Needs: api.Read,
		Summary: "Page through files",
		Doc:     "Every file in capture order, a page at a time. Pass the next value of one page as after to read the one after it; an empty next means there is no more.",
		Params: []api.Param{
			api.Query("after", "string", "The next value from the previous page."),
			api.Query("limit", "integer", "How many files to return, 1 to 500. 80 by default."),
			{Name: "kind", In: "query", Type: "string", Doc: "Only photos or only videos.", Enum: []string{"image", "video"}},
			api.Query("source", "string", "Only files from this source, such as archive."),
			{Name: "view", In: "query", Type: "string", Doc: "review reads the review queue instead, with status, from, matches and groups.", Enum: []string{"review"}},
			api.Query("status", "string", "With view=review, only files with this status."),
			api.Query("from", "string", "With view=review, start at this date."),
			api.Query("matches", "boolean", "With view=review, 1 for only files that have copies."),
			api.Query("groups", "boolean", "With view=review, 1 to show each group of copies once."),
		},
		Returns: Page{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		limit, ok := intParam(w, r, "limit", 80)
		if !ok {
			return
		}
		q := r.URL.Query()
		var p Page
		var err error
		if q.Get("view") == "review" {
			p, err = s.page(ctx, q.Get("after"), q.Get("kind"), "", limit, true, q.Get("from"), q.Get("matches") == "1", q.Get("status"), q.Get("groups") == "1")
		} else {
			p, err = s.Page(ctx, q.Get("after"), q.Get("kind"), q.Get("source"), limit)
		}
		if err != nil {
			failFor(w, err, "limit should be 1 to 500, kind image or video, and after a next value from a page.")
			return
		}
		writeJSON(w, p)
	})
	for _, relation := range []struct {
		path, summary, doc string
		read               func(context.Context, int64) ([]Asset, error)
	}{
		{"/api/assets/{id}/alternatives", "List a file's other versions", "Other files that are the same picture in another form: the same photo exported again, or a copy at another size.", s.Alternatives},
		{"/api/assets/{id}/related", "List files related to a file", "Files that belong with this one, such as the other half of a Live Photo or a RAW+JPEG pair, and near copies.", s.Related},
	} {
		m.HandleFunc(api.Route{
			Method: "GET", Path: relation.path, Tag: "Library", Needs: api.Read,
			Summary: relation.summary, Doc: relation.doc,
			Params:  []api.Param{api.PathInt("id", "The file's id.", "1")},
			Returns: []Asset{},
			Errors:  []api.Error{{Status: 400, When: "The id is not a file id"}, {Status: 404, When: "There is no such file"}, unreadable},
		}, func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil || id < 1 {
				api.Fail(w, 400, "The id should be a file's id, a whole number from 1.")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			assets, err := relation.read(ctx, id)
			if err != nil {
				failFor(w, err, "")
				return
			}
			writeJSON(w, assets)
		})
	}

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/year", Tag: "Calendar", Needs: api.Read,
		Summary: "Read the year's calendar",
		Doc:     "Every calendar date with how many files it holds across all years and whether it is reviewed, plus overall progress, the streak and this week's review.",
		Params:  []api.Param{zoneParam},
		Returns: CalendarData{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		now := time.Now()
		data, err := s.Calendar(ctx, now)
		if err != nil {
			failFor(w, err, "")
			return
		}
		activity, err := s.Activity(ctx, viewerLocation(r), now)
		if err != nil {
			failFor(w, err, "")
			return
		}
		data.Streak = activity.Streak
		data.Week = CalendarWeek{Days: activity.WeekDays, Seconds: activity.WeekSeconds}
		writeJSON(w, data)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/streak", Tag: "Calendar", Needs: api.Read,
		Summary: "Read the review streak",
		Doc:     "How many days in a row something was reviewed, the best run, and every day that had a review, in the reader's time zone.",
		Params:  []api.Param{zoneParam},
		Returns: StreakCalendar{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		data, err := s.Streak(ctx, viewerLocation(r), time.Now())
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, data)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/today/{md}", Tag: "Calendar", Needs: api.Read,
		Summary: "Read a calendar date",
		Doc:     "Every file taken on this date in every year, grouped by year, with each file's status. This is what the Today page shows.",
		Params:  []api.Param{api.Path("md", "The month and day, as MM-DD.", "09-07")},
		Returns: TodayData{},
		Errors:  []api.Error{{Status: 400, When: "The date is not a real MM-DD"}, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		data, err := s.Today(ctx, r.PathValue("md"))
		if err != nil {
			failFor(w, err, "The date should be a real month and day, as MM-DD.")
			return
		}
		writeJSON(w, data)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/dates/{md}/reviewed", Tag: "Calendar", Needs: api.Review,
		Summary: "Mark a date reviewed",
		Doc:     "Marks every year filed under the date reviewed, and keeps each file on it that nobody decided on. The answer lists the files it kept, with their revisions, so the choice can be undone.",
		Params:  []api.Param{api.Path("md", "The month and day, as MM-DD.", "09-07")},
		Body:    ReviewRequest{},
		Returns: DateReviewed{},
		Errors:  []api.Error{{Status: 400, When: "The date is not a real MM-DD"}, stale, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var body ReviewRequest
		if !decodeBody(w, r, 1024, &body, "Send {\"requestId\": \"...\"}.") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := s.MarkDateReviewed(ctx, r.PathValue("md"), body.RequestID)
		if err != nil {
			failFor(w, err, "The date should be a real month and day, as MM-DD, and the requestId 8 to 80 characters.")
			return
		}
		writeJSON(w, result)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/day-progress", Tag: "Calendar", Needs: api.Review,
		Summary: "Set one year's progress on a date",
		Doc:     "Marks one day of one year pending or done. Marking a whole calendar date reviewed is usually what is wanted instead.",
		Body:    DayProgressChange{},
		Returns: DayProgressResult{},
		Errors:  []api.Error{{Status: 400, When: "The day or status is not one Cull knows"}, stale, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var change DayProgressChange
		if !decodeBody(w, r, 4096, &change, "Send a day, a status and a requestId.") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		result, err := s.SetDayProgress(ctx, change)
		if err != nil {
			failFor(w, err, "The day should be YYYY-MM-DD, the status pending or done, and the requestId 8 to 100 characters.")
			return
		}
		writeJSON(w, result)
	})

	decisions := func(batch bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var ds []Decision
			if batch {
				if !decodeBody(w, r, 16384, &ds, "Send a list of choices.") {
					return
				}
			} else {
				var d Decision
				if !decodeBody(w, r, 16384, &d, "Send one choice.") {
					return
				}
				ds = []Decision{d}
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			result, err := s.DecideBatch(ctx, ds)
			if err != nil {
				failFor(w, err, "Each choice needs a requestId, an assetId, the revision it was read at, and a status of unreviewed, keep, later or cull.")
				return
			}
			if batch {
				writeJSON(w, result)
			} else {
				writeJSON(w, result[0])
			}
		}
	}
	choiceDoc := "A status of cull removes the file: it is marked for the Bin and moved there when the Bin is next filled. keep keeps it, later sets it aside, and unreviewed takes a choice back. favourite is saved alongside, and is mirrored to Immich while that addon is on. expectedRevision is the revision the file was read at; if someone else changed it since, nothing is saved and the answer is 409. The same requestId is only acted on once, so a retry is safe."
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/decisions", Tag: "Choices", Needs: api.Review,
		Summary: "Save a choice",
		Doc:     choiceDoc,
		Body:    Decision{},
		Returns: Saved{},
		Errors:  []api.Error{{Status: 400, When: "The choice is not one Cull takes"}, stale, notJSON, offSite, unreadable},
	}, decisions(false))
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/decisions/batch", Tag: "Choices", Needs: api.Review,
		Summary: "Save several choices at once",
		Doc:     "Saves every choice or none of them, in one transaction. " + choiceDoc,
		Body:    []Decision{},
		Returns: []Saved{},
		Errors:  []api.Error{{Status: 400, When: "A choice is not one Cull takes"}, stale, notJSON, offSite, unreadable},
	}, decisions(true))
	// Turning a file is Cull's own record of how it should be shown; the file
	// in the archive is not touched.
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/turns", Tag: "Choices", Needs: api.Review,
		Summary: "Turn files",
		Doc:     "Turns how files are shown in Cull, by quarter turns. The files themselves are not touched.",
		Body:    Turn{},
		Returns: Turned{},
		Errors:  []api.Error{{Status: 400, When: "More than 500 files, or a turn other than 1 to 3 quarters either way"}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var input Turn
		if !decodeBody(w, r, 16<<10, &input, "Send ids and quarters.") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		turned, err := s.Turn(ctx, input.IDs, input.Quarters)
		if err != nil {
			failFor(w, err, "Send up to 500 different files and a turn of 1 to 3 quarters either way.")
			return
		}
		out := make(map[string]int, len(turned))
		for id, quarters := range turned {
			out[strconv.FormatInt(id, 10)] = quarters
		}
		writeJSON(w, Turned{Turns: out})
	})
	// A RAW+JPEG pair shows as one photo until the reviewer splits it. Only
	// the catalogue changes: both files stay where they are.
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/pairs", Tag: "Choices", Needs: api.Review,
		Summary: "Join or split a RAW+JPEG pair",
		Doc:     "A RAW file and the JPEG taken with it show as one photo until they are split. Only the catalogue changes; both files stay where they are.",
		Body:    PairChoice{},
		Returns: Paired{},
		Errors:  []api.Error{{Status: 400, When: "The two files are not a pair"}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var input PairChoice
		if !decodeBody(w, r, 1024, &input, "Send rawId, partnerId and paired.") {
			return
		}
		if input.Paired == nil || input.RawID <= 0 || input.PartnerID <= 0 {
			api.Fail(w, 400, "Send rawId, partnerId and paired.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.SetPaired(ctx, input.RawID, input.PartnerID, *input.Paired); err != nil {
			failFor(w, err, "These files are not a pair.")
			return
		}
		writeJSON(w, Paired{Paired: *input.Paired})
	})

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/duplicates", Tag: "Duplicates", Needs: api.Read,
		Summary: "List groups of identical files",
		Doc:     "Groups of files whose bytes are identical, as proven by a full hash of each.",
		Params: []api.Param{
			api.Query("md", "string", "Only groups with a file on this month and day, as MM-DD."),
			api.Query("limit", "integer", "How many groups to return, 1 to 1000. 100 by default."),
		},
		Returns: []DuplicateGroup{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		limit, ok := intParam(w, r, "limit", 100)
		if !ok {
			return
		}
		groups, err := s.ExactDuplicates(ctx, r.URL.Query().Get("md"), limit)
		if err != nil {
			failFor(w, err, "md should be MM-DD and limit 1 to 1000.")
			return
		}
		writeJSON(w, groups)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/duplicate-report", Tag: "Duplicates", Needs: api.Read,
		Summary: "Read the duplicates report",
		Doc:     "The groups of identical files, and how far the answer can be trusted: how many of the files that could be copies have been hashed.",
		Params: []api.Param{
			api.Query("md", "string", "Only groups with a file on this month and day, as MM-DD."),
			api.Query("limit", "integer", "How many groups to return, 1 to 1000. 100 by default."),
		},
		Returns: DuplicateReport{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		limit, ok := intParam(w, r, "limit", 100)
		if !ok {
			return
		}
		report, err := s.DuplicateOverview(ctx, r.URL.Query().Get("md"), limit)
		if err != nil {
			failFor(w, err, "md should be MM-DD and limit 1 to 1000.")
			return
		}
		writeJSON(w, report)
	})

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/trash", Tag: "Bin", Needs: api.Read,
		Summary: "List what is in the Bin",
		Doc:     "Every file in the Bin, from every source, newest first: files removed while reviewing, screenshots, and anything an addon moved there.",
		Returns: []TrashItem{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := s.Trash(ctx)
		if err == nil {
			// What a task is still restoring or deleting has left the Bin.
			items, err = s.notQueued(ctx, items)
		}
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, items)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/trash/deleting", Tag: "Bin", Needs: api.Read,
		Summary: "List files waiting to be deleted for good",
		Doc:     "Files deleted from the Bin wait on disk for the grace period before they are gone. This lists them, the grace period, and how the last run went.",
		Returns: DeletingReport{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		report, err := s.Deleting(ctx)
		if err == nil {
			report.Items, err = s.deletingNotQueued(ctx, report.Items)
		}
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, report)
	})
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/marked", Tag: "Bin", Needs: api.Read,
		Summary: "List files marked for the Bin",
		Doc:     "Files a reviewer removed that have not been moved into the Bin yet, with when they were removed.",
		Returns: []MarkedAsset{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		marked, err := s.MarkedForBin(ctx, 1000)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, marked)
	})

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/log", Tag: "Log", Needs: api.Read,
		Summary: "Read the log",
		Doc:     "Every choice ever saved, newest first, with what it replaced and where the file is now.",
		Params: []api.Param{
			api.Query("limit", "integer", "How many to return, 1 to 500. 200 by default."),
			api.Query("offset", "integer", "How many of the newest to skip."),
		},
		Returns: []HistoryEvent{},
		Errors:  []api.Error{refused, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		limit, ok := intParam(w, r, "limit", 200)
		if !ok {
			return
		}
		offset, ok := intParam(w, r, "offset", 0)
		if !ok {
			return
		}
		events, err := s.History(ctx, limit, offset)
		if err != nil {
			failFor(w, err, "limit should be 1 to 500 and offset 0 or more.")
			return
		}
		writeJSON(w, events)
	})

	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/notifications", Tag: "Notifications", Needs: api.Read,
		Summary: "List notifications",
		Doc:     "The newest 50 notifications and how many are unread.",
		Returns: Notifications{},
		Errors:  []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		result, err := s.ListNotifications(ctx, 50)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, result)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/notifications/read", Tag: "Notifications", Needs: api.Review,
		Summary: "Mark notifications read",
		Doc:     "Marks the notification with this id and every older one read, so the bell's count goes down. Notifications already read are left as they are. Nothing is returned.",
		Body:    ReadThrough{},
		Errors:  []api.Error{{Status: 400, When: "through is not a notification id"}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var body ReadThrough
		if !decodeBody(w, r, 256, &body, "Send {\"through\": <id>}.") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.ReadNotifications(ctx, body.Through); err != nil {
			failFor(w, err, "through should be a notification's id.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/arrivals/seen", Tag: "Notifications", Needs: api.Review,
		Summary: "Mark newly arrived files seen",
		Doc:     "A file that arrived after its date was reviewed shows as new until it is seen.",
		Body:    Arrivals{},
		Errors:  []api.Error{{Status: 400, When: "An id is not a file id"}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var body Arrivals
		if !decodeBody(w, r, 256<<10, &body, "Send {\"ids\": [...]}.") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.SeeArrivals(ctx, body.IDs); err != nil {
			failFor(w, err, "Each id should be a file's id.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// The grace period is a setting, not a file operation, so this process saves
	// it; the writer reads it each time its reaper runs.
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/settings/bin", Tag: "Settings", Needs: api.Settings,
		Summary: "Set the Bin's grace period",
		Doc:     "How many days a file deleted from the Bin waits on disk before it is gone. The answer is the list of files waiting, under the new period.",
		Body:    GraceChoice{},
		Returns: DeletingReport{},
		Errors:  []api.Error{{Status: 400, When: fmt.Sprintf("The period is not 0 to %d days", MaxGraceDays)}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var input GraceChoice
		if !decodeBody(w, r, 1024, &input, "Send {\"graceDays\": <days>}.") {
			return
		}
		if input.GraceDays == nil {
			api.Fail(w, 400, "Send {\"graceDays\": <days>}.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.SetGraceDays(ctx, *input.GraceDays); err != nil {
			failFor(w, err, fmt.Sprintf("Choose a number of days from 0 to %d.", MaxGraceDays))
			return
		}
		report, err := s.Deleting(ctx)
		if err == nil {
			report.Items, err = s.deletingNotQueued(ctx, report.Items)
		}
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, report)
	})
	// Whether clips start muted is a preference, so it is saved here and read by
	// every browser with the rest of the stats.
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/settings/video", Tag: "Settings", Needs: api.Settings,
		Summary: "Set whether videos start muted",
		Doc:     "Saves whether videos start muted, for every browser: the setting is read back as videoMuted in GET /api/stats. The answer repeats the choice saved.",
		Body:    SoundChoice{},
		Returns: SoundChoice{},
		Errors:  []api.Error{{Status: 400, When: "muted is missing"}, notJSON, offSite, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		var input SoundChoice
		if !decodeBody(w, r, 1024, &input, "Send {\"muted\": true} or {\"muted\": false}.") {
			return
		}
		if input.Muted == nil {
			api.Fail(w, 400, "Send {\"muted\": true} or {\"muted\": false}.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := s.SetVideoMuted(ctx, *input.Muted); err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, input)
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/reindex", Tag: "Settings", Needs: api.Settings,
		Summary: "Rebuild the calendar and the related-file index",
		Doc:     "Reads the catalogue again to rebuild which files are related and which dates they fall on. Needed only after the catalogue was changed outside Cull.",
		Returns: Done{},
		Errors:  []api.Error{notJSON, offSite, {Status: 503, When: "The index could not be rebuilt"}},
	}, func(w http.ResponseWriter, r *http.Request) {
		if !api.IsJSON(r) {
			api.Fail(w, 415, "Send JSON, with Content-Type: application/json.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.IndexRelated(ctx); err != nil {
			api.Fail(w, 503, "The index could not be rebuilt.")
			return
		}
		if err := s.IndexCalendar(ctx); err != nil {
			api.Fail(w, 503, "The index could not be rebuilt.")
			return
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('snapshot_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
			api.Fail(w, 503, "The index could not be rebuilt.")
			return
		}
		writeJSON(w, Done{OK: true})
	})

	s.addonRoutes(m)
}
