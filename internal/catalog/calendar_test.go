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
