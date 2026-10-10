package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

func scoresFixture(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	for _, path := range []string{
		// Photos has it on the same day, picked for Memories: a keep hint.
		"/archive/2023/2023-08/2023-08-25/IMG_1001.HEIC",
		// A video Photos marks as a failed shot: a cull hint.
		"/archive/2023/2023-08/2023-08-25/IMG_1002.MOV",
		// Photos has it only the day after, and nothing else bears the name.
		"/archive/2023/2023-08/2023-08-25/IMG_1003.HEIC",
		// Photos has it the day after, where the archive also holds the name.
		"/archive/2023/2023-08/2023-08-25/IMG_1004.HEIC",
		"/archive/2023/2023-08/2023-08-26/IMG_1004.HEIC",
		// Photos has it a day either side: two bearers, so no match.
		"/archive/2023/2023-08/2023-08-25/IMG_1006.HEIC",
		// Photos has it but never scored it.
		"/archive/2023/2023-08/2023-08-25/IMG_1007.HEIC",
		// Photos has never seen it.
		"/archive/2023/2023-08/2023-08-25/IMG_1008.HEIC",
	} {
		day := path[strings.LastIndex(path, "/")-10 : strings.LastIndex(path, "/")]
		when, _ := time.Parse("2006-01-02", day)
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,10,'archive')", path, when.Add(10*time.Hour).Unix(), mediaKind(path)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.ExecContext(ctx, "UPDATE stats SET total=8"); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func scoresReport(run string, done bool, items ...PhotosScore) PhotosScoresReport {
	return PhotosScoresReport{Version: PhotosScoresVersion, Run: run, Items: items, Done: done}
}

func scoredImage(id, name, day string) PhotosScore {
	return PhotosScore{PhotosID: id, Name: name, Day: day, Kind: "image", Overall: f64(0.5), Labels: []string{"people"}}
}

func TestPhotosScoresMatchTheDayPage(t *testing.T) {
	s := scoresFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	picked := scoredImage("u1", "IMG_1001.HEIC", "2023-08-25")
	picked.Curation, picked.Adjusted, picked.Faces, picked.Smiles, picked.Caption = f64(0.9), true, 2, 1, "two people on a beach"
	failed := PhotosScore{PhotosID: "u2", Name: "IMG_1002.MOV", Day: "2023-08-25", Kind: "video", Overall: f64(0.3), Failure: f64(-0.2), LowLight: f64(0.7), Labels: []string{}}
	unscored := PhotosScore{PhotosID: "u7", Name: "IMG_1007.HEIC", Day: "2023-08-25", Kind: "image", Favourite: true, Labels: []string{}}
	stored, err := s.RecordPhotosScores(ctx, scoresReport("r1", true, picked, failed,
		scoredImage("u3", "IMG_1003.HEIC", "2023-08-26"),
		scoredImage("u4", "IMG_1004.HEIC", "2023-08-26"),
		scoredImage("u6a", "IMG_1006.HEIC", "2023-08-24"), scoredImage("u6b", "IMG_1006.HEIC", "2023-08-26"),
		unscored), now)
	if err != nil || stored.Stored != 7 || stored.Total != 7 {
		t.Fatalf("stored %+v: %v", stored, err)
	}
	state, err := s.PhotosScoresState(ctx)
	if err != nil || state.Items != 7 || state.SyncedAt != "2026-10-10T12:00:00Z" {
		t.Fatalf("state %+v: %v", state, err)
	}

	hints := func(md string) map[string]*AssetHint {
		t.Helper()
		data, err := s.Today(ctx, md)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]*AssetHint{}
		for _, year := range data.Years {
			for _, a := range year.Assets {
				out[a.Path] = a.Hint
			}
		}
		return out
	}
	got := hints("08-25")
	if len(got) != 7 {
		t.Fatalf("files on the day: %d", len(got))
	}
	h := got["/archive/2023/2023-08/2023-08-25/IMG_1001.HEIC"]
	if h == nil || h.Source != "apple-photos" || h.How != "exact" || !h.Keep || h.Cull || h.Overall != 0.5 || h.Faces != 2 || h.Caption != "two people on a beach" || len(h.Labels) != 1 {
		t.Fatalf("picked: %+v", h)
	}
	if strings.Join(h.Reasons, "; ") != "edited in Photos; Photos would pick it for Memories" {
		t.Fatalf("picked reasons: %q", h.Reasons)
	}
	h = got["/archive/2023/2023-08/2023-08-25/IMG_1002.MOV"]
	if h == nil || h.How != "exact" || h.Keep || !h.Cull || strings.Join(h.Reasons, "; ") != "Photos marks it a failed shot; dark" {
		t.Fatalf("failed: %+v", h)
	}
	if h = got["/archive/2023/2023-08/2023-08-25/IMG_1003.HEIC"]; h == nil || h.How != "near" {
		t.Fatalf("near: %+v", h)
	}
	if h = got["/archive/2023/2023-08/2023-08-25/IMG_1004.HEIC"]; h != nil {
		t.Fatalf("held by the day after, yet matched: %+v", h)
	}
	if h = got["/archive/2023/2023-08/2023-08-25/IMG_1006.HEIC"]; h != nil {
		t.Fatalf("two bearers, yet matched: %+v", h)
	}
	h = got["/archive/2023/2023-08/2023-08-25/IMG_1007.HEIC"]
	if h == nil || h.Keep || h.Cull || h.Overall != 0 || strings.Join(h.Reasons, "; ") != "favourite in Photos" {
		t.Fatalf("unscored: %+v", h)
	}
	if h = got["/archive/2023/2023-08/2023-08-25/IMG_1008.HEIC"]; h != nil {
		t.Fatalf("unknown to Photos, yet matched: %+v", h)
	}
	if h = hints("08-26")["/archive/2023/2023-08/2023-08-26/IMG_1004.HEIC"]; h == nil || h.How != "exact" {
		t.Fatalf("the day after's own file: %+v", h)
	}

	// A later run that no longer carries an item drops it once the run is
	// complete, and not before.
	if _, err = s.RecordPhotosScores(ctx, scoresReport("r2", false, picked), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got = hints("08-25"); got["/archive/2023/2023-08/2023-08-25/IMG_1002.MOV"] == nil {
		t.Fatal("an unfinished run dropped earlier items")
	}
	stored, err = s.RecordPhotosScores(ctx, scoresReport("r2", true), now.Add(2*time.Hour))
	if err != nil || stored.Stored != 0 || stored.Total != 1 {
		t.Fatalf("after the run: %+v %v", stored, err)
	}
	got = hints("08-25")
	if got["/archive/2023/2023-08/2023-08-25/IMG_1001.HEIC"] == nil || got["/archive/2023/2023-08/2023-08-25/IMG_1002.MOV"] != nil {
		t.Fatalf("after the run: %+v", got)
	}
}

