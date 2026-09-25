package catalog

import (
	"context"
	"testing"
	"time"
)

func TestCalendarMatchesLegacyDateAcrossYears(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	rows := []struct {
		path string
		when int64
	}{
		{"/archive/2016/2016-09/2016-09-07/A.JPG", time.Date(2016, 9, 7, 10, 0, 0, 0, time.UTC).Unix()},
		{"/archive/2020/2020-09/2020-09-07/B.JPG", time.Date(2020, 9, 7, 11, 0, 0, 0, time.UTC).Unix()},
		{"/archive/2020/2020-09/2020-09-07/C.JPG", time.Date(2020, 9, 7, 12, 0, 0, 0, time.UTC).Unix()},
		// Folder date is authoritative when imported capture metadata is unknown.
		{"/archive/1999/1999-09/1999-09-07/SCAN.JPG", 0},
	}
	for _, row := range rows {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,'image',10,'archive')", row.path, row.when); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.ExecContext(ctx, "UPDATE stats SET total=?", len(rows)); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	year, err := s.Calendar(ctx, time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	cell := year.Months[8].Cells[6]
	if cell == nil || cell.MD != "09-07" || cell.Years != 3 || cell.Files != 4 || cell.State != "todo" || !cell.Today {
		t.Fatalf("unexpected calendar cell: %+v", cell)
	}
	today, err := s.Today(ctx, "09-07", time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(today.Years) != 3 || today.Years[0].Day != "1999-09-07" || today.Memories != 4 || today.Bytes != 40 {
		t.Fatalf("unexpected today response: %+v", today)
	}
}

func TestCalendarProgressIsSeparateFromPhotoDecisions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES('/archive/2020/2020-01/2020-01-02/A.JPG',1577966400,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := s.SetDayProgress(ctx, DayProgressChange{Day: "2020-01-02", Status: "done", RequestID: "progress-request-1"})
	if err != nil || first.Status != "done" || first.PreviousStatus != "pending" {
		t.Fatalf("mark done: %+v %v", first, err)
	}
	again, err := s.SetDayProgress(ctx, DayProgressChange{Day: "2020-01-02", Status: "done", RequestID: "progress-request-1"})
	if err != nil || again != first {
		t.Fatalf("idempotent progress: %+v %v", again, err)
	}
	year, err := s.Calendar(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || year.Progress.Done != 1 || year.Progress.Dates != 1 || year.Months[0].Cells[1].State != "done" {
		t.Fatalf("unexpected progress: %+v %v", year.Progress, err)
	}
	var decisions int
	if err := s.read.QueryRowContext(ctx, "SELECT count(*) FROM decisions").Scan(&decisions); err != nil || decisions != 0 {
		t.Fatalf("day completion changed decisions: %d %v", decisions, err)
	}
}

func TestCalendarRejectsInvalidDateAndConflictingRequest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Today(ctx, "02-30", time.Now()); err == nil {
		t.Fatal("accepted impossible month-day")
	}
	if _, err := s.SetDayProgress(ctx, DayProgressChange{Day: "2020-01-02", Status: "done", RequestID: "progress-request-2"}); err == nil {
		t.Fatal("accepted progress for day absent from catalogue")
	}
}

func TestArchiveDayUsesDatedHoldingAreaFilename(t *testing.T) {
	day, ok := archiveDay("/archive/screenshots/2024-03-12_capture.png", 0)
	if !ok || day != "2024-03-12" {
		t.Fatalf("dated filename not indexed: %q %v", day, ok)
	}
}

// A date is reviewed once a year, because each year adds another anniversary of
// it. A mark made last year must leave the date waiting again from 1 January,
// on the calendar and on the day page alike, without the mark being lost.
func TestCalendarReviewMarksLapseAtTheTurnOfTheYear(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES('/archive/2020/2020-09/2020-09-25/A.HEIC',1601028000,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO day_progress(day,status,reviewed_at) VALUES('2020-09-25','done','2026-09-25T09:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		now   time.Time
		state string
	}{
		{time.Date(2026, 12, 31, 23, 30, 0, 0, berlin), "done"},
		{time.Date(2027, 1, 1, 0, 30, 0, 0, berlin), "todo"},
		{time.Date(2027, 9, 25, 12, 0, 0, 0, berlin), "todo"},
	} {
		year, err := s.Calendar(ctx, check.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := year.Months[8].Cells[24].State; got != check.state {
			t.Fatalf("on %s the 25 September cell is %q, wanted %q", check.now, got, check.state)
		}
		today, err := s.Today(ctx, "09-25", check.now)
		if err != nil {
			t.Fatal(err)
		}
		want := "pending"
		if check.state == "done" {
			want = "done"
		}
		if len(today.Years) != 1 || today.Years[0].Status != want {
			t.Fatalf("on %s the day page shows %+v, wanted %q", check.now, today.Years, want)
		}
	}
	var kept string
	if err := s.read.QueryRowContext(ctx, "SELECT status FROM day_progress WHERE day='2020-09-25'").Scan(&kept); err != nil || kept != "done" {
		t.Fatalf("the lapsed mark was not kept as history: %q %v", kept, err)
	}
}

// The calendar marks where culling has happened: files marked for the Bin, files
// already moved or deleted, and files the earlier tool removed. A file that has
// left the archive no longer counts as waiting for review.
func TestCalendarCountsRemovedFilesPerDate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, path := range []string{
		"/archive/2020/2020-09/2020-09-25/KEEP.JPG",
		"/archive/2020/2020-09/2020-09-25/MARKED.JPG",
		"/archive/2021/2021-09/2021-09-25/PURGED.JPG",
	} {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,0,'image',10,'archive')", path); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"INSERT INTO decisions(asset_id,status,favourite,revision) SELECT id,'cull',0,1 FROM assets WHERE relative_path LIKE '%MARKED.JPG' OR relative_path LIKE '%PURGED.JPG'",
		"INSERT INTO file_state(asset_id,state,plan_id) SELECT id,'purged','plan' FROM assets WHERE relative_path LIKE '%PURGED.JPG'",
		"INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,day,size_bytes,culled_at) VALUES(1,'b','media','/disks/disk1/2019/x.JPG','/disks/disk1/.culled/x.JPG','2019-09-25',10,'2026-09-01')",
		"INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,day,size_bytes,culled_at,restored_at) VALUES(2,'b','media','/disks/disk1/2019/y.JPG','/disks/disk1/.culled/y.JPG','2019-09-25',10,'2026-09-01','2026-09-02')",
	} {
		if _, err := s.write.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	year, err := s.Calendar(ctx, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	cell := year.Months[8].Cells[24]
	if cell.Removed != 3 || cell.Years != 1 || cell.Files != 2 {
		t.Fatalf("wanted 3 removed and one year of 2 waiting files, got %+v", cell)
	}
}
