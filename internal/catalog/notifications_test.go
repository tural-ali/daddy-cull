package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A night's graduation lands files on a date already reviewed: the date comes
// back waiting with a red dot, the bell lists the arrival, and the dot goes
// once the new files are dealt with.
func TestArrivalsReopenReviewedDaysWithARedDot(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	write := func(rel string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("media"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan := func() {
		t.Helper()
		if _, err := s.ScanArchive(ctx, root); err != nil {
			t.Fatal(err)
		}
	}
	cell := func(md string) CalendarCell {
		t.Helper()
		calendar, err := s.Calendar(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		month := int(md[0]-'0')*10 + int(md[1]-'0')
		day := int(md[3]-'0')*10 + int(md[4]-'0')
		return *calendar.Months[month-1].Cells[day-1]
	}
	idOf := func(name string) int64 {
		t.Helper()
		var id int64
		if err := s.read.QueryRow("SELECT id FROM assets WHERE relative_path LIKE ?", "%/"+name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	write("2026/2026-09/2026-09-26/IMG_0001.HEIC")
	write("2026/2026-09/2026-09-26/IMG_0002.HEIC")
	write("2019/2019-09/2019-09-27/IMG_0100.HEIC")
	scan()
	// A first sight of a date is news too, until it is reviewed.
	if got := cell("09-26").Fresh; got != 2 {
		t.Fatalf("new date: fresh %d, want 2", got)
	}
	if _, err := s.MarkDateReviewed(ctx, "09-26", "review-0926-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDateReviewed(ctx, "09-27", "review-0927-a"); err != nil {
		t.Fatal(err)
	}
	if c := cell("09-26"); c.State != "done" || c.Fresh != 0 {
		t.Fatalf("reviewed date: %+v", c)
	}
	streakBefore, err := s.reviewTimes(ctx, time.UTC)
	if err != nil {
		t.Fatal(err)
	}

	write("2026/2026-09/2026-09-26/IMG_0003.HEIC")
	write("2026/2026-09/2026-09-27/IMG_0004.HEIC")
	scan()
	if c := cell("09-26"); c.State != "todo" || c.Waiting != 3 || c.Fresh != 1 {
		t.Fatalf("09-26 after an arrival: %+v, want waiting again with one fresh file", c)
	}
	// 09-27 was reviewed for 2019 only; the new year's day brings it back part-done.
	if c := cell("09-27"); c.State != "part" || c.Fresh != 1 {
		t.Fatalf("09-27 after an arrival: %+v", c)
	}
	today, err := s.Today(ctx, "09-26")
	if err != nil {
		t.Fatal(err)
	}
	if today.Years[0].Fresh != 1 || today.Years[0].Status != "pending" {
		t.Fatalf("day page: %+v", today.Years[0])
	}
	for _, a := range today.Years[0].Assets {
		if a.New != strings.HasSuffix(a.Path, "IMG_0003.HEIC") {
			t.Fatalf("%s marked new=%v", a.Path, a.New)
		}
	}
	streakAfter, err := s.reviewTimes(ctx, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(streakAfter) < len(streakBefore) {
		t.Fatalf("reopening took reviews out of the streak: %d, then %d", len(streakBefore), len(streakAfter))
	}

	list, err := s.ListNotifications(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if list.Unread != 2 || len(list.Items) != 2 {
		t.Fatalf("notifications: %+v", list)
	}
	latest := list.Items[0]
	if latest.Kind != "arrivals" || latest.Files != 2 || len(latest.Days) != 2 {
		t.Fatalf("latest: %+v", latest)
	}
	if d := latest.Days[0]; d.Day != "2026-09-26" || d.MD != "09-26" || !d.Reopened || d.Files != 1 || d.Fresh != 1 {
		t.Fatalf("reopened day: %+v", d)
	}
	if d := latest.Days[1]; d.Day != "2026-09-27" || d.Reopened || d.Fresh != 1 {
		t.Fatalf("new year's day: %+v", d)
	}

	// Deciding the one new file clears the dot, though the date waits to be marked.
	if _, err = s.Decide(ctx, Decision{RequestID: "decide-new-0003", AssetID: idOf("IMG_0003.HEIC"), Status: "keep"}); err != nil {
		t.Fatal(err)
	}
	if got := cell("09-26").Fresh; got != 0 {
		t.Fatalf("09-26 after deciding the new file: fresh %d", got)
	}
	// Marking the date reviewed clears it, and taking the mark back does not
	// bring it back: the file was looked at after it arrived.
	if _, err = s.SetDayProgress(ctx, DayProgressChange{Day: "2026-09-27", Status: "done", RequestID: "done-0927-2026"}); err != nil {
		t.Fatal(err)
	}
	if got := cell("09-27").Fresh; got != 0 {
		t.Fatalf("09-27 reviewed: fresh %d", got)
	}
	if _, err = s.SetDayProgress(ctx, DayProgressChange{Day: "2026-09-27", Status: "pending", RequestID: "undo-0927-2026"}); err != nil {
		t.Fatal(err)
	}
	if got := cell("09-27").Fresh; got != 0 {
		t.Fatalf("09-27 review taken back: fresh %d", got)
	}

	if err = s.ReadNotifications(ctx, list.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadNotifications(ctx); n != 1 {
		t.Fatalf("reading up to the older one left %d unread, want 1", n)
	}
	if err = s.ReadNotifications(ctx, latest.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadNotifications(ctx); n != 0 {
		t.Fatalf("all read, still %d unread", n)
	}
	// A scan that adds nothing says nothing.
	scan()
	if n, _ := s.UnreadNotifications(ctx); n != 0 {
		t.Fatalf("an empty scan notified: %d", n)
	}
}

func TestPhoneDeletionMarksAreNotified(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(7,'/archive/2026/2026-09/2026-09-26/IMG_0007.HEIC',1,'image',300,'archive')"); err != nil {
		t.Fatal(err)
	}
	root := "/mnt/user/family-archive"
	line := root + "/2026/2026-09/2026-09-26/IMG_0007.HEIC\t300\talex\t2026/09/26/IMG_0007.HEIC\t2026-09-28T00:00:00+00:00\n"
	for range 2 {
		if _, err := s.MarkPhoneDeletions(ctx, root, strings.NewReader(line)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListNotifications(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if list.Unread != 1 || len(list.Items) != 1 || list.Items[0].Kind != "phone-deletions" || list.Items[0].Files != 1 || list.Items[0].Bytes != 300 {
		t.Fatalf("a deletion handled twice should notify once: %+v", list)
	}
}
