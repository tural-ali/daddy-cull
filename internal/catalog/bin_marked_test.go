package catalog

import (
	"context"
	"testing"
)

// A reviewer who has just marked some clips comes straight to the Bin to carry
// them out, so the newest mark has to be the first thing on the page. Ordered by
// capture date instead, a clip filmed in 2020 and marked a minute ago lands in
// the middle of the list, which reads as the mark not having been recorded.
func TestMarkedForBinPutsTheNewestMarkFirst(t *testing.T) {
	s := testStore(t)
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2020/old-clip.mp4',1000,'video',10,'archive'),
		(2,'/archive/2024/new-photo.jpg',9000,'image',10,'archive'),
		(3,'/archive/2022/imported.jpg',5000,'image',10,'archive')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO decisions(asset_id,status,favourite,revision) VALUES (1,'cull',0,1),(2,'cull',0,1),(3,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	// Asset 2 was marked first, asset 1 a minute ago. Asset 3 came from the
	// earlier tool and carries no event at all.
	if _, err := s.write.Exec(`INSERT INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite,created_at) VALUES
		('a',2,0,'cull',0,'unreviewed',0,'2026-09-15 10:00:00'),
		('b',1,0,'cull',0,'unreviewed',0,'2026-09-15 14:28:54')`); err != nil {
		t.Fatal(err)
	}
	marked, err := s.MarkedForBin(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, item := range marked {
		order = append(order, item.ID)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("marked list came back as %v, wanted the newest mark first and the unrecorded one last", order)
	}
	// The time has to say it is UTC, or a browser reads it as local and reports
	// a mark made a minute ago as hours away.
	if marked[0].MarkedAt != "2026-09-15T14:28:54Z" {
		t.Fatalf("mark time came back as %q", marked[0].MarkedAt)
	}
	if marked[2].MarkedAt != "" {
		t.Fatalf("an unrecorded mark invented a time: %q", marked[2].MarkedAt)
	}
}

// Screenshots and Takeout sources have their own routes out and must never be
// offered to the archive writer.
func TestMarkedForBinOffersOnlyArchiveFiles(t *testing.T) {
	s := testStore(t)
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2020/a.jpg',1,'image',10,'archive'),
		(2,'/screenshots/b.png',2,'image',10,'screenshots'),
		(3,'/upgrades/c.jpg',3,'image',10,'takeout')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO decisions(asset_id,status,favourite,revision) VALUES (1,'cull',0,1),(2,'cull',0,1),(3,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	marked, err := s.MarkedForBin(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0].ID != 1 {
		t.Fatalf("marked list offered something other than the archive file: %+v", marked)
	}
}

// A file already moved out is no longer waiting to move.
func TestMarkedForBinLeavesOutWhatHasAlreadyMoved(t *testing.T) {
	s := testStore(t)
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2020/a.jpg',1,'image',10,'archive'),
		(2,'/archive/2020/b.jpg',2,'image',10,'archive')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO decisions(asset_id,status,favourite,revision) VALUES (1,'cull',0,1),(2,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO file_state(asset_id,plan_id,state) VALUES (2,'p1','bin')`); err != nil {
		t.Fatal(err)
	}
	marked, err := s.MarkedForBin(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0].ID != 1 {
		t.Fatalf("a file already in the Bin was still listed as waiting: %+v", marked)
	}
}
