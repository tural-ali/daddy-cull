package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	// KeptDays is set when a deletion was only scheduled: the files stay on
	// disk this many days, restorable from the Log, then go for good.
	KeptDays int `json:"keptDays,omitempty"`
}

// actOutcome records which groups an action finished and why the others did
// not, so a schedule is only dropped for a group that really was handled.
type actOutcome struct {
	done   map[string]bool
	failed map[string]string
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

// The ways a selection can fail to match the Bin.
var (
	errTrashEmptySelection = errors.New("nothing was selected")
	errTrashChanged        = errors.New("the Bin has changed since this page was loaded; refresh and select again")
)

// resolve returns the current items for the given keys from one list, widened
// to whole groups, in list order.
func (t *TrashWriter) resolve(ctx context.Context, keys []string, list func(context.Context) ([]TrashItem, error)) ([]TrashItem, error) {
	return resolveTrash(ctx, keys, list)
}

// resolveTrash is resolve for any process that can read the catalogue: the
// web process checks a queued request the way the writer will.
func resolveTrash(ctx context.Context, keys []string, list func(context.Context) ([]TrashItem, error)) ([]TrashItem, error) {
	if len(keys) == 0 {
		return nil, errTrashEmptySelection
	}
	all, err := list(ctx)
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
			return nil, errTrashChanged
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
	items, err := t.resolve(ctx, keys, t.s.Trash)
	return len(items), err
}

// deletingItems lists only the files deleted from the Bin and still waiting.
func (t *TrashWriter) deletingItems(ctx context.Context) ([]TrashItem, error) {
	return t.s.deletingTrash(ctx)
}

// deletingTrash lists the files deleted from the Bin and still waiting, as
// cards.
func (s *Store) deletingTrash(ctx context.Context) ([]TrashItem, error) {
	grace, err := s.GraceDays(ctx)
	if err != nil {
		grace = 0
	}
	waiting, err := s.deleting(ctx, grace)
	if err != nil {
		return nil, err
	}
	items := make([]TrashItem, 0, len(waiting))
	for _, item := range waiting {
		items = append(items, item.TrashItem)
	}
	return items, nil
}

// Restore puts files back where they came from, whether they are in the Bin or
// were deleted from it and are still waiting out the grace period.
func (t *TrashWriter) Restore(ctx context.Context, keys []string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items, err := t.resolve(ctx, keys, t.s.held)
	if err != nil {
		return TrashResult{}, err
	}
	result, outcome := t.act(ctx, items, false)
	return result, t.s.unschedule(ctx, setKeys(outcome.done))
}

// RestoreFile puts back exactly one photograph, sidecars included, leaving the
// rest of its batch where it is. The writer's own batches can give back a
// single photograph; a batch from another engine moves only as a whole, so a
// file in one of those is refused unless it is alone in its batch.
func (t *TrashWriter) RestoreFile(ctx context.Context, key string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	all, err := t.s.held(ctx)
	if err != nil {
		return TrashResult{}, err
	}
	var item *TrashItem
	members := 0
	for i := range all {
		if all[i].Key == key {
			item = &all[i]
		}
	}
	if item == nil {
		return TrashResult{}, fmt.Errorf("the Bin has changed since this page was loaded; refresh and select again")
	}
	for _, other := range all {
		if other.Group == item.Group {
			members++
		}
	}
	if item.Source != "bin" {
		if members > 1 {
			return TrashResult{}, fmt.Errorf("%s was moved with %d other files and can only be restored with them", item.Name, members-1)
		}
		result, outcome := t.act(ctx, []TrashItem{*item}, false)
		return result, t.s.unschedule(ctx, setKeys(outcome.done))
	}
	if t.bin == nil {
		return TrashResult{}, fmt.Errorf("the archive writer is not configured")
	}
	result := TrashResult{Failures: []TrashFailure{}}
	plan, err := t.bin.Return(ctx, item.planID, item.assetID)
	if err != nil {
		result.Failures = append(result.Failures, TrashFailure{Name: item.Name, Error: err.Error()})
		return result, nil
	}
	result.Done, result.Bytes = 1, item.Size
	if plan.State == "restored" {
		return result, t.s.unschedule(ctx, []string{item.Group})
	}
	return result, nil
}

func (t *TrashWriter) Delete(ctx context.Context, keys []string, confirmation string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items, err := t.resolve(ctx, keys, t.s.Trash)
	if err != nil {
		return TrashResult{}, err
	}
	if confirmation != DeleteConfirmation(len(items)) {
		return TrashResult{}, fmt.Errorf("deletion was not confirmed for these %d files", len(items))
	}
	return t.remove(ctx, items)
}

// PurgeNow deletes files already deleted from the Bin without waiting for the
// grace period to end, for a reviewer who is sure and needs the space.
func (t *TrashWriter) PurgeNow(ctx context.Context, keys []string, confirmation string) (TrashResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	items, err := t.resolve(ctx, keys, t.deletingItems)
	if err != nil {
		return TrashResult{}, err
	}
	if confirmation != DeleteConfirmation(len(items)) {
		return TrashResult{}, fmt.Errorf("deletion was not confirmed for these %d files", len(items))
	}
	result, outcome := t.act(ctx, items, true)
	return result, t.s.unschedule(ctx, setKeys(outcome.done))
}

// remove deletes files from the Bin: at once when the grace period is zero,
// otherwise by scheduling them for the reaper. A file marked but never moved is
// moved into the Bin first, so every scheduled file waits somewhere the writer
// owns rather than in the middle of the archive.
func (t *TrashWriter) remove(ctx context.Context, items []TrashItem) (TrashResult, error) {
	grace, err := t.s.GraceDays(ctx)
	if err != nil {
		return TrashResult{}, err
	}
	if grace == 0 {
		result, _ := t.act(ctx, items, true)
		return result, nil
	}
	result := TrashResult{Failures: []TrashFailure{}, KeptDays: grace}
	groups := make([]string, 0)
	var marked []TrashItem
	for _, item := range items {
		if item.Source == "marked" {
			marked = append(marked, item)
			continue
		}
		result.Done++
		result.Bytes += item.Size
	}
	for _, group := range groupsOf(items) {
		if !strings.HasPrefix(group, "marked:") {
			groups = append(groups, group)
		}
	}
	for start := 0; start < len(marked); start += engineBatch {
		chunk := marked[start:min(start+engineBatch, len(marked))]
		id, err := t.quarantine(ctx, chunk)
		if err != nil {
			for _, item := range chunk {
				result.Failures = append(result.Failures, TrashFailure{Name: item.Name, Error: err.Error()})
			}
			continue
		}
		groups = append(groups, "bin:"+id)
		for _, item := range chunk {
			result.Done++
			result.Bytes += item.Size
		}
	}
	return result, t.s.scheduleDeletion(ctx, groups, time.Now())
}

// quarantine moves marked files into the Bin as one writer batch and returns
// the batch, so it can be scheduled for deletion as a whole.
func (t *TrashWriter) quarantine(ctx context.Context, chunk []TrashItem) (string, error) {
	if t.bin == nil {
		return "", fmt.Errorf("the archive writer is not configured")
	}
	ids := make([]int64, 0, len(chunk))
	for _, item := range chunk {
		ids = append(ids, item.assetID)
	}
	plan, err := t.bin.Preview(ctx, ids)
	if err != nil {
		return "", err
	}
	if plan, err = t.bin.Run(ctx, plan.ID, "quarantine", ""); err != nil {
		return "", err
	}
	return plan.ID, nil
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
	return t.remove(ctx, items)
}

// act runs one action over resolved items, source by source. Each group
// succeeds or fails on its own, and a failure never stops the rest.
func (t *TrashWriter) act(ctx context.Context, items []TrashItem, purge bool) (TrashResult, actOutcome) {
	result := TrashResult{Failures: []TrashFailure{}}
	outcome := actOutcome{done: map[string]bool{}, failed: map[string]string{}}
	fail := func(names []TrashItem, err error) {
		for _, item := range names {
			result.Failures = append(result.Failures, TrashFailure{Name: item.Name, Error: err.Error()})
			outcome.failed[item.Group] = err.Error()
		}
	}
	done := func(names []TrashItem) {
		for _, item := range names {
			result.Done++
			result.Bytes += item.Size
			outcome.done[item.Group] = true
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
	return result, outcome
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