func TestPhotosScoresRefuseBadPages(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()
	good := scoredImage("u1", "IMG_1.HEIC", "2023-08-25")
	bad := map[string]PhotosScoresReport{
		"version": {Version: "apple-photos-v0", Run: "r", Items: []PhotosScore{good}},
		"no run":  scoresReport("", false, good),
	}
	for name, change := range map[string]func(*PhotosScore){
		"no id":          func(p *PhotosScore) { p.PhotosID = "" },
		"no name":        func(p *PhotosScore) { p.Name = "" },
		"bad day":        func(p *PhotosScore) { p.Day = "25/08/2023" },
		"bad kind":       func(p *PhotosScore) { p.Kind = "audio" },
		"score above 1":  func(p *PhotosScore) { p.Overall = f64(1.5) },
		"score below -1": func(p *PhotosScore) { p.Failure = f64(-2) },
		"too many labels": func(p *PhotosScore) {
			p.Labels = make([]string, 21)
			for i := range p.Labels {
				p.Labels[i] = "x"
			}
		},
		"empty label":       func(p *PhotosScore) { p.Labels = []string{""} },
		"long caption":      func(p *PhotosScore) { p.Caption = strings.Repeat("a", 201) },
		"smiles over faces": func(p *PhotosScore) { p.Faces, p.Smiles = 1, 2 },
		"negative size":     func(p *PhotosScore) { p.Size = -1 },
	} {
		item := good
		change(&item)
		bad[name] = scoresReport("r", false, good, item)
	}
	bad["too many items"] = scoresReport("r", false, make([]PhotosScore, photosScoresPageMax+1)...)
	for name, report := range bad {
		if _, err := s.RecordPhotosScores(ctx, report, now); err != ErrInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	state, _ := s.PhotosScoresState(ctx)
	if state.Items != 0 || state.SyncedAt != "" {
		t.Fatalf("a refused page wrote: %+v", state)
	}
	// A day Photos has no date for is allowed; it just never matches.
	undated := good
	undated.Day = ""
	if _, err := s.RecordPhotosScores(ctx, scoresReport("r", false, undated), now); err != nil {
		t.Fatal(err)
	}
}

func TestPhotosScoresRouteNeedsTheKey(t *testing.T) {
	h, s, _ := photosHubFixture(t)
	server := httptest.NewServer(h.Handler())
	t.Cleanup(server.Close)
	post := func(key, body string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", server.URL+"/api/photos/agent/scores", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set(PhotosAgentHeader, key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	page := `{"version":"apple-photos-v1","run":"r1","items":[{"photosId":"u1","name":"IMG_1001.HEIC","day":"2023-08-25","kind":"image","overall":0.5,"labels":[]}],"done":true}`
	if status := post("", page); status != 403 {
		t.Fatalf("no key: %d", status)
	}
	if status := post("wrong", page); status != 403 {
		t.Fatalf("wrong key: %d", status)
	}
	if status := post(fakePhotosKey, `{"version":"nope","run":"r1","items":[],"done":true}`); status != 400 {
		t.Fatalf("wrong version: %d", status)
	}
	if status := post(fakePhotosKey, page); status != 200 {
		t.Fatalf("right key: %d", status)
	}
	state, err := s.PhotosScoresState(context.Background())
	if err != nil || state.Items != 1 {
		t.Fatalf("state %+v: %v", state, err)
	}
	res, err := http.Get(server.URL + "/api/photos/scores")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("state route: %d", res.StatusCode)
	}
}
