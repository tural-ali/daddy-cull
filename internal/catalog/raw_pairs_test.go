package catalog

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
)

func TestRawPairsJoinOnlyOneExposure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const day = "/archive/2020/2020-01/2020-01-02/"
	files := []struct {
		path, kind string
	}{
		{day + "A7404251.ARW", "raw"},       // 1 pairs with 2
		{day + "a7404251.jpg", "image"},     // 2
		{day + "DSC_1.NEF", "raw"},          // 3 two JPEGs: no pair
		{day + "DSC_1.JPG", "image"},        // 4
		{day + "DSC_1.JPEG", "image"},       // 5
		{day + "IMG_2.DNG", "raw"},          // 6 a PNG is not a camera JPEG
		{day + "IMG_2.PNG", "image"},        // 7
		{day + "IMG_3.CR2", "raw"},          // 8 an edited copy stays apart
		{day + "IMG_3-edited.JPG", "image"}, // 9
		{"/archive/2020/other/IMG_4.CR2", "raw"},
		{day + "IMG_4.HEIC", "image"}, // 11 a different folder: no pair
	}
	for i, f := range files {
		if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1577966400,?,10,'archive')", i+1, f.path, f.kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	var pairs [][2]int64
	rows, err := s.read.Query("SELECT raw_id,partner_id FROM raw_pairs ORDER BY raw_id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p [2]int64
		rows.Scan(&p[0], &p[1])
		pairs = append(pairs, p)
	}
	rows.Close()
	if len(pairs) != 1 || pairs[0] != [2]int64{1, 2} {
		t.Fatal("pairs", pairs)
	}

	if err = s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	pair := func() map[int64]int64 {
		t.Helper()
		data, err := s.Today(ctx, "01-02")
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]int64{}
		for _, y := range data.Years {
			for _, a := range y.Assets {
				if a.Pair != 0 {
					out[a.ID] = a.Pair
				}
			}
		}
		return out
	}
	if got := pair(); len(got) != 2 || got[1] != 2 || got[2] != 1 {
		t.Fatal("day page pair", got)
	}

	// A split survives a rescan, and joining again undoes it.
	if err = s.SetPaired(ctx, 2, 1, false); err != nil {
		t.Fatal(err)
	}
	if err = s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	if got := pair(); len(got) != 0 {
		t.Fatal("split pair still shown together", got)
	}
	if err = s.SetPaired(ctx, 1, 2, true); err != nil {
		t.Fatal(err)
	}
	if got := pair(); len(got) != 2 {
		t.Fatal("joined pair", got)
	}
	if err = s.SetPaired(ctx, 3, 4, false); err != ErrInvalid {
		t.Fatal("split files the index never paired", err)
	}

	// A half in the Bin leaves the other showing alone.
	s.write.Exec("INSERT INTO file_state VALUES(1,'bin','test')")
	if got := pair(); len(got) != 0 {
		t.Fatal("pair with a binned half", got)
	}
}

func TestRawPairsHTTP(t *testing.T) {
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
