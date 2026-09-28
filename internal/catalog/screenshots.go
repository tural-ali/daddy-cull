package catalog

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var screenshotMediaExtensions = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "heic": true, "heif": true, "tif": true, "tiff": true,
	"mov": true, "mp4": true, "m4v": true, "avi": true, "mkv": true, "3gp": true, "mpg": true, "mpeg": true,
	"arw": true, "dng": true, "cr2": true, "nef": true, "raf": true, "orf": true,
}

type ScreenshotImportResult struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// ScreenshotItem is a screenshot or screen recording waiting in the holding
// area. Its status is keep once it has been reviewed.
type ScreenshotItem struct {
	Asset
	// Day is the date the file's name starts with, as YYYY-MM-DD before an
	// underscore, or empty when it starts with none.
	Day string `json:"day"`
	// Name is the file's name in the holding area.
	Name string `json:"name"`
	// State is where it stands in the holding area; always waiting in this
	// list.
	State string `json:"state"`
}

// ScreenshotPage is one page of the holding area under one filter. Total and
// Bytes are for that filter. For the filters' counts, Unreviewed and Reviewed
// count both sides of it within its kind, and Stills and Recordings count
// each kind within its review side.
type ScreenshotPage struct {
	// Items are this page's screenshots, ordered by day and then name; those
	// with no day come first.
	Items []ScreenshotItem `json:"items"`
	// Total counts the screenshots under the filter, on every page.
	Total int `json:"total"`
	// Bytes is the size of those screenshots together, in bytes.
	Bytes int64 `json:"bytes"`
	// Unreviewed counts the screenshots of the filter's kind not reviewed yet.
	Unreviewed int `json:"unreviewed"`
	// Reviewed counts the screenshots of the filter's kind already kept.
	Reviewed int `json:"reviewed"`
	// Stills counts the pictures on the filter's review side.
	Stills int `json:"stills"`
	// Recordings counts the screen recordings on the filter's review side.
	Recordings int `json:"recordings"`
}

// A screenshot is reviewed once it has been kept: a decision only, so nothing
// moves on disk and it stays in the holding area under Reviewed.
const screenshotReviewed = "COALESCE(d.status,'unreviewed')='keep'"

type screenshotFile struct {
	path, day, name string
	size, mtime     int64
}

// ImportScreenshotDirectory reads directory entries and stat metadata only.
// It never opens media content and preserves prior keep or Bin outcomes.
func (s *Store) ImportScreenshotDirectory(ctx context.Context, root string) (ScreenshotImportResult, error) {
	result := ScreenshotImportResult{}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return result, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return result, err
	}
	files := make([]screenshotFile, 0)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		extension := strings.ToLower(strings.TrimPrefix(path.Ext(entry.Name()), "."))
		if !screenshotMediaExtensions[extension] {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			if statErr != nil {
				return result, statErr
			}
			continue
		}
		logicalPath := "/screenshots/" + entry.Name()
		day, _ := archiveDay(logicalPath, 0)
		files = append(files, screenshotFile{path: logicalPath, day: day, name: entry.Name(), size: info.Size(), mtime: info.ModTime().Unix()})
		result.Files++
		result.Bytes += info.Size()
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_items SET state='missing' WHERE state='waiting'"); err != nil {
		return result, err
	}
	for _, file := range files {
		captured := int64(0)
		if file.day != "" {
			if parsed, parseErr := time.Parse("2006-01-02", file.day); parseErr == nil {
				captured = parsed.Unix()
			}
		}
		assetID, insertErr := upsertExternalAsset(ctx, tx, "screenshots", file.path, file.size, captured)
		if insertErr != nil {
			return result, insertErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO screenshot_items(asset_id,path,day,name,size_bytes,mtime,state) VALUES(?,?,?,?,?,?,'waiting') ON CONFLICT(path) DO UPDATE SET day=excluded.day,name=excluded.name,size_bytes=excluded.size_bytes,mtime=excluded.mtime,state=CASE WHEN screenshot_items.state='missing' THEN 'waiting' ELSE screenshot_items.state END`, assetID, file.path, nullable(file.day), file.name, file.size, file.mtime); err != nil {
			return result, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('screenshots_indexed_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) ScreenshotBacklog(ctx context.Context, kind string, limit int) ([]ScreenshotItem, error) {
	page, err := s.ScreenshotPage(ctx, kind, "all", 0, limit)
	return page.Items, err
}

// ScreenshotPage lists waiting screenshots. review is "" for those not yet
// reviewed, "reviewed" for those kept, or "all".
func (s *Store) ScreenshotPage(ctx context.Context, kind, review string, from, limit int) (ScreenshotPage, error) {
	page := ScreenshotPage{Items: make([]ScreenshotItem, 0)}
	if kind != "" && kind != "image" && kind != "video" {
		return page, ErrInvalid
	}
	if review != "" && review != "reviewed" && review != "all" {
		return page, ErrInvalid
	}
	if from < 0 || limit < 1 || limit > 200 {
		return page, ErrInvalid
	}
	where := " WHERE shots.state='waiting'"
	args := make([]any, 0, 3)
	if kind != "" {
		where += " AND a.kind=?"
		args = append(args, kind)
	}
	var reviewedBytes, unreviewedBytes int64
	if err := s.read.QueryRowContext(ctx, `SELECT COALESCE(sum(`+screenshotReviewed+`),0),COALESCE(sum(CASE WHEN `+screenshotReviewed+` THEN a.size_bytes END),0),
		COALESCE(sum(NOT `+screenshotReviewed+`),0),COALESCE(sum(CASE WHEN NOT `+screenshotReviewed+` THEN a.size_bytes END),0)
		FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id JOIN screenshot_items shots ON shots.asset_id=a.id`+where, args...).Scan(&page.Reviewed, &reviewedBytes, &page.Unreviewed, &unreviewedBytes); err != nil {
		return page, err
	}
	side := ""
	switch review {
	case "":
		side = " AND NOT " + screenshotReviewed
		page.Total, page.Bytes = page.Unreviewed, unreviewedBytes
	case "reviewed":
		side = " AND " + screenshotReviewed
		page.Total, page.Bytes = page.Reviewed, reviewedBytes
	default:
		page.Total, page.Bytes = page.Reviewed+page.Unreviewed, reviewedBytes+unreviewedBytes
	}
	where += side
	if err := s.read.QueryRowContext(ctx, `SELECT COALESCE(sum(a.kind='image'),0),COALESCE(sum(a.kind='video'),0)
		FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id JOIN screenshot_items shots ON shots.asset_id=a.id WHERE shots.state='waiting'`+side).Scan(&page.Stills, &page.Recordings); err != nil {
		return page, err
	}
	query := `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),` + relatedCount + `,COALESCE(shots.day,''),shots.name,shots.state FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id JOIN screenshot_items shots ON shots.asset_id=a.id` + where
	query += " ORDER BY COALESCE(shots.day,''),shots.name LIMIT ? OFFSET ?"
	args = append(args, limit, from)
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ScreenshotItem
		if err = rows.Scan(&item.Asset.ID, &item.Asset.Path, &item.Asset.CapturedAt, &item.Asset.Kind, &item.Asset.Size, &item.Asset.Status, &item.Asset.Favourite, &item.Asset.Revision, &item.Asset.Source, &item.Asset.AlternativeCount, &item.Asset.RelatedCount, &item.Day, &item.Name, &item.State); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	assets := make([]*Asset, len(page.Items))
	for i := range page.Items {
		assets[i] = &page.Items[i].Asset
	}
	return page, s.markShapes(ctx, assets)
}
