package catalog

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type UpgradeCopy struct {
	Asset     Asset   `json:"asset"`
	Pixels    string  `json:"pixels"`
	Ratio     float64 `json:"ratio"`
	Date      string  `json:"date"`
	Album     string  `json:"album"`
	Available bool    `json:"available"`
}

type UpgradeGroup struct {
	Archive       Asset         `json:"archive"`
	Day           string        `json:"day"`
	Pixels        string        `json:"pixels"`
	Accepted      string        `json:"accepted,omitempty"`
	AcceptedAsset *Asset        `json:"acceptedAsset,omitempty"`
	Copies        []UpgradeCopy `json:"copies"`
}

type UpgradePage struct {
	Groups   []UpgradeGroup `json:"groups"`
	Total    int            `json:"total"`
	Pending  int            `json:"pending"`
	Accepted int            `json:"accepted"`
	Bytes    int64          `json:"bytes"`
}

func mappedPath(value, hostPrefix, logicalPrefix string) (string, bool) {
	clean := filepath.Clean(value)
	host := filepath.Clean(hostPrefix)
	if clean != host && !strings.HasPrefix(clean, host+string(filepath.Separator)) {
		return "", false
	}
	rel := strings.TrimPrefix(clean, host)
	logical := path.Clean(logicalPrefix + filepath.ToSlash(rel))
	return logical, logical != logicalPrefix && strings.HasPrefix(logical, logicalPrefix+"/")
}

func mediaKind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mov", ".mp4", ".m4v", ".avi", ".mkv", ".3gp", ".mpg", ".mpeg":
		return "video"
	case ".arw", ".dng", ".cr2", ".nef", ".raf", ".orf":
		return "raw"
	default:
		return "image"
	}
}

