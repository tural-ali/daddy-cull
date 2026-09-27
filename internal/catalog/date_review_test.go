package catalog

import (
	"context"
	"testing"
)

func TestMarkDateReviewedKeepsTheUndecidedAndCountsWhatTheBinTook(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// 27 September in two years, and one file on another date that must not
	// be touched. Sizes are distinct powers so the removed bytes identify
	// exactly which files were counted.
	for _, row := range []struct {
		id   int64
		path string
		size int64
	}{
		{1, "/archive/2020/2020-09/2020-09-27/UNDECIDED.JPG", 1},
		{2, "/archive/2020/2020-09/2020-09-27/FAVOURITE.JPG", 2},
		{3, "/archive/2020/2020-09/2020-09-27/MARKED.JPG", 4},
		{4, "/archive/2020/2020-09/2020-09-27/IN_BIN.JPG", 8},
		{5, "/archive/2025/2025-09/2025-09-27/PURGED.MOV", 16},
		{6, "/archive/2025/2025-09/2025-09-27/ALSO_UNDECIDED.JPG", 32},
		{7, "/archive/2025/2025-09/2025-09-28/NEXT_DAY.JPG", 64},
	} {
		if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,'image',?,'archive')`, row.id, row.path, row.id, row.size); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO decisions VALUES(2,'keep',1,3),(3,'cull',0,1),(4,'cull',0,2),(5,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO file_state VALUES(4,'bin','p1'),(5,'purged','p2')`); err != nil {
		t.Fatal(err)
	}

	got, err := s.MarkDateReviewed(ctx, "09-27", "request-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Kept) != 2 || got.Kept[0] != (KeptAsset{ID: 1, Revision: 1}) || got.Kept[1] != (KeptAsset{ID: 6, Revision: 1}) {
		t.Fatalf("kept %+v, wanted the two undecided files at revision 1", got.Kept)
	}
	if len(got.Days) != 2 || got.Days[0] != "2020-09-27" || got.Days[1] != "2025-09-27" {
		t.Fatalf("days %v", got.Days)
	}
	want := DateTally{Total: 6, Removed: 3, Bytes: 4 + 8 + 16, Kept: 3, Favourites: 1}
	if got.Tally != want {
		t.Fatalf("tally %+v, wanted %+v", got.Tally, want)
	}
	var status string
	var favourite bool
	if err = s.read.QueryRow(`SELECT status,favourite FROM decisions WHERE asset_id=2`).Scan(&status, &favourite); err != nil || status != "keep" || !favourite {
		t.Fatalf("the favourite was changed: %s %v %v", status, favourite, err)
	}
	var untouched int
	if err = s.read.QueryRow(`SELECT count(*) FROM decisions WHERE asset_id=7`).Scan(&untouched); err != nil || untouched != 0 {
		t.Fatalf("another date's file was decided: %d %v", untouched, err)
	}
	var journaled int
	if err = s.read.QueryRow(`SELECT count(*) FROM decision_events WHERE status='keep' AND asset_id IN (1,6)`).Scan(&journaled); err != nil || journaled != 2 {
		t.Fatalf("keeps journaled %d %v", journaled, err)
	}
	for _, day := range []string{"2020-09-27", "2025-09-27"} {
		if err = s.read.QueryRow(`SELECT status FROM day_progress WHERE day=?`, day).Scan(&status); err != nil || status != "done" {
			t.Fatalf("%s is %q %v", day, status, err)
		}
	}

	// A retry of the same request changes nothing and reports the same tally.
	again, err := s.MarkDateReviewed(ctx, "09-27", "request-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Kept) != 0 || len(again.Days) != 0 || again.Tally != want {
		t.Fatalf("retry %+v", again)
	}
	if _, err = s.MarkDateReviewed(ctx, "13-40", "request-two"); err != ErrInvalid {
		t.Fatalf("an impossible date: %v", err)
	}
}
