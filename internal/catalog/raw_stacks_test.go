package catalog

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRawStacksJoinOnlyOneExposure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const day = "/archive/2020/2020-01/2020-01-02/"
	files := []struct {
		path, kind string
	}{
		{day + "A7404251.ARW", "raw"},       // 1 stacks with 2
		{day + "a7404251.jpg", "image"},     // 2
		{day + "DSC_1.NEF", "raw"},          // 3 a RAW with three exports
		{day + "DSC_1.JPG", "image"},        // 4
		{day + "DSC_1.HEIC", "image"},       // 5
		{day + "DSC_1.tif", "image"},        // 6
		{day + "IMG_2.DNG", "raw"},          // 7 a PNG is not an export
		{day + "IMG_2.PNG", "image"},        // 8
		{day + "IMG_3.CR2", "raw"},          // 9 an edited copy stays apart
		{day + "IMG_3-edited.JPG", "image"}, // 10
		{"/archive/2020/other/IMG_4.CR2", "raw"},
		{day + "IMG_4.HEIC", "image"}, // 12 a different folder: no stack
		{day + "IMG_5.CR2", "raw"},    // 13 two RAWs of one name: no stack
		{day + "IMG_5.DNG", "raw"},    // 14
		{day + "IMG_5.JPG", "image"},  // 15
	}
	for i, f := range files {
		if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1577966400,?,10,'archive')", i+1, f.path, f.kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	var links [][2]int64
	rows, err := s.read.Query("SELECT raw_id,export_id FROM raw_stacks ORDER BY raw_id,export_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p [2]int64
		rows.Scan(&p[0], &p[1])
		links = append(links, p)
	}
	rows.Close()
	if fmt.Sprint(links) != "[[1 2] [3 4] [3 5] [3 6]]" {
		t.Fatal("stacks", links)
	}

	if err = s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	stacks := func() map[int64]string {
		t.Helper()
		data, err := s.Today(ctx, "01-02")
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]string{}
		for _, y := range data.Years {
			for _, a := range y.Assets {
				if len(a.Stack) > 0 {
					out[a.ID] = fmt.Sprint(a.Stack)
				}
			}
		}
		return out
	}
	want := map[int64]string{1: "[2]", 2: "[1]", 3: "[4 5 6]", 4: "[3 5 6]", 5: "[3 4 6]", 6: "[3 4 5]"}
	if got := stacks(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatal("day page stacks", got)
	}

	// A split survives a rescan and takes only that export out; joining again
	// undoes it.
	if err = s.SetPaired(ctx, 5, 3, false); err != nil {
		t.Fatal(err)
	}
	if err = s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); got[3] != "[4 6]" || got[5] != "" || got[4] != "[3 6]" {
		t.Fatal("split export still stacked", got)
	}
	if err = s.SetPaired(ctx, 2, 1, false); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); got[1] != "" || got[2] != "" {
		t.Fatal("a RAW with its only export split off still stacked", got)
	}
	if err = s.SetPaired(ctx, 1, 2, true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPaired(ctx, 3, 5, true); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatal("joined stacks", got)
	}
	if err = s.SetPaired(ctx, 7, 8, false); err != ErrInvalid {
		t.Fatal("split files the index never stacked", err)
	}

	// Settings can show every file on its own, and together again.
	if err = s.SetRawTogether(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := stacks(); len(got) != 0 {
		t.Fatal("stacks shown with the setting off", got)
	}
	if err = s.SetRawTogether(ctx, true); err != nil {
		t.Fatal(err)
	}

	// A file in the Bin leaves the rest of its stack together, and a RAW with
	// no export left shows alone.
	s.write.Exec("INSERT INTO file_state VALUES(4,'bin','test')")
	s.write.Exec("INSERT INTO file_state VALUES(2,'bin','test')")
	if got := stacks(); got[3] != "[5 6]" || got[1] != "" {
		t.Fatal("stacks with binned files", got)
	}
}

func TestRawTogetherSetting(t *testing.T) {
	s := testStore(t)
	on := func() bool {
		t.Helper()
		st, err := s.Stats(context.Background(), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return st.RawTogether
	}
	if !on() {
		t.Fatal("a new catalogue shows RAWs apart")
	}
	for _, tc := range []struct {
		body     string
		want     int
		together bool
	}{
		{`{"together":false}`, 200, false},
		{`{}`, 400, false},
		{`{"together":"yes"}`, 400, false},
		{`{"together":true}`, 200, true},
	} {
		req := httptest.NewRequest("POST", "/api/settings/raw", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.want || on() != tc.together {
			t.Fatalf("%s: got %d %s, together %v", tc.body, w.Code, w.Body.String(), on())
		}
	}
}

func TestRawStacksHTTP(t *testing.T) {
	s := testStore(t)
	for i, p := range []string{"/archive/d/A.ARW", "/archive/d/A.JPG"} {
		kind := "image"
		if i == 0 {
			kind = "raw"
		}
		if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1,?,10)", i+1, p, kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexRelated(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"rawId":1,"partnerId":2,"paired":false}`, 200},
		{`{"rawId":1,"partnerId":2,"paired":true}`, 200},
		{`{"rawId":1,"partnerId":3,"paired":false}`, 400},
		{`{"rawId":1,"partnerId":2}`, 400},
		{`{"rawId":1,"partnerId":2,"paired":false,"extra":1}`, 400},
	} {
		req := httptest.NewRequest("POST", "/api/pairs", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d %s", tc.body, w.Code, w.Body.String())
		}
	}
}
