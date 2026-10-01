package catalog

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrashWindowKeepsFullBatchSizeAndCursorAfterRemoval(t *testing.T) {
	items := []TrashItem{}
	for i := 0; i < 205; i++ {
		items = append(items, TrashItem{Key: fmt.Sprintf("bin:%03d", i), Group: "one-batch", Size: 10, RemovedAt: "2026-10-01T12:00:00Z"})
	}
	first, err := trashWindow(items, 100, "")
	if err != nil || len(first.Items) != 100 || first.Total != 205 || first.Groups["one-batch"].Files != 205 {
		t.Fatalf("bad first page: %+v %v", first, err)
	}
	second, err := trashWindow(items[100:], 100, first.Next)
	if err != nil || len(second.Items) != 100 || second.Items[0].Key != "bin:100" {
		t.Fatalf("removed earlier cards shifted page: %+v %v", second, err)
	}
	if _, err = trashWindow(items, 101, ""); err == nil {
		t.Fatal("unbounded page accepted")
	}
}

func TestSearchFiltersAndBoundCursor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC).Unix()
	for i := 1; i <= 4; i++ {
		_, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,?,'image',100)", i, fmt.Sprintf("/archive/2020-01-02/A7_%d.jpg", i), at+int64(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	s.write.Exec("INSERT INTO exposures VALUES(1,100,'2020:01:02 12:00:01','','Sony A7 IV')")
	q := SearchQuery{Text: "A7_", Limit: 2}
	p, err := s.Search(ctx, q)
	if err != nil || len(p.Assets) != 2 || p.Next == "" {
		t.Fatal(p, err)
	}
	q.After = p.Next
	s.write.Exec("INSERT INTO file_state VALUES(1,'bin','fixture')")
	p, err = s.Search(ctx, q)
	if err != nil || len(p.Assets) != 2 || p.Assets[0].ID != 3 {
		t.Fatal(p, err)
	}
	q.Text = "different"
	if _, err = s.Search(ctx, q); err == nil {
		t.Fatal("cursor accepted for another query")
	}
	s.write.Exec("DELETE FROM file_state WHERE asset_id=1")
	p, err = s.Search(ctx, SearchQuery{Text: "Sony", Limit: 50})
	if err != nil || len(p.Assets) != 1 || p.Assets[0].ID != 1 {
		t.Fatal(p, err)
	}
	for _, url := range []string{"/api/search?limit=101", "/api/search?from=bad", "/api/search?from=2021-01-01&to=2020-01-01"} {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, httptest.NewRequest("GET", url, nil))
		if r.Code != 400 {
			t.Fatal(url, r.Code)
		}
	}
}

func TestBurstUsesTimeAndCameraButNotFolderDate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 4; i++ {
		s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,?,'image',100)", i, fmt.Sprintf("/archive/2020-01-02/DSC_%d.jpg", i), int64(1577923200))
	}
	for _, row := range []struct {
		id           int
		stamp, model string
	}{{1, "2020:01:02 12:00:00", "Sony"}, {2, "2020:01:02 12:00:03", "Sony"}, {3, "2020:01:02 12:00:02", "Nikon"}} {
		s.write.Exec("INSERT INTO exposures VALUES(?,100,?,'',?)", row.id, row.stamp, row.model)
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	found, err := s.Burst(ctx, 1)
	if err != nil || len(found) != 2 || found[1].ID != 2 || found[1].ComparisonReason != "Taken within 8 seconds" {
		t.Fatal(found, err)
	}
	s.write.Exec("INSERT INTO file_state VALUES(2,'bin','fixture')")
	found, err = s.Burst(ctx, 1)
	if err != nil || len(found) != 1 {
		t.Fatal(found, err)
	}
}

func TestDuplicateWindowCountsPastLegacyLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tx, err := s.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2002; i++ {
		for _, query := range []string{fmt.Sprintf("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(%d,'/archive/2020-01-02/%d.jpg',1,'image',100)", i, i), fmt.Sprintf("INSERT INTO asset_days VALUES(%d,'2020-01-02')", i), fmt.Sprintf("INSERT INTO asset_evidence(asset_id,full_hash) VALUES(%d,'hash%04d')", i, (i-1)/2)} {
			if _, err = tx.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p, err := s.DuplicateWindow(ctx, 50, "")
	if err != nil || len(p.Groups) != 50 || p.Total != 1001 || p.Next == "" {
		t.Fatal(len(p.Groups), p.Total, p.Next, err)
	}
	first := p.Groups[0].Hash
	next, err := s.DuplicateWindow(ctx, 50, p.Next)
	if err != nil || len(next.Groups) != 50 || next.Groups[0].Hash == first {
		t.Fatal(next, err)
	}
}

func TestBurstKeepsRawCompanionAcrossWindow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 45; i++ {
		name, kind := fmt.Sprintf("SHOT_%03d.JPG", i), "image"
		if i == 45 {
			name = "SHOT_001.ARW"
			kind = "raw"
		}
		if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1,?,100)", i, "/archive/2020-01-02/"+name, kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := s.Burst(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]Asset{}
	for _, file := range files {
		byID[file.ID] = file
	}
	if len(byID[1].Stack) != 1 || byID[1].Stack[0] != 45 || len(byID[45].Stack) != 1 {
		t.Fatal("RAW companion lost at page boundary", byID[1], byID[45])
	}
	s.write.Exec("INSERT INTO file_state VALUES(1,'bin','fixture')")
	if _, err = s.Burst(ctx, 1); err == nil {
		t.Fatal("comparison allowed for a file in the Bin")
	}
}
