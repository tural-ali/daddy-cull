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
	events, err := s.History(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != "keep" || events[0].PreviousStatus != "cull" || events[0].Asset.Revision != 2 {
		t.Fatalf("unexpected history: %+v", events)
	}
}
