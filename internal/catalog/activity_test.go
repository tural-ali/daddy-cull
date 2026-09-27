package catalog

import (
	"context"
	"testing"
	"time"
)

func TestActivityStreakCountsConsecutiveReviewDaysInTheViewersZone(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2020/2020-09/2020-09-25/A.jpg',1,'image',1,'archive')`); err != nil {
		t.Fatal(err)
	}
	if err := s.IndexCalendar(ctx); err != nil {
		t.Fatal(err)
	}
	// 22:30 UTC on 23 September is already 24 September in Berlin.
	for i, at := range []string{"2026-09-20 10:00:00", "2026-09-22 09:00:00", "2026-09-23 22:30:00", "2026-09-24 20:00:00", "2026-09-24 20:03:00"} {
		if _, err := s.write.Exec(`INSERT INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite,created_at) VALUES(?,1,0,'keep',0,'unreviewed',0,?)`, "r"+string(rune('a'+i)), at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.write.Exec(`INSERT INTO day_progress(day,status,reviewed_at) VALUES('2020-09-25','done','2026-09-23T08:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	morning := time.Date(2026, 9, 25, 8, 0, 0, 0, berlin)
	got, err := s.Activity(ctx, berlin, morning)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing yet today, so the streak is 22, 23 and 24 September and still alive.
	if got.Streak != 3 || got.Today {
		t.Fatalf("before today's first review: %+v", got)
	}
	if got.WeekDays != 4 || got.WeekSeconds != 180 {
		t.Fatalf("week: %+v", got)
	}
	if _, err = s.SetDayProgress(ctx, DayProgressChange{Day: "2020-09-25", Status: "done", RequestID: "today-mark"}); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Activity(ctx, berlin, time.Now()); err != nil || !got.Today || got.Streak < 1 {
		t.Fatalf("after marking a date today: %+v %v", got, err)
	}
	// The mark above was stamped with the real clock, so the missed day is
	// counted from today: nothing tomorrow means no streak the day after.
	if got, err = s.Activity(ctx, berlin, time.Now().In(berlin).AddDate(0, 0, 2)); err != nil || got.Streak != 0 {
		t.Fatalf("a missed day ends the streak: %+v %v", got, err)
	}
}
