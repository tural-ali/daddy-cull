package catalog

import (
	"context"
	"testing"
)

func TestHistoryReturnsDurableDecisionEventsNewestFirst(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/A.JPG',1,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, Decision{AssetID: 1, Status: "cull", ExpectedRevision: 0, RequestID: "history-request-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, Decision{AssetID: 1, Status: "keep", ExpectedRevision: 1, RequestID: "history-request-2"}); err != nil {
		t.Fatal(err)
	}
	events, err := s.History(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != "keep" || events[0].PreviousStatus != "cull" || events[0].Asset.Revision != 2 {
		t.Fatalf("unexpected history: %+v", events)
	}
	older, err := s.History(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 1 || older[0].RequestID != "history-request-1" {
		t.Fatalf("the second page should hold the first choice: %+v", older)
	}
	if _, err = s.History(ctx, 1, -1); err != ErrInvalid {
		t.Fatalf("a negative offset should be refused, got %v", err)
	}
}

// The events table holds two shapes of time: SQLite's CURRENT_TIMESTAMP, which
// is UTC but says so nowhere, and RFC 3339 from the history imported from the
// earlier tool. Both have to come back as one UTC form a browser reads exactly.
func TestHistoryTimesAreOneUnambiguousUTCForm(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/A.JPG',1,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, `INSERT INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite,created_at) VALUES
		('imported',1,0,'keep',0,'unreviewed',0,'2026-08-25T12:01:56+00:00'),
		('offset',1,0,'keep',0,'unreviewed',0,'2026-08-25T14:01:56+02:00'),
		('live',1,0,'cull',0,'keep',0,'2026-09-15 13:16:20')`); err != nil {
		t.Fatal(err)
	}
	events, err := s.History(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, event := range events {
		got[event.RequestID] = event.CreatedAt
	}
	want := map[string]string{"imported": "2026-08-25T12:01:56Z", "offset": "2026-08-25T12:01:56Z", "live": "2026-09-15T13:16:20Z"}
	for id, at := range want {
		if got[id] != at {
			t.Fatalf("%s came back as %q, wanted %q", id, got[id], at)
		}
	}
}

// The earlier tool's history was imported after this app had already recorded
// choices of its own, so insertion order is not the order things happened in.
func TestHistoryIsInTheOrderThingsHappened(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/A.JPG',1,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, `INSERT INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite,created_at) VALUES
		('live',1,0,'keep',0,'unreviewed',0,'2026-09-06 00:10:33'),
		('imported-later',1,0,'keep',0,'unreviewed',0,'2026-08-25T12:01:56+00:00'),
		('newest',1,0,'cull',0,'keep',0,'2026-09-15 13:16:20')`); err != nil {
		t.Fatal(err)
	}
	events, err := s.History(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].RequestID != "newest" || events[1].RequestID != "live" || events[2].RequestID != "imported-later" {
		t.Fatalf("history order: %+v", events)
	}
}
