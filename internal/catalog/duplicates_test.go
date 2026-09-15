package catalog

import (
	"context"
	"testing"
)

func TestExactDuplicatesUseFullHashEvidenceAcrossDates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	assets := []struct {
		id   int
		path string
		day  string
		size int
		hash string
	}{
		{1, "/archive/2000/2000-01/2000-01-02/A.JPG", "2000-01-02", 100, "same"},
		{2, "/archive/2010/2010-01/2010-01-02/A (2).JPG", "2010-01-02", 100, "same"},
		{3, "/archive/2015/2015-04/2015-04-03/C.JPG", "2015-04-03", 100, "same"},
		{4, "/archive/2010/2010-01/2010-01-02/A edited.JPG", "2010-01-02", 100, "different"},
		{5, "/archive/2010/2010-01/2010-01-02/EMPTY.JPG", "2010-01-02", 0, "empty"},
		{6, "/archive/2020/2020-01/2020-01-02/EMPTY.JPG", "2020-01-02", 0, "empty"},
	}
	for _, item := range assets {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',?,'archive')", item.id, item.path, item.size); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,?)", item.id, item.day); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_evidence(asset_id,full_hash) VALUES(?,?)", item.id, item.hash); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := s.ExactDuplicates(ctx, "01-02", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Hash != "same" || len(groups[0].Members) != 3 || groups[0].Members[2].Day != "2015-04-03" || groups[0].Reclaimable != 200 {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(1,'keep',0,1),(2,'cull',0,1),(3,'cull',0,1)"); err != nil {
		t.Fatal(err)
	}
	groups, err = s.ExactDuplicates(ctx, "01-02", 100)
	if err != nil || len(groups) != 0 {
		t.Fatalf("resolved group remained in queue: %+v %v", groups, err)
	}
}

func TestExactDuplicatesRejectInvalidDate(t *testing.T) {
	s := testStore(t)
	if _, err := s.ExactDuplicates(context.Background(), "02-30", 100); err == nil {
		t.Fatal("accepted impossible month-day")
	}
}
