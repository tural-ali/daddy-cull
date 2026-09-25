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
	today, err := s.Today(ctx, "09-07")
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
	if _, err := s.Today(ctx, "02-30"); err == nil {
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

// A review mark belongs to one year's day. A reviewed date stays reviewed until
// media from a new year lands on it, and then comes back with only the new files
// waiting. A file that has left the archive no longer waits at all.
func TestCalendarReviewedDateReturnsWhenANewYearAddsMedia(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	add := func(paths ...string) {
		for _, path := range paths {
			if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,0,'image',10,'archive')", path); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.IndexCalendar(ctx); err != nil {
			t.Fatal(err)
		}
	}
	add("/archive/2020/2020-09/2020-09-25/A.HEIC", "/archive/2020/2020-09/2020-09-25/B.HEIC", "/archive/2021/2021-09/2021-09-25/GONE.JPG")
	if _, err := s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) SELECT id,'purged','plan' FROM assets WHERE relative_path LIKE '%GONE.JPG'"); err != nil {
		t.Fatal(err)
	}
	cell := func() *CalendarCell {
		year, err := s.Calendar(ctx, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		return year.Months[8].Cells[24]
	}
	if got := cell(); got.State != "todo" || got.Years != 1 || got.Files != 2 || got.Waiting != 2 {
		t.Fatalf("before review, wanted one year of 2 waiting files and the purged one left out: %+v", got)
	}
	if _, err := s.SetDayProgress(ctx, DayProgressChange{Day: "2020-09-25", Status: "done", RequestID: "reviewed-2020-09-25"}); err != nil {
		t.Fatal(err)
	}
	if got := cell(); got.State != "done" || got.Waiting != 0 {
		t.Fatalf("after review, wanted a reviewed date with nothing waiting: %+v", got)
	}
	add("/archive/2027/2027-09/2027-09-25/NEW1.HEIC", "/archive/2027/2027-09/2027-09-25/NEW2.HEIC", "/archive/2027/2027-09/2027-09-25/NEW3.HEIC")
	if got := cell(); got.State == "done" || got.Waiting != 3 || got.Files != 5 {
		t.Fatalf("a new year's media should bring the date back with 3 waiting: %+v", got)
	}
}