// ImportUpgradeReport replaces only derived candidate rows. Decisions, accepted
// receipts and archive assets remain untouched.
func (s *Store) ImportUpgradeReport(ctx context.Context, input io.Reader, upgradesRoot, sourcePrefix, archivePrefix string) (int, error) {
	if upgradesRoot == "" || upgradesRoot == "/" || !filepath.IsAbs(upgradesRoot) {
		return 0, ErrInvalid
	}
	reader := csv.NewReader(input)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	if _, err := reader.Read(); err != nil {
		return 0, err
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM upgrade_candidates"); err != nil {
		return 0, err
	}
	count := 0
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
		if len(record) < 9 || record[0] != "CONFIRMED" {
			continue
		}
		ratio, parseErr := strconv.ParseFloat(record[4], 64)
		if parseErr != nil || ratio <= 1 {
			return 0, fmt.Errorf("invalid upgrade ratio")
		}
		sourcePath, ok := mappedPath(record[7], sourcePrefix, "/upgrades")
		if !ok {
			return 0, fmt.Errorf("upgrade source outside configured staging tree")
		}
		archivePath, ok := mappedPath(record[8], archivePrefix, "/archive")
		if !ok {
			return 0, fmt.Errorf("upgrade target outside configured archive")
		}
		var archiveID, capturedAt int64
		var archiveKind string
		if err = tx.QueryRowContext(ctx, "SELECT id,captured_at,kind FROM assets WHERE source_id='archive' AND relative_path=?", archivePath).Scan(&archiveID, &capturedAt, &archiveKind); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return 0, err
		}
		available := 0
		sourceSize := int64(0)
		containerPath := filepath.Join(upgradesRoot, filepath.FromSlash(strings.TrimPrefix(sourcePath, "/upgrades/")))
		if info, statErr := os.Stat(containerPath); statErr == nil && info.Mode().IsRegular() {
			available = 1
			sourceSize = info.Size()
		}
		if parsed, dateErr := time.ParseInLocation("2006:01:02 15:04:05", record[1], time.Local); dateErr == nil {
			capturedAt = parsed.Unix()
		}
		kind := mediaKind(sourcePath)
		if kind == "image" && archiveKind == "raw" {
			kind = archiveKind
		}
		var sourceID int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id,anchor_id)
			VALUES(?,?,?,?, 'takeout',?) ON CONFLICT(source_id,relative_path) DO UPDATE SET captured_at=excluded.captured_at,kind=excluded.kind,size_bytes=excluded.size_bytes,anchor_id=excluded.anchor_id RETURNING id`, sourcePath, capturedAt, kind, sourceSize, archiveID).Scan(&sourceID); err != nil {
			return 0, err
		}
		album := path.Base(path.Dir(sourcePath))
		if _, err = tx.ExecContext(ctx, `INSERT INTO upgrade_candidates(archive_asset_id,source_asset_id,capture_date,archive_day,ratio,source_pixels,archive_pixels,album,source_available)
			VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(archive_asset_id,source_asset_id) DO UPDATE SET capture_date=excluded.capture_date,archive_day=excluded.archive_day,ratio=excluded.ratio,source_pixels=excluded.source_pixels,archive_pixels=excluded.archive_pixels,album=excluded.album,source_available=excluded.source_available`, archiveID, sourceID, record[1], record[2], ratio, record[5], record[6], album, available); err != nil {
			return 0, err
		}
		count++
	}
	if count == 0 {
		return 0, fmt.Errorf("no confirmed upgrade rows imported")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('upgrades_indexed_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func (s *Store) Upgrades(ctx context.Context) (UpgradePage, error) {
	page := UpgradePage{Groups: make([]UpgradeGroup, 0)}
	if err := s.read.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(size_bytes),0) FROM upgrade_history").Scan(&page.Accepted, &page.Bytes); err != nil {
		return page, err
	}
	rows, err := s.read.QueryContext(ctx, `SELECT
		a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,
		(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),(SELECT count(*) FROM related_assets rel WHERE rel.asset_id=a.id),
		c.archive_day,c.archive_pixels,COALESCE(h.accepted_as,''),
		s.id,s.relative_path,s.captured_at,s.kind,s.size_bytes,COALESCE(sd.status,'unreviewed'),COALESCE(sd.favourite,0),COALESCE(sd.revision,0),s.source_id,
		(SELECT count(*) FROM assets alt WHERE alt.anchor_id=s.id),(SELECT count(*) FROM related_assets rel WHERE rel.asset_id=s.id),
		c.source_pixels,c.ratio,c.capture_date,c.album,c.source_available
		FROM upgrade_candidates c
		JOIN assets a ON a.id=c.archive_asset_id JOIN assets s ON s.id=c.source_asset_id
		LEFT JOIN decisions d ON d.asset_id=a.id LEFT JOIN decisions sd ON sd.asset_id=s.id
		LEFT JOIN upgrade_history h ON h.archive_file=a.relative_path
		ORDER BY c.ratio DESC,a.id,s.id`)
	if err != nil {
		return page, err
	}
	indices := map[int64]int{}
	for rows.Next() {
		var archive, source Asset
		var day, archivePixels, accepted, sourcePixels, captureDate, album string
		var ratio float64
		var available bool
		values := []any{&archive.ID, &archive.Path, &archive.CapturedAt, &archive.Kind, &archive.Size, &archive.Status, &archive.Favourite, &archive.Revision, &archive.Source, &archive.AlternativeCount, &archive.RelatedCount, &day, &archivePixels, &accepted, &source.ID, &source.Path, &source.CapturedAt, &source.Kind, &source.Size, &source.Status, &source.Favourite, &source.Revision, &source.Source, &source.AlternativeCount, &source.RelatedCount, &sourcePixels, &ratio, &captureDate, &album, &available}
		if err = rows.Scan(values...); err != nil {
			return page, err
		}
		index, found := indices[archive.ID]
		if !found {
			index = len(page.Groups)
			indices[archive.ID] = index
			page.Groups = append(page.Groups, UpgradeGroup{Archive: archive, Day: day, Pixels: archivePixels, Accepted: accepted, Copies: make([]UpgradeCopy, 0)})
			page.Total++
			if accepted == "" {
				page.Pending++
			}
		}
		page.Groups[index].Copies = append(page.Groups[index].Copies, UpgradeCopy{Asset: source, Pixels: sourcePixels, Ratio: ratio, Date: captureDate, Album: album, Available: available})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return page, err
	}
	if err = rows.Close(); err != nil {
		return page, err
	}
	for index := range page.Groups {
		group := &page.Groups[index]
		if group.Accepted == "" {
			continue
		}
		var asset Asset
		err = s.read.QueryRowContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,
			(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),(SELECT count(*) FROM related_assets rel WHERE rel.asset_id=a.id)
			FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id WHERE a.source_id='archive' AND a.relative_path=?`, group.Accepted).Scan(&asset.ID, &asset.Path, &asset.CapturedAt, &asset.Kind, &asset.Size, &asset.Status, &asset.Favourite, &asset.Revision, &asset.Source, &asset.AlternativeCount, &asset.RelatedCount)
		if err == nil {
			group.AcceptedAsset = &asset
		} else if err != sql.ErrNoRows {
			return page, err
		}
	}
	return page, nil
}
