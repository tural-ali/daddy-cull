package catalog

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TrashItem is one thing the reviewer sees in the Bin, whichever tool put it
// there. The Bin has four sources that grew up separately: files marked in
// review and not yet moved, files this app's writer moved, files the earlier PHP
// tool moved, and screenshots removed from the holding area. The reviewer does
// not care which, so they are listed as one gallery and acted on as one.
//
// Group names the unit an engine moves as a whole. A marked file is its own
// group; a writer batch, a legacy batch and a screenshot plan each move together
// with their sidecars, so selecting any member selects the whole group.
type TrashItem struct {
	Key       string `json:"key"`
	Group     string `json:"group"`
	Source    string `json:"source"`
	Name      string `json:"name"`
	Original  string `json:"original"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Sidecars  int    `json:"sidecars"`
	RemovedAt string `json:"removedAt"`
	Preview   string `json:"preview,omitempty"`
	Disk      string `json:"disk,omitempty"`

	// What the writer needs to act on the item, never sent to the page.
	assetID   int64
	revision  int64
	favourite bool
	planID    string
	legacyID  int64
}

// Trash lists everything currently in the Bin, most recently removed first.
// Files deleted from the Bin are still on disk until their grace period ends,
// but they are no longer the Bin's: they are listed by Deleting instead.
func (s *Store) Trash(ctx context.Context) ([]TrashItem, error) {
	items, err := s.held(ctx)
	if err != nil {
		return nil, err
	}
	scheduled, err := s.scheduledGroups(ctx)
	if err != nil {
		return nil, err
	}
	bin := make([]TrashItem, 0, len(items))
	for _, item := range items {
		if _, gone := scheduled[item.Group]; !gone {
			bin = append(bin, item)
		}
	}
	return bin, nil
}

// held lists every removed file still on disk, whether it is in the Bin or
// waiting out its grace period after being deleted from it.
func (s *Store) held(ctx context.Context) ([]TrashItem, error) {
	items := make([]TrashItem, 0)

	marked, err := s.markedForBin(ctx, -1)
	if err != nil {
		return nil, err
	}
	for _, asset := range marked {
		key := "marked:" + strconv.FormatInt(asset.ID, 10)
		items = append(items, TrashItem{
			Key: key, Group: key, Source: "marked",
			Name: path.Base(asset.Path), Original: asset.Path, Kind: asset.Kind, Size: asset.Size,
			RemovedAt: asset.MarkedAt, Preview: "/api/media/" + strconv.FormatInt(asset.ID, 10),
			assetID: asset.ID, revision: asset.Revision, favourite: asset.Favourite,
		})
	}

	plans, err := s.binPlansHeld(ctx)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		items = append(items, binPlanItems(plan)...)
	}

	legacy, err := s.legacyHeld(ctx)
	if err != nil {
		return nil, err
	}
	items = append(items, legacyItems(legacy)...)

	shots, err := s.screenshotsHeld(ctx)
	if err != nil {
		return nil, err
	}
	for _, plan := range shots {
		if len(plan.Files) == 0 {
			continue
		}
		item := TrashItem{
			Key: "shot:" + plan.ID, Group: "shot:" + plan.ID, Source: "screenshot",
			Name: path.Base(plan.Files[0].Source), Original: "/screenshots/" + plan.Files[0].Source,
			Kind: mediaKind(plan.Files[0].Source), RemovedAt: plan.Created,
			Preview: "/api/binned-media/shot/" + plan.ID + "/0", planID: plan.ID,
		}
		for _, file := range plan.Files {
			item.Size += file.Size
			if file.Sidecar {
				item.Sidecars++
			}
		}
		items = append(items, item)
	}

	// The sources record time in different layouts, so they are compared as
	// instants. An item with no recorded time sorts last.
	sort.SliceStable(items, func(i, j int) bool {
		return instant(items[i].RemovedAt).After(instant(items[j].RemovedAt))
	})
	return items, nil
}

func instant(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at
		}
	}
	return time.Time{}
}

// binPlansHeld returns the writer batches whose files are in the Bin, including
// one interrupted mid-restore or mid-deletion: the engine resumes those when
// asked again, and hiding them would hide files that are still in the Bin.
func (s *Store) binPlansHeld(ctx context.Context) ([]BinPlan, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT body FROM file_plans WHERE json_extract(body,'$.state') IN ('quarantining','bin','restoring','purging') ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]BinPlan, 0)
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		var plan BinPlan
		if err = json.Unmarshal([]byte(body), &plan); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

// legacyHeld returns every row of the earlier tool's history still in the Bin.
// The history page reads a capped window of all rows; the Bin must see every
// held row however old, or Empty Bin would leave files behind unseen.
func (s *Store) legacyHeld(ctx context.Context) ([]LegacyBinItem, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT legacy_id,batch,kind,original_path,culled_path,size_bytes,culled_at FROM legacy_culled WHERE restored_at IS NULL AND purged_at IS NULL ORDER BY legacy_id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]LegacyBinItem, 0)
	for rows.Next() {
		var item LegacyBinItem
		if err = rows.Scan(&item.ID, &item.Batch, &item.Kind, &item.Original, &item.Stored, &item.Size, &item.CulledAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// screenshotsHeld returns every screenshot removal whose files are in the Bin,
// including one interrupted mid-undo, which the writer resumes when asked again.
func (s *Store) screenshotsHeld(ctx context.Context) ([]ScreenshotPlan, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT body FROM screenshot_plans WHERE json_extract(body,'$.action')='remove' AND json_extract(body,'$.state') IN ('bin','restoring') ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]ScreenshotPlan, 0)
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		var plan ScreenshotPlan
		if err = json.Unmarshal([]byte(body), &plan); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

// binPlanItems turns one writer batch into a card per photograph. Sidecars are
// credited to the photograph whose name they extend.
func binPlanItems(plan BinPlan) []TrashItem {
	items := make([]TrashItem, 0, len(plan.Assets))
	for _, asset := range plan.Assets {
		original := strings.TrimPrefix(asset.Path, "/archive/")
		item := TrashItem{
			Key: "bin:" + plan.ID + ":" + strconv.FormatInt(asset.ID, 10), Group: "bin:" + plan.ID, Source: "bin",
			Name: path.Base(asset.Path), Original: asset.Path, Kind: mediaKind(asset.Path),
			RemovedAt: plan.Created, assetID: asset.ID, planID: plan.ID,
		}
		stem := strings.TrimSuffix(original, path.Ext(original))
		for index, file := range plan.Files {
			switch {
			case file.Original == original:
				item.Size += file.Size
				// A batch interrupted on its way in has files on both sides;
				// each is shown from wherever it is now.
				if file.Phase == "bin" {
					item.Preview = "/api/binned-media/bin/" + plan.ID + "/" + strconv.Itoa(index)
				} else {
					item.Preview = "/api/media/" + strconv.FormatInt(asset.ID, 10)
				}
			case file.Sidecar && (len(plan.Assets) == 1 || strings.HasPrefix(file.Original, stem)):
				item.Size += file.Size
				item.Sidecars++
			}
		}
		items = append(items, item)
	}
	return items
}

// legacyItems folds the imported history's sidecar rows into the photograph of
// the same batch. A batch holding only sidecars, whose photograph has already
// left, is still shown, so nothing in the Bin is invisible.
func legacyItems(rows []LegacyBinItem) []TrashItem {
	type batch struct {
		media    []int
		sidecars []LegacyBinItem
	}
	batches := map[string]*batch{}
	order := make([]string, 0)
	active := make([]LegacyBinItem, 0)
	for _, row := range rows {
		if row.RestoredAt != "" || row.PurgedAt != "" {
			continue
		}
		b, seen := batches[row.Batch]
		if !seen {
			b = &batch{}
			batches[row.Batch] = b
			order = append(order, row.Batch)
		}
		if row.Kind == "sidecar" {
			b.sidecars = append(b.sidecars, row)
		} else {
			b.media = append(b.media, len(active))
		}
		active = append(active, row)
	}
	items := make([]TrashItem, 0)
	for _, name := range order {
		b := batches[name]
		cards := b.media
		loose := b.sidecars
		if len(cards) == 0 {
			// Nothing to hang the sidecars on, so each is a card of its own.
			for _, row := range b.sidecars {
				items = append(items, legacyItem(row, name, "sidecar"))
			}
			continue
		}
		for n, index := range cards {
			row := active[index]
			item := legacyItem(row, name, mediaKind(row.Original))
			item.Preview = "/api/bin-media/" + strconv.FormatInt(row.ID, 10)
			if n == 0 {
				for _, sidecar := range loose {
					item.Size += sidecar.Size
					item.Sidecars++
				}
			}
			items = append(items, item)
		}
	}
	return items
}

func legacyItem(row LegacyBinItem, batch, kind string) TrashItem {
	disk := ""
	if parts := strings.Split(row.Stored, "/"); len(parts) > 2 && parts[1] == "disks" {
		disk = parts[2]
	}
	return TrashItem{
		Key: "legacy:" + strconv.FormatInt(row.ID, 10), Group: "legacy:" + batch, Source: "legacy",
		Name: path.Base(row.Original), Original: row.Original, Kind: kind, Size: row.Size,
		RemovedAt: row.CulledAt, Disk: disk, legacyID: row.ID,
	}
}
