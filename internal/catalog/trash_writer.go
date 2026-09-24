package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// TrashWriter carries out the Bin page's three actions, restore, delete and
// empty, across every source the Bin lists. It adds no way of moving a file:
// each source is still moved only by its own engine, with that engine's
// fingerprints, ledger and recovery. What it adds is the reviewer's view, one
// selection acted on at once, so the page does not need to know which tool put
// a file in the Bin.
//
// A selection is resolved against the Bin as it is now, never trusted from the
// page, and widened to whole groups, because an engine moves a batch with its
// sidecars or not at all. A key the Bin no longer holds is refused outright, so
// a stale page cannot act on something it did not show.
type TrashWriter struct {
	s      *Store
	bin    *BinEngine
	legacy *LegacyBinEngine
	shots  *ScreenshotWriter
	mu     sync.Mutex
}

func NewTrashWriter(s *Store, bin *BinEngine, legacy *LegacyBinEngine, shots *ScreenshotWriter) *TrashWriter {
	return &TrashWriter{s: s, bin: bin, legacy: legacy, shots: shots}
}

// TrashResult reports what happened to each group, so a partial failure is
// shown as exactly that rather than as success or as nothing having happened.
type TrashResult struct {
	Done     int            `json:"done"`
	Bytes    int64          `json:"bytes"`
	Failures []TrashFailure `json:"failures"`
}

type TrashFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// engineBatch is the most any engine accepts in one plan.
const engineBatch = 20

// DeleteConfirmation is the sentence a deletion must carry. The page asks the
// reviewer first and sends it only once they have agreed; the server checks it
// so that nothing else can delete by accident.
func DeleteConfirmation(items int) string { return fmt.Sprintf("DELETE %d", items) }

// resolve returns the Bin's current items for the given keys, widened to whole
// groups, in Bin order.
func (t *TrashWriter) resolve(ctx context.Context, keys []string) ([]TrashItem, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("nothing was selected")
	}
	all, err := t.s.Trash(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]TrashItem, len(all))
	for _, item := range all {
		byKey[item.Key] = item
	}
	groups := map[string]bool{}
	for _, key := range keys {
		item, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("the Bin has changed since this page was loaded; refresh and select again")
		}
		groups[item.Group] = true
	}
	selected := make([]TrashItem, 0, len(keys))
	for _, item := range all {
		if groups[item.Group] {
			selected = append(selected, item)
		}
	}
	return selected, nil
}

// Selection reports how many cards an action on these keys would affect once
// widened to whole groups, which is the number the confirmation must name.
func (t *TrashWriter) Selection(ctx context.Context, keys []string) (int, error) {
	items, err := t.resolve(ctx, keys)
	return len(items), err
}

func (t *TrashWriter) Restore(ctx context.Context, keys []string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items, err := t.resolve(ctx, keys)
	if err != nil {
		return TrashResult{}, err
	}
	return t.act(ctx, items, false), nil
}

func (t *TrashWriter) Delete(ctx context.Context, keys []string, confirmation string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items, err := t.resolve(ctx, keys)
	if err != nil {
		return TrashResult{}, err
	}
	if confirmation != DeleteConfirmation(len(items)) {
		return TrashResult{}, fmt.Errorf("deletion was not confirmed for these %d files", len(items))
	}
	return t.act(ctx, items, true), nil
}

// Empty deletes everything the Bin holds. The confirmation must name the count
// the reviewer was shown, so a Bin that grew in the meantime is not emptied of
// files they never saw.
func (t *TrashWriter) Empty(ctx context.Context, confirmation string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items, err := t.s.Trash(ctx)
	if err != nil {
		return TrashResult{}, err
	}
	if len(items) == 0 {
		return TrashResult{Failures: []TrashFailure{}}, nil
	}
	if confirmation != DeleteConfirmation(len(items)) {
		return TrashResult{}, fmt.Errorf("the Bin now holds %d files; refresh and confirm again", len(items))
	}
	return t.act(ctx, items, true), nil
}

