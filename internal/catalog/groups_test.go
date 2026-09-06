package catalog

import (
	"context"
	"testing"
)

func TestUnifiedQueueAndAtomicChoice(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if e := s.Seed(ctx, 40); e != nil {
		t.Fatal(e)
	}
	p, e := s.ReviewPage(ctx, "", "", 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Assets) != 31 {
		t.Fatalf("expected 31 memories, got %d", len(p.Assets))
	}
	for _, a := range p.Assets {
		if a.ID == 4 {
			t.Fatal("matched Takeout copy appears twice")
		}
	}
	group, e := s.Alternatives(ctx, 3)
	if e != nil || len(group) != 2 {
		t.Fatalf("%+v %v", group, e)
	}
	if group[0].Source != "archive" || group[1].Source != "takeout" {
		t.Fatal("missing provenance")
	}
	ds := []Decision{{RequestID: "group-001", AssetID: 3, Status: "keep"}, {RequestID: "group-002", AssetID: 4, Status: "cull", ExpectedRevision: 99}}
	if _, e = s.DecideBatch(ctx, ds); e == nil {
		t.Fatal("stale group applied")
	}
	group, _ = s.Alternatives(ctx, 3)
	if group[0].Revision != 0 {
		t.Fatal("partially saved group")
	}
	ds[1].ExpectedRevision = 0
	if _, e = s.DecideBatch(ctx, ds); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideBatch(ctx, ds); e != nil {
		t.Fatal("idempotent batch failed", e)
	}
	group, _ = s.Alternatives(ctx, 3)
	if group[0].Status != "keep" || group[1].Status != "cull" {
		t.Fatal("wrong keeper")
	}
}

func TestUndecidedQueueExcludesSavedChoicesAndUndoRestores(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if e := s.Seed(ctx, 40); e != nil {
		t.Fatal(e)
	}
	p, e := s.page(ctx, "", "", "", 40, true, "", false, "unreviewed")
	if e != nil {
		t.Fatal(e)
	}
	first := p.Assets[0]
	saved, e := s.Decide(ctx, Decision{RequestID: "queue-test-keep", AssetID: first.ID, Status: "keep"})
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := s.page(ctx, "", "", "", 40, true, "", false, "unreviewed")
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range fresh.Assets {
		if a.ID == first.ID {
			t.Fatal("decided photo reappeared after refresh")
		}
	}
	if len(fresh.Assets) != len(p.Assets)-1 {
		t.Fatal("wrong pending count")
	}
	if _, e = s.Decide(ctx, Decision{RequestID: "queue-test-undo", AssetID: first.ID, Status: "unreviewed", ExpectedRevision: saved.Revision}); e != nil {
		t.Fatal(e)
	}
	restored, _ := s.page(ctx, "", "", "", 40, true, "", false, "unreviewed")
	if restored.Assets[0].ID != first.ID {
		t.Fatal("undo did not restore pending photo")
	}
}
