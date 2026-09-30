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
	if len(groups) != 1 || groups[0].Hash != "same" || groups[0].Proof != "bytes" || len(groups[0].Members) != 3 || groups[0].Members[2].Day != "2015-04-03" || groups[0].Reclaimable != 200 {
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

// A group where every copy but one is in the Bin has nothing left to choose,
// whether the one left was kept or not.
func TestExactDuplicatesSettleWhenOneCopyIsLeft(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for id, path := range map[int]string{1: "/archive/2000/2000-01/2000-01-02/A.JPG", 2: "/archive/2000/2000-01/2000-01-02/A (2).JPG"} {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',100,'archive')", id, path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,'2000-01-02'); INSERT INTO asset_evidence(asset_id,full_hash) VALUES(?,'same')", id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(2,'cull',0,1)"); err != nil {
		t.Fatal(err)
	}
	if groups, err := s.ExactDuplicates(ctx, "", 100); err != nil || len(groups) != 0 {
		t.Fatalf("a group with one copy left remained: %+v %v", groups, err)
	}
}

// Saved from social offers the copies of the videos still waiting there, with
// every copy wherever it is filed, and nothing once the video is decided.
// Every name is a synthetic fixture.
func TestSocialDuplicatesHoldAWaitingVideo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, item := range []struct {
		id         int
		path, hash string
	}{
		{1, "/archive/2021/2021-05/2021-05-14/clip.mp4", "social"},
		{2, "/archive/2022/2022-01/2022-01-02/clip (2).mp4", "social"},
		{3, "/archive/2021/2021-05/2021-05-14/IMG_0001.MOV", "camera"},
		{4, "/archive/2021/2021-05/2021-05-14/IMG_0001 (2).MOV", "camera"},
	} {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'video',100,'archive')", item.id, item.path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,?); INSERT INTO asset_evidence(asset_id,full_hash) VALUES(?,?)", item.id, item.path[22:32], item.id, item.hash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO social_items(asset_id,path,day,name,size_bytes,score,evidence,width,height,duration,state) VALUES(1,'/archive/2021/2021-05/2021-05-14/clip.mp4','2021-05-14','clip.mp4',100,12,'uuid filename',480,848,15,'waiting')"); err != nil {
		t.Fatal(err)
	}
	groups, err := s.SocialDuplicates(ctx, 100)
	if err != nil || len(groups) != 1 || groups[0].Hash != "social" || len(groups[0].Members) != 2 || groups[0].Members[1].Day != "2022-01-02" {
		t.Fatalf("social copies: %+v %v", groups, err)
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(1,'keep',0,1)"); err != nil {
		t.Fatal(err)
	}
	if groups, err = s.SocialDuplicates(ctx, 100); err != nil || len(groups) != 0 {
		t.Fatalf("a decided video still offered its copies: %+v %v", groups, err)
	}
}

func TestExactDuplicatesRejectInvalidDate(t *testing.T) {
	s := testStore(t)
	if _, err := s.ExactDuplicates(context.Background(), "02-30", 100); err == nil {
		t.Fatal("accepted impossible month-day")
	}
}

// The page's credibility rests on this distinction: a catalogue where every
// file that shares a size has been hashed can state that no duplicates exist,
// however little else has been hashed, while one unhashed candidate means the
// question is still open and must be shown as open.
func TestDuplicateOverviewSeparatesSettledFromUnproven(t *testing.T) {
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
		{3, "/archive/2001/2001-03/2001-03-04/B.MOV", "2001-03-04", 900, ""},
		{4, "/archive/2002/2002-03/2002-03-04/B copy.MOV", "2002-03-04", 900, ""},
		{5, "/archive/2003/2003-05/2003-05-06/C.JPG", "2003-05-06", 400, "solo"},
	}
	for _, item := range assets {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',?,'archive')", item.id, item.path, item.size); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,?)", item.id, item.day); err != nil {
			t.Fatal(err)
		}
		if item.hash != "" {
			if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_evidence(asset_id,full_hash) VALUES(?,?)", item.id, item.hash); err != nil {
				t.Fatal(err)
			}
		}
	}

	report, err := s.DuplicateOverview(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	// Asset 5 shares its size with nothing, so it is not a candidate at all and
	// its hash must not flatter the coverage figures.
	if report.Candidates != 4 || report.Hashed != 2 || report.Settled {
		t.Fatalf("coverage counted the wrong population: %+v", report)
	}
	if len(report.Groups) != 1 || report.Groups[0].Hash != "same" {
		t.Fatalf("proven group missing: %+v", report.Groups)
	}
	if len(report.Unproven) != 1 || report.Unproven[0].Size != 900 || len(report.Unproven[0].Members) != 2 || report.Unproven[0].Hashed != 0 || report.Unproven[0].Reclaimable != 900 {
		t.Fatalf("unsettled size group not reported: %+v", report.Unproven)
	}

	// Hashing the open pair to two different values settles it: the files are
	// proven distinct, so the group leaves the list and the report is complete.
	if _, err = s.write.ExecContext(ctx, "INSERT INTO asset_evidence(asset_id,full_hash) VALUES(3,'left'),(4,'right')"); err != nil {
		t.Fatal(err)
	}
	report, err = s.DuplicateOverview(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates != 4 || report.Hashed != 4 || !report.Settled {
		t.Fatalf("fully hashed candidates did not settle: %+v", report)
	}
	if len(report.Unproven) != 0 || len(report.Groups) != 1 {
		t.Fatalf("distinct files should leave no open question: %+v", report)
	}
}

// A file removed from disk is no longer a candidate, so it must not hold the
// question open or count against coverage.
func TestDuplicateCoverageIgnoresRemovedFiles(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for id, path := range map[int]string{1: "/archive/2000/2000-01/2000-01-02/A.MOV", 2: "/archive/2000/2000-01/2000-01-03/B.MOV"} {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'video',500,'archive')", id, path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,'2000-01-02')", id); err != nil {
			t.Fatal(err)
		}
	}
	candidates, hashed, err := s.DuplicateStatus(ctx)
	if err != nil || candidates != 2 || hashed != 0 {
		t.Fatalf("unexpected coverage before removal: %d %d %v", candidates, hashed, err)
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) VALUES(2,'culled','plan-1')"); err != nil {
		t.Fatal(err)
	}
	candidates, hashed, err = s.DuplicateStatus(ctx)
	if err != nil || candidates != 0 || hashed != 0 {
		t.Fatalf("removed file still counted: %d %d %v", candidates, hashed, err)
	}
}

// A copy the archive scan found gone is not a duplicate of anything: the file
// left behind is the only one, so it must not show as a group of one, and the
// gone copy is no longer a candidate for hashing.
func TestDuplicatesIgnoreMissingFiles(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for id, path := range map[int]string{1: "/archive/2026/2026-07/2026-07-25/A7401914.ARW", 2: "/archive/2026/2026-07/2026-07-25/A7401914-2.ARW"} {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'raw',500,'archive')", id, path); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_evidence(asset_id,full_hash) VALUES(?,'same')", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO missing_assets(asset_id,since) VALUES(1,datetime('now'))"); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	groups, err := s.ExactDuplicates(ctx, "", 100)
	if err != nil || len(groups) != 0 {
		t.Fatalf("missing copy still made a group: %+v %v", groups, err)
	}
	candidates, _, err := s.DuplicateStatus(ctx)
	if err != nil || candidates != 0 {
		t.Fatalf("missing copy still a candidate: %d %v", candidates, err)
	}
}