// act runs one action over resolved items, source by source. Each group
// succeeds or fails on its own, and a failure never stops the rest.
func (t *TrashWriter) act(ctx context.Context, items []TrashItem, purge bool) TrashResult {
	result := TrashResult{Failures: []TrashFailure{}}
	fail := func(names []TrashItem, err error) {
		for _, item := range names {
			result.Failures = append(result.Failures, TrashFailure{Name: item.Name, Error: err.Error()})
		}
	}
	done := func(names []TrashItem) {
		for _, item := range names {
			result.Done++
			result.Bytes += item.Size
		}
	}
	var marked, legacy []TrashItem
	binGroups := map[string][]TrashItem{}
	var binOrder []string
	for _, item := range items {
		switch item.Source {
		case "marked":
			marked = append(marked, item)
		case "legacy":
			legacy = append(legacy, item)
		case "bin":
			if _, seen := binGroups[item.planID]; !seen {
				binOrder = append(binOrder, item.planID)
			}
			binGroups[item.planID] = append(binGroups[item.planID], item)
		case "screenshot":
			if err := t.screenshot(ctx, item, purge); err != nil {
				fail([]TrashItem{item}, err)
			} else {
				done([]TrashItem{item})
			}
		}
	}
	for start := 0; start < len(marked); start += engineBatch {
		chunk := marked[start:min(start+engineBatch, len(marked))]
		if err := t.markedChunk(ctx, chunk, purge); err != nil {
			fail(chunk, err)
		} else {
			done(chunk)
		}
	}
	for _, id := range binOrder {
		if err := t.binPlan(ctx, id, purge); err != nil {
			fail(binGroups[id], err)
		} else {
			done(binGroups[id])
		}
	}
	// The earlier tool's engine widens any row to its whole batch, so it is
	// given one row per batch, and a batch is never split across two plans.
	var legacyOrder []string
	legacyGroups := map[string][]TrashItem{}
	for _, item := range legacy {
		if _, seen := legacyGroups[item.Group]; !seen {
			legacyOrder = append(legacyOrder, item.Group)
		}
		legacyGroups[item.Group] = append(legacyGroups[item.Group], item)
	}
	for start := 0; start < len(legacyOrder); start += engineBatch {
		var chunk, members []TrashItem
		for _, group := range legacyOrder[start:min(start+engineBatch, len(legacyOrder))] {
			chunk = append(chunk, legacyGroups[group][0])
			members = append(members, legacyGroups[group]...)
		}
		if err := t.legacyChunk(ctx, chunk, purge); err != nil {
			fail(members, err)
		} else {
			done(members)
		}
	}
	return result
}

// markedChunk handles files that were marked but never moved. Restoring one is
// only a change of mind, so its decision goes back to undecided and no file is
// touched. Deleting one moves it into the Bin through the writer's own checks
// first, then deletes exactly what was moved.
func (t *TrashWriter) markedChunk(ctx context.Context, chunk []TrashItem, purge bool) error {
	if !purge {
		decisions := make([]Decision, 0, len(chunk))
		for _, item := range chunk {
			id, err := randomID()
			if err != nil {
				return err
			}
			decisions = append(decisions, Decision{RequestID: id, AssetID: item.assetID, ExpectedRevision: item.revision, Status: "unreviewed", Favourite: item.favourite})
		}
		_, err := t.s.DecideBatch(ctx, decisions)
		return err
	}
	if t.bin == nil {
		return fmt.Errorf("the archive writer is not configured")
	}
	ids := make([]int64, 0, len(chunk))
	for _, item := range chunk {
		ids = append(ids, item.assetID)
	}
	plan, err := t.bin.Preview(ctx, ids)
	if err != nil {
		return err
	}
	if plan, err = t.bin.Run(ctx, plan.ID, "quarantine", ""); err != nil {
		return err
	}
	_, err = t.bin.Run(ctx, plan.ID, "purge", DeleteConfirmation(len(plan.Files)))
	return err
}

func (t *TrashWriter) binPlan(ctx context.Context, id string, purge bool) error {
	if t.bin == nil {
		return fmt.Errorf("the archive writer is not configured")
	}
	plan, err := t.bin.load(id)
	if err != nil {
		return err
	}
	if !purge {
		_, err = t.bin.Run(ctx, id, "restore", "")
		return err
	}
	// A batch interrupted on its way into the Bin is finished first, so what
	// is deleted is exactly what the batch recorded, moved by its own checks.
	if plan.State == "quarantining" {
		if _, err = t.bin.Run(ctx, id, "quarantine", ""); err != nil {
			return err
		}
	}
	_, err = t.bin.Run(ctx, id, "purge", DeleteConfirmation(len(plan.Files)))
	return err
}

func (t *TrashWriter) legacyChunk(ctx context.Context, chunk []TrashItem, purge bool) error {
	if t.legacy == nil {
		return fmt.Errorf("the writer for the earlier tool's Bin is not configured")
	}
	ids := make([]int64, 0, len(chunk))
	for _, item := range chunk {
		ids = append(ids, item.legacyID)
	}
	plan, err := t.legacy.Preview(ctx, ids)
	if err != nil {
		return err
	}
	if !purge {
		_, err = t.legacy.Run(ctx, plan.ID, "restore", "")
		return err
	}
	_, err = t.legacy.Run(ctx, plan.ID, "purge", DeleteConfirmation(len(plan.Files)))
	return err
}

func (t *TrashWriter) screenshot(ctx context.Context, item TrashItem, purge bool) error {
	if t.shots == nil {
		return fmt.Errorf("the screenshot writer is not configured")
	}
	if !purge {
		_, err := t.shots.UndoRemove(ctx, item.planID)
		return err
	}
	plan, err := t.shots.load(item.planID)
	if err != nil {
		return err
	}
	_, err = t.shots.PurgeRemove(ctx, item.planID, DeleteConfirmation(len(plan.Files)))
	return err
}

func randomID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}
