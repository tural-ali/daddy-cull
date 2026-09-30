package catalog

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDecisionReplayConflictAndUndo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Seed(ctx, 2); err != nil {
		t.Fatal(err)
	}
	d := Decision{RequestID: "request-1", AssetID: 1, Status: "cull"}
	r, err := s.Decide(ctx, d)
	if err != nil || r.Revision != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	again, err := s.Decide(ctx, d)
	if err != nil || again != r {
		t.Fatal("retry changed result", err)
	}
	d.Status = "keep"
	if _, err = s.Decide(ctx, d); !errors.Is(err, ErrConflict) {
		t.Fatal("reused id accepted", err)
	}
	d.RequestID = "request-2"
	if _, err = s.Decide(ctx, d); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision accepted", err)
	}
	d.ExpectedRevision = 1
	d.Status = r.PreviousStatus
	d.Favourite = r.PreviousFavourite
	if _, err = s.Decide(ctx, d); err != nil {
		t.Fatal(err)
	}
	p, err := s.Page(ctx, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if p.Assets[0].Status != "unreviewed" || p.Assets[0].Revision != 2 {
		t.Fatal("undo lost state")
	}
}

func TestConcurrentDecisions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Seed(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"request-a", "request-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := s.Decide(ctx, Decision{RequestID: id, AssetID: 1, Status: "keep"})
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestRemovingAFileWithdrawsItsFavourite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Seed(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, Decision{RequestID: "heart-request-1", AssetID: 1, Status: "keep", Favourite: true}); err != nil {
		t.Fatal(err)
	}
	cull := Decision{RequestID: "remove-request-1", AssetID: 1, ExpectedRevision: 1, Status: "cull", Favourite: true}
	saved, err := s.Decide(ctx, cull)
	if err != nil || saved.PreviousStatus != "keep" || !saved.PreviousFavourite {
		t.Fatalf("%+v %v", saved, err)
	}
	if again, err := s.Decide(ctx, cull); err != nil || again != saved {
		t.Fatal("a retried removal was not recognised", again, err)
	}
	p, err := s.Page(ctx, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Assets[0]; got.Status != "cull" || got.Favourite {
		t.Fatalf("a removed file kept its heart: %+v", got)
	}
	var desired bool
	if err = s.read.QueryRowContext(ctx, "SELECT desired FROM immich_favourites WHERE asset_id=1").Scan(&desired); err != nil {
		t.Fatal("the withdrawn heart was not queued for Immich", err)
	}
	if desired {
		t.Fatal("Immich was asked to keep the heart on a removed file")
	}
}

// A file in the Bin cannot be decided on until it is restored, and a batch
// that names one saves nothing, so a page learns why rather than half-saving.
// The HTTP answer says it is the Bin, not another tab, that is in the way.
func TestDecidingAFileInTheBinIsRefused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Seed(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) VALUES(2,'bin','plan')"); err != nil {
		t.Fatal(err)
	}
	_, refused := s.DecideBatch(ctx, []Decision{{RequestID: "request-1", AssetID: 1, Status: "keep"}, {RequestID: "request-2", AssetID: 2, Status: "keep"}})
	if !errors.Is(refused, ErrInBin) || !errors.Is(refused, ErrConflict) {
		t.Fatalf("got %v, want the Bin's refusal", refused)
	}
	p, err := s.Page(ctx, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Assets {
		if a.Status != "unreviewed" {
			t.Fatalf("file %d saved as %s from a refused batch", a.ID, a.Status)
		}
	}
	w := httptest.NewRecorder()
	failFor(w, refused, "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "in the Bin") {
		t.Fatalf("answered %d %s", w.Code, w.Body.String())
	}
}
