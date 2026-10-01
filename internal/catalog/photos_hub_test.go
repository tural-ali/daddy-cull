package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const fakePhotosKey = "photos-agent-test-key-9d2e4b7a1c3f5e8d"

// A thumbnail only has to look like a JPEG to be kept.
var fakeJPEG = []byte{0xff, 0xd8, 0xff, 0xe0, 'f', 'a', 'k', 'e'}

type photosClock struct{ at time.Time }

func (c *photosClock) now() time.Time             { return c.at }
func (c *photosClock) advance(step time.Duration) { c.at = c.at.Add(step) }

func photosHubFixture(t *testing.T) (*PhotosHub, *Store, *photosClock) {
	t.Helper()
	f := newPhotosFixture(t)
	h := NewPhotosHub(f.s, fakePhotosKey)
	clock := &photosClock{at: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	h.now = clock.now
	return h, f.s, clock
}

// claimNow takes whatever work is waiting without sitting through a long poll.
func claimNow(t *testing.T, h *PhotosHub) *PhotosTask {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	task, err := h.Claim(ctx)
	if err != nil || task == nil {
		t.Fatalf("no task: %v", err)
	}
	return task
}

// checkAndMatch runs a check in which Photos holds every entry once, except
// the ones named in missing.
func checkAndMatch(t *testing.T, h *PhotosHub, missing ...string) PhotosJobView {
	t.Helper()
	view, err := h.StartCheck(context.Background())
	if err != nil || view.State != "queued_check" {
		t.Fatalf("start %s: %v", view.State, err)
	}
	task := claimNow(t, h)
	if task.Task != "check" || task.JobID != view.ID || len(task.Entries) != view.ToCheck {
		t.Fatalf("task %+v", task)
	}
	h.Seen(PhotosHeartbeat{Version: "test", Access: "authorized", Job: view.ID, Stage: "matching", Done: 1, Total: 2})
	skip := map[string]bool{}
	for _, name := range missing {
		skip[name] = true
	}
	report := PhotosMatchReport{}
	for i, entry := range task.Entries {
		if skip[entry.Name] {
			report.Missing = append(report.Missing, entry.ID)
			continue
		}
		report.Matches = append(report.Matches, PhotosReportedMatch{ID: entry.ID, How: "exact", Photos: []PhotosReportedAsset{{
			ID: fmt.Sprintf("UUID-%d/L0/001", i), Name: entry.Name, Created: entry.Day + "T10:00:00Z", Thumb: fakeJPEG,
		}}})
	}
	if err = h.Matches(view.ID, report); err != nil {
		t.Fatal(err)
	}
	if err = h.Checked(view.ID); err != nil {
		t.Fatal(err)
	}
	view, _ = h.Job(view.ID)
	if view.State != "planned" {
		t.Fatalf("state %s", view.State)
	}
	return view
}

func rowByName(rows []PhotosRow, name string) PhotosRow {
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	return PhotosRow{}
}

func TestPhotosJobRunsFromCheckToRecordedResult(t *testing.T) {
	h, s, _ := photosHubFixture(t)
	ctx := context.Background()
	view := checkAndMatch(t, h, "IMG_0100.PNG")
	// The check's last progress count must not linger on as a stalled bar.
	if view.Stage != "" || view.Done != 0 || view.Total != 0 {
		t.Fatalf("planned job still shows progress: %s %d/%d", view.Stage, view.Done, view.Total)
	}

	if len(view.Missing) != 1 || view.Missing[0].Name != "IMG_0100.PNG" {
		t.Fatalf("missing %+v", view.Missing)
	}
	row := rowByName(view.Delete, "IMG_1001.HEIC")
	if row.How != "exact" || len(row.Photos) != 1 || row.Preview != "/api/media/1" || !strings.HasPrefix(row.Photos[0].Thumb, "/api/photos/thumb/"+view.ID+"/") {
		t.Fatalf("row %+v", row)
	}
	var n int
	fmt.Sscanf(strings.TrimPrefix(row.Photos[0].Thumb, "/api/photos/thumb/"+view.ID+"/"), "%d", &n)
	if thumb, ok := h.Thumb(view.ID, n); !ok || !bytes.Equal(thumb, fakeJPEG) {
		t.Fatalf("thumb %d not served", n)
	}
	if len(view.Held) != 2 || view.Undated != 1 {
		t.Fatalf("held %+v undated %d", view.Held, view.Undated)
	}

	fav := rowByName(view.Favourite, "IMG_7000.HEIC")
	view, err := h.Apply(ctx, view.ID, []string{row.ID, rowByName(view.Delete, "IMG_3000.JPG").ID}, []string{fav.ID})
	if err != nil || view.State != "queued_apply" || len(view.Selected) != 3 {
		t.Fatalf("apply %s %v: %v", view.State, view.Selected, err)
	}
	task := claimNow(t, h)
	if task.Task != "apply" || len(task.Deletes) != 2 || len(task.Favourites) != 1 || task.Favourites[0].Photos[0] != fav.Photos[0].ID {
		t.Fatalf("apply task %+v", task)
	}
	if status := h.Status(); status.Job.State != "applying" {
		t.Fatalf("state %s", status.Job.State)
	}

	// The pair was confirmed gone; the single photograph's deletion was
	// cancelled at the macOS dialog, so it is not recorded.
	err = h.Applied(ctx, view.ID, PhotosAppliedReport{
		Favourites: []PhotosAppliedItem{{ID: fav.ID, Done: true}},
		Deletes:    []PhotosAppliedItem{{ID: rowByName(view.Delete, "IMG_3000.JPG").ID, Done: true}, {ID: row.ID, Done: false, Error: "still in the library"}},
		Note:       "The deletion was cancelled on the Mac.",
	})
	if err != nil {
		t.Fatal(err)
	}
	view, _ = h.Job(view.ID)
	if view.State != "done" || view.Result.Deleted != 1 || view.Result.NotDeleted != 1 || view.Result.Favourited != 1 {
		t.Fatalf("result %s %+v", view.State, view.Result)
	}
	if outcome := rowByName(view.Delete, "IMG_1001.HEIC"); outcome.Outcome != "not-deleted" || outcome.OutcomeError != "still in the library" {
		t.Fatalf("outcome %+v", outcome)
	}
	var keys []string
	rows, _ := s.read.Query("SELECT asset_key||' '||action FROM photos_sync ORDER BY 1")
	for rows.Next() {
		var key string
		rows.Scan(&key)
		keys = append(keys, key)
	}
	rows.Close()
	if strings.Join(keys, ",") != "asset:16 favourite,asset:4 delete,asset:5 delete" {
		t.Fatalf("recorded %v", keys)
	}
	// A retried report is accepted and changes nothing.
	if err = h.Applied(ctx, view.ID, PhotosAppliedReport{}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	// The next check offers only what is still new.
	next, err := h.StartCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.ToCheck != view.ToCheck-2 {
		t.Fatalf("next check has %d entries, want %d", next.ToCheck, view.ToCheck-2)
	}
}

func TestPhotosApplyLeavesOutWhatChangedSinceTheCheck(t *testing.T) {
	h, s, _ := photosHubFixture(t)
	ctx := context.Background()
	view := checkAndMatch(t, h)
	restored, kept := rowByName(view.Delete, "IMG_1001.HEIC"), rowByName(view.Delete, "IMG_6748.PNG")
	// The reviewer restores one file in another tab after the check.
	if _, err := s.write.Exec("UPDATE decisions SET status='keep' WHERE asset_id=1"); err != nil {
		t.Fatal(err)
	}
	view, err := h.Apply(ctx, view.ID, []string{restored.ID, kept.ID}, nil)
	if err != nil || view.Skipped != 1 || len(view.Selected) != 1 || view.Selected[0] != kept.ID {
		t.Fatalf("apply %+v: %v", view.Selected, err)
	}
	if task := claimNow(t, h); len(task.Deletes) != 1 || task.Deletes[0].ID != kept.ID {
		t.Fatalf("task %+v", task)
	}
	// A report about an entry that was not asked for is refused whole.
	if err = h.Applied(ctx, view.ID, PhotosAppliedReport{Deletes: []PhotosAppliedItem{{ID: kept.ID, Done: true}, {ID: restored.ID, Done: true}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unrequested entry: %v", err)
	}
	if n := countQuery(ctx, s.read, "SELECT count(*) FROM photos_sync"); n != 0 {
		t.Fatalf("%d rows recorded from a refused report", n)
	}
}

func TestPhotosApplyRefusesWhatWasNotMatched(t *testing.T) {
	h, _, _ := photosHubFixture(t)
	ctx := context.Background()
	view := checkAndMatch(t, h, "IMG_1001.HEIC")
	for name, ids := range map[string][2][]string{
		"unmatched":    {{"delete:asset:1"}, nil},
		"unknown":      {{"delete:asset:999"}, nil},
		"wrong action": {nil, {"delete:asset:12"}},
		"nothing":      {nil, nil},
		"twice":        {{"delete:asset:12", "delete:asset:12"}, nil},
	} {
		if _, err := h.Apply(ctx, view.ID, ids[0], ids[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := h.Apply(ctx, "another", []string{"delete:asset:12"}, nil); !errors.Is(err, ErrPhotosStale) {
		t.Errorf("wrong job: %v", err)
	}
}

func TestPhotosCancelStopsACheckButNeverAnApply(t *testing.T) {
	h, _, _ := photosHubFixture(t)
	ctx := context.Background()
	view, _ := h.StartCheck(ctx)
	if _, err := h.StartCheck(ctx); !errors.Is(err, ErrPhotosBusy) {
		t.Fatalf("second check while one waits: %v", err)
	}
	task := claimNow(t, h)
	if cancel := h.Seen(PhotosHeartbeat{Version: "1", Access: "authorized", Job: task.JobID, Stage: "reading", Done: 10, Total: 100}); cancel {
		t.Fatal("told to cancel a live check")
	}
	if status := h.Status(); status.Job.Done != 10 || status.Job.Total != 100 || !status.Agent.Online || status.Agent.Access != "authorized" {
		t.Fatalf("status %+v", status)
	}
	if _, err := h.Cancel(view.ID); err != nil {
		t.Fatal(err)
	}
	if cancel := h.Seen(PhotosHeartbeat{Job: task.JobID}); !cancel {
		t.Fatal("the helper was not told the check was cancelled")
	}
	if err := h.Matches(view.ID, PhotosMatchReport{}); !errors.Is(err, ErrPhotosStale) {
		t.Fatalf("matches after cancel: %v", err)
	}

	view = checkAndMatch(t, h)
	view, _ = h.Apply(ctx, view.ID, []string{rowByName(view.Delete, "IMG_6748.PNG").ID}, nil)
	claimNow(t, h)
	if _, err := h.Cancel(view.ID); !errors.Is(err, ErrPhotosStale) {
		t.Fatalf("cancelled while applying: %v", err)
	}
}

func TestPhotosTimeLimits(t *testing.T) {
	h, s, clock := photosHubFixture(t)
	ctx := context.Background()

	// A change nobody picks up does not run days later.
	view := checkAndMatch(t, h)
	target := rowByName(view.Delete, "IMG_6748.PNG")
	h.Apply(ctx, view.ID, []string{target.ID}, nil)
	clock.advance(photosApplyPickup + time.Second)
	if status := h.Status(); status.Job.State != "failed" || !strings.Contains(status.Job.Error, "nothing was changed") {
		t.Fatalf("stale apply: %+v", status.Job)
	}

	// A plan must be checked again once old.
	view = checkAndMatch(t, h)
	clock.advance(photosPlanLifetime + time.Second)
	if _, err := h.Apply(ctx, view.ID, []string{rowByName(view.Delete, "IMG_6748.PNG").ID}, nil); !errors.Is(err, ErrPhotosStale) {
		t.Fatalf("old plan applied: %v", err)
	}

	// A check whose helper goes silent fails.
	h.StartCheck(ctx)
	claimNow(t, h)
	clock.advance(photosCheckSilence + time.Second)
	if status := h.Status(); status.Job.State != "failed" || status.Agent.Online {
		t.Fatalf("silent check: %+v %+v", status.Job, status.Agent)
	}

	// An apply whose helper goes silent fails, but what the helper later
	// reports it really did is still recorded.
	view = checkAndMatch(t, h)
	target = rowByName(view.Delete, "IMG_6748.PNG")
	h.Apply(ctx, view.ID, []string{target.ID}, nil)
	claimNow(t, h)
	clock.advance(photosApplySilence + time.Second)
	if status := h.Status(); status.Job.State != "failed" {
		t.Fatalf("silent apply: %+v", status.Job)
	}
	if err := h.Applied(ctx, view.ID, PhotosAppliedReport{Deletes: []PhotosAppliedItem{{ID: target.ID, Done: true}}}); err != nil {
		t.Fatalf("late report: %v", err)
	}
	if n := countQuery(ctx, s.read, "SELECT count(*) FROM photos_sync WHERE asset_key='asset:12'"); n != 1 {
		t.Fatal("the late report was not recorded")
	}

	// Thumbnails are freed an hour after the job ends.
	view, _ = h.Job(view.ID)
	thumb := rowByName(view.Delete, "IMG_6748.PNG").Photos[0].Thumb
	if thumb == "" {
		t.Fatal("no thumbnail before expiry")
	}
	clock.advance(photosThumbLifetime + time.Second)
	view, _ = h.Job(view.ID)
	if rowByName(view.Delete, "IMG_6748.PNG").Photos[0].Thumb != "" || view.Delete == nil {
		t.Fatal("thumbnail outlived its job")
	}
}

func TestPhotosNothingToSyncNeedsNoHelper(t *testing.T) {
	h := NewPhotosHub(testStore(t), fakePhotosKey)
	view, err := h.StartCheck(context.Background())
	if err != nil || view.State != "done" || view.Result == nil || !view.Result.Nothing {
		t.Fatalf("%+v: %v", view, err)
	}
}

func TestPhotosReportsAreValidated(t *testing.T) {
	h, _, _ := photosHubFixture(t)
	view, _ := h.StartCheck(context.Background())
	claimNow(t, h)
	id := "delete:asset:12"
	for name, report := range map[string]PhotosMatchReport{
		"unknown entry": {Matches: []PhotosReportedMatch{{ID: "delete:asset:999", How: "exact", Photos: []PhotosReportedAsset{{ID: "A"}}}}},
		"unknown how":   {Matches: []PhotosReportedMatch{{ID: id, How: "guess", Photos: []PhotosReportedAsset{{ID: "A"}}}}},
		"no assets":     {Matches: []PhotosReportedMatch{{ID: id, How: "exact"}}},
		"empty id":      {Matches: []PhotosReportedMatch{{ID: id, How: "exact", Photos: []PhotosReportedAsset{{ID: ""}}}}},
		"unknown miss":  {Missing: []string{"favourite:asset:1"}},
		"reason for a match": {Matches: []PhotosReportedMatch{{ID: id, How: "exact", Photos: []PhotosReportedAsset{{ID: "A"}}}},
			Reasons: map[string]string{id: photosWhySharedAlbum}},
		"unknown reason": {Missing: []string{id}, Reasons: map[string]string{id: "deleted"}},
	} {
		if err := h.Matches(view.ID, report); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Anything that is not a JPEG is not kept as one.
	if err := h.Matches(view.ID, PhotosMatchReport{Matches: []PhotosReportedMatch{{ID: id, How: "near", Photos: []PhotosReportedAsset{{ID: "A", Thumb: []byte("<svg onload=alert(1)>")}}}}}); err != nil {
		t.Fatal(err)
	}
	// A retried report replaces the first rather than adding to it.
	if err := h.Matches(view.ID, PhotosMatchReport{Matches: []PhotosReportedMatch{{ID: id, How: "near", Photos: []PhotosReportedAsset{{ID: "A", Thumb: fakeJPEG}}}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Matches(view.ID, PhotosMatchReport{Matches: []PhotosReportedMatch{{ID: id, How: "near", Photos: []PhotosReportedAsset{{ID: "A", Thumb: fakeJPEG}}}}}); err != nil {
		t.Fatal(err)
	}
	h.Checked(view.ID)
	view, _ = h.Job(view.ID)
	row := rowByName(view.Delete, "IMG_6748.PNG")
	if row.How != "near" || len(row.Photos) != 1 || row.Photos[0].Thumb == "" || len(view.Delete) != 1 {
		t.Fatalf("row %+v, %d rows", row, len(view.Delete))
	}
	if h.job.thumbBytes != len(fakeJPEG) {
		t.Fatalf("thumbnail bytes %d after a retry", h.job.thumbBytes)
	}
}

// A file the helper did not find says what it saw instead, so a reviewer who
// knows the photograph is in Photos learns why it was not offered. A retried
// report replaces what the first one said.
func TestPhotosMissSaysWhy(t *testing.T) {
	h, _, _ := photosHubFixture(t)
	view, _ := h.StartCheck(context.Background())
	task := claimNow(t, h)
	ids := map[string]string{}
	for _, entry := range task.Entries {
		ids[entry.Name] = entry.ID
	}
	shared, other, found := ids["IMG_1001.HEIC"], ids["IMG_6748.PNG"], ids["IMG_3000.JPG"]
	if shared == "" || other == "" || found == "" {
		t.Fatalf("entries %v", ids)
	}
	if err := h.Matches(view.ID, PhotosMatchReport{Missing: []string{shared, other, found}, Reasons: map[string]string{
		shared: photosWhySharedAlbum, other: photosWhyOtherDay, found: photosWhyOtherDay,
	}}); err != nil {
		t.Fatal(err)
	}
	// The retry finds one of them and no longer sees the other day's file.
	if err := h.Matches(view.ID, PhotosMatchReport{
		Matches: []PhotosReportedMatch{{ID: found, How: "exact", Photos: []PhotosReportedAsset{{ID: "A", Thumb: fakeJPEG}}}},
		Missing: []string{shared, other},
		Reasons: map[string]string{shared: photosWhySharedAlbum},
	}); err != nil {
		t.Fatal(err)
	}
	h.Checked(view.ID)
	view, _ = h.Job(view.ID)
	got := map[string]string{}
	for _, miss := range view.Missing {
		got[miss.Name] = miss.Why
	}
	if got["IMG_1001.HEIC"] != photosWhySharedAlbum {
		t.Fatalf("the shared album is not named: %v", got)
	}
	if reason, ok := got["IMG_6748.PNG"]; !ok || reason != "" {
		t.Fatalf("a retried miss keeps its old reason: %q %v", reason, ok)
	}
	if _, ok := got["IMG_3000.JPG"]; ok {
		t.Fatalf("a matched file is still listed as missing: %v", got)
	}
	if len(h.job.why) != 1 {
		t.Fatalf("reasons left behind: %v", h.job.why)
	}
}

func TestPhotosClaimWaitsForWork(t *testing.T) {
	h, _, _ := photosHubFixture(t)
	got := make(chan *PhotosTask, 1)
	go func() {
		task, _ := h.Claim(context.Background())
		got <- task
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := h.StartCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case task := <-got:
		if task == nil || task.Task != "check" {
			t.Fatalf("task %+v", task)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiting helper was not woken by a new check")
	}
}

func TestPhotosAgentRoutesNeedTheKey(t *testing.T) {
	h, _, _ := photosHubFixture(t)
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	request := func(method, path, key, body string) int {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			req.Header.Set(PhotosAgentHeader, key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	for _, key := range []string{"", "wrong", fakePhotosKey + "x", fakePhotosKey[:len(fakePhotosKey)-1]} {
		if status := request("POST", "/api/photos/agent/heartbeat", key, `{"version":"1"}`); status != 403 {
			t.Errorf("key %q: %d", key, status)
		}
	}
	if status := request("POST", "/api/photos/agent/heartbeat", fakePhotosKey, `{"version":"1","access":"authorized"}`); status != 200 {
		t.Fatalf("right key: %d", status)
	}
	if status := request("POST", "/api/photos/agent/heartbeat", fakePhotosKey, `{"version":"1","extra":true}`); status != 400 {
		t.Errorf("unknown field: %d", status)
	}
	req, _ := http.NewRequest("POST", server.URL+"/api/photos/agent/heartbeat", strings.NewReader(`{}`))
	req.Header.Set(PhotosAgentHeader, fakePhotosKey)
	req.Header.Set("Content-Type", "text/plain")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 415 {
		t.Errorf("form post: %d", res.StatusCode)
	}

	// The key is never accepted in the URL.
	if status := request("GET", "/api/photos/agent/work?key="+fakePhotosKey, "", ""); status != 403 {
		t.Errorf("key in URL: %d", status)
	}

	// The page's writes are same-origin JSON like every other.
	cross, _ := http.NewRequest("POST", server.URL+"/api/photos/check", strings.NewReader(`{}`))
	cross.Header.Set("Content-Type", "application/json")
	cross.Header.Set("Origin", "https://evil.example")
	if res, _ := http.DefaultClient.Do(cross); res.StatusCode != 403 {
		t.Errorf("cross-site check: %d", res.StatusCode)
	}
	if status := request("POST", "/api/photos/check", "", `{}`); status != 200 {
		t.Errorf("same-origin check: %d", status)
	}

	// Through HTTP, the helper takes the check and reports.
	req, _ = http.NewRequest("GET", server.URL+"/api/photos/agent/work", nil)
	req.Header.Set(PhotosAgentHeader, fakePhotosKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("work: %v %v", res, err)
	}
	var task PhotosTask
	json.NewDecoder(res.Body).Decode(&task)
	res.Body.Close()
	report, _ := json.Marshal(PhotosMatchReport{Matches: []PhotosReportedMatch{{ID: task.Entries[0].ID, How: "exact", Photos: []PhotosReportedAsset{{ID: "X/L0/001", Thumb: fakeJPEG}}}}})
	if status := request("POST", "/api/photos/agent/jobs/"+task.JobID+"/matches", fakePhotosKey, string(report)); status != 200 {
		t.Fatalf("matches: %d", status)
	}
	if status := request("POST", "/api/photos/agent/jobs/"+task.JobID+"/checked", fakePhotosKey, `{}`); status != 200 {
		t.Fatalf("checked: %d", status)
	}
	res, err = http.Get(server.URL + "/api/photos/thumb/" + task.JobID + "/1")
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("thumb: %v %v", res, err)
	}
	res.Body.Close()
}

// Until a setup command hands out a key, and with a PHOTOS_AGENT_KEY too weak
// to use, the helper is refused and the page is told nothing can be checked.
func TestPhotosWithoutAKeyIsSwitchedOff(t *testing.T) {
	for _, key := range []string{"", strings.Repeat("k", PhotosAgentKeyMin-1), strings.Repeat("k", PhotosAgentKeyMin) + " spaced"} {
		h := NewPhotosHub(testStore(t), key)
		if h.Enabled() {
			t.Fatalf("enabled with a %d character key", len(key))
		}
		if _, err := h.StartCheck(context.Background()); !errors.Is(err, ErrPhotosDisabled) {
			t.Fatalf("check without a key: %v", err)
		}
		server := httptest.NewServer(h.Handler())
		req, _ := http.NewRequest("GET", server.URL+"/api/photos/agent/work", nil)
		req.Header.Set(PhotosAgentHeader, key)
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 403 {
			t.Fatalf("agent without a key: %v %v", res, err)
		}
		res.Body.Close()
		server.Close()
	}
}

func TestPhotosHubNeverPrintsItsKey(t *testing.T) {
	h := NewPhotosHub(testStore(t), fakePhotosKey)
	for _, printed := range []string{fmt.Sprint(h), fmt.Sprintf("%v %+v %#v %s", h, h, h, h)} {
		if strings.Contains(printed, fakePhotosKey) || strings.Contains(printed, fmt.Sprintf("%x", h.keyHash[:4])) {
			t.Fatalf("printed %q", printed)
		}
	}
}

// The page offers to update a helper older than the one this server was built
// with, and the two version numbers are kept in one place each: a test, not a
// reviewer, notices when Info.plist moves on without the server.
func TestPhotosAgentVersionMatchesTheHelper(t *testing.T) {
	plist, err := os.ReadFile("../../mac/CullSync/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	want := "<key>CFBundleShortVersionString</key>\n\t<string>" + PhotosAgentVersion + "</string>"
	if !strings.Contains(string(plist), want) {
		t.Errorf("Info.plist does not say version %s", PhotosAgentVersion)
	}
	for _, c := range []struct {
		a, b  string
		older bool
	}{
		{"1.0", "1.1", true}, {"1.1", "1.1", false}, {"1.2", "1.1", false}, {"1", "1.1", true},
		{"1.1.0", "1.1", false}, {"1.10", "1.9", false}, {"0.9", "1.0", true}, {"dev", "1.1", false}, {"", "1.1", false},
	} {
		if got := photosOlder(c.a, c.b); got != c.older {
			t.Errorf("photosOlder(%q, %q) = %v", c.a, c.b, got)
		}
	}

	hub, _, _ := photosHubFixture(t)
	beat := func(version string) PhotosAgentView {
		hub.Seen(PhotosHeartbeat{Version: version, Access: "authorized"})
		return hub.Status().Agent
	}
	if agent := beat("1.0"); !agent.Outdated || agent.Latest != PhotosAgentVersion {
		t.Errorf("a 1.0 helper is not offered an update: %+v", agent)
	}
	if beat(PhotosAgentVersion).Outdated {
		t.Error("the current helper is offered an update")
	}
}

// Photos is checked by itself while the helper is online: at once when there
// is no answer yet, then again once the last one is photosAutoCheckEvery old.
// A plan younger than that is left for the reviewer, and a job the helper is
// working on is never replaced.
func TestPhotosAutoCheck(t *testing.T) {
	h, _, clock := photosHubFixture(t)
	ctx := context.Background()
	beat := func() { h.Seen(PhotosHeartbeat{Version: PhotosAgentVersion, Access: "authorized"}) }
	auto := func(want bool, why string) {
		t.Helper()
		started, err := h.AutoCheck(ctx)
		if err != nil || started != want {
			t.Fatalf("%s: started %v (%v), want %v", why, started, err, want)
		}
	}

	auto(false, "no helper has called in")
	beat()
	auto(true, "a helper online and no answer yet")
	first := h.Status().Job
	if first == nil || first.State != "queued_check" {
		t.Fatalf("job %+v", first)
	}
	auto(false, "a check already waiting for the helper")
	claimNow(t, h)
	beat()
	auto(false, "a check under way")
	if h.Status().Job.ID != first.ID {
		t.Fatal("the check under way was replaced")
	}

	// A fresh plan is the reviewer's to look at.
	h.Cancel(first.ID)
	view := checkAndMatch(t, h)
	clock.advance(photosAutoCheckEvery - time.Minute)
	beat()
	auto(false, "a plan made minutes ago")
	clock.advance(time.Minute)
	beat()
	auto(true, "a plan as old as the interval")
	if job := h.Status().Job; job.ID == view.ID || job.State != "queued_check" {
		t.Fatalf("the old plan was not checked again: %+v", job)
	}

	// Changing Photos is never interrupted, however long it takes.
	h.Cancel(h.Status().Job.ID)
	view = checkAndMatch(t, h)
	if _, err := h.Apply(ctx, view.ID, []string{rowByName(view.Delete, "IMG_6748.PNG").ID}, nil); err != nil {
		t.Fatal(err)
	}
	claimNow(t, h)
	for range 4 {
		clock.advance(photosApplySilence - time.Second)
		h.Seen(PhotosHeartbeat{Version: PhotosAgentVersion, Access: "authorized", Job: view.ID, Stage: "confirm"})
		auto(false, "an apply waiting on the Mac")
	}
	if job := h.Status().Job; job.ID != view.ID || job.State != "applying" {
		t.Fatalf("the apply was disturbed: %+v", job)
	}

	// Once a job ends, the next check waits out the interval too.
	h.Applied(ctx, view.ID, PhotosAppliedReport{Deletes: []PhotosAppliedItem{{ID: rowByName(view.Delete, "IMG_6748.PNG").ID, Done: true}}})
	if job := h.Status().Job; job.State != "done" {
		t.Fatalf("not done: %+v", job)
	}
	auto(false, "a sync that has just ended")
	clock.advance(photosAutoCheckEvery)
	auto(false, "the helper has since gone quiet")
	beat()
	auto(true, "the helper is back and the answer is old")
}

// Photos finding a removed file's name only a day off has found the
// photograph of a file the archive still keeps on that day, when it keeps one:
// a copy filed under the next day was the one removed. Such a match is held,
// never offered for deletion, while a day-off match with nothing kept nearby
// still is.
func TestPhotosNearMatchOfAKeptFileIsHeld(t *testing.T) {
	s := testStore(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.write.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	asset := func(id int64, path string, cull bool) {
		exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,0,?,100,'archive')", id, path, mediaKind(path))
		if cull {
			exec("INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(?,'cull',0,1)", id)
		}
	}
	// The copy under 30 August removed, the one under 29 August kept.
	asset(1, "/archive/2020/2020-08/2020-08-29/IMG_0367.HEIC", false)
	asset(2, "/archive/2020/2020-08/2020-08-30/IMG_0367.HEIC", true)
	// Both plain copies removed, a re-download under the day before kept.
	asset(3, "/archive/2023/2023-12/2023-12-31/IMG_4167 (2023-12-31).MOV", false)
	asset(4, "/archive/2024/2024-01/2024-01-01/IMG_4167.MOV", true)
	// Nothing kept nearby: the day-off match still stands.
	asset(5, "/archive/2021/2021-03/2021-03-04/IMG_9000.HEIC", true)

	h := NewPhotosHub(s, fakePhotosKey)
	view, err := h.StartCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	task := claimNow(t, h)
	report := PhotosMatchReport{}
	for i, entry := range task.Entries {
		report.Matches = append(report.Matches, PhotosReportedMatch{ID: entry.ID, How: "near", Photos: []PhotosReportedAsset{{
			ID: fmt.Sprintf("UUID-%d/L0/001", i), Name: entry.Name, Created: "2020-01-01T10:00:00Z", Thumb: fakeJPEG,
		}}})
	}
	if err = h.Matches(view.ID, report); err != nil {
		t.Fatal(err)
	}
	if err = h.Checked(view.ID); err != nil {
		t.Fatal(err)
	}
	view, _ = h.Job(view.ID)
	if len(view.Delete) != 1 || view.Delete[0].Name != "IMG_9000.HEIC" || view.Delete[0].How != "near" {
		t.Fatalf("offered %+v", view.Delete)
	}
	if len(view.Missing) != 0 {
		t.Fatalf("missing %+v", view.Missing)
	}
	held := map[string]PhotosHeld{}
	for _, item := range view.Held {
		held[item.Name] = item
	}
	if got := held["IMG_0367.HEIC"]; got.Kept != "IMG_0367.HEIC" || got.KeptDay != "2020-08-29" || got.Day != "2020-08-30" {
		t.Fatalf("IMG_0367 held as %+v", got)
	}
	if got := held["IMG_4167.MOV"]; got.Kept != "IMG_4167 (2023-12-31).MOV" || got.KeptDay != "2023-12-31" {
		t.Fatalf("IMG_4167 held as %+v", got)
	}
	// Choosing a held one is refused.
	if _, err = h.Apply(context.Background(), view.ID, []string{"delete:asset:2"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("applying a held match: %v", err)
	}
}
