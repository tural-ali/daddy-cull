package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func legacyRequestID(path, timestamp string) string {
	sum := sha256.Sum256([]byte(path + "\x00" + timestamp))
	return "legacy-" + hex.EncodeToString(sum[:16])
}

// ImportLegacyDatabase copies durable user state from a consistent read-only
// snapshot. Existing Go decisions win; a legacy favourite is merged without
// clearing a newer status, and every imported decision remains auditable.
func (s *Store) ImportLegacyDatabase(ctx context.Context, source string) error {
	abs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: abs}
	legacy, err := sql.Open("sqlite3", u.String()+"?mode=ro&_query_only=on&_busy_timeout=3000")
	if err != nil {
		return err
	}
	defer legacy.Close()
	for _, table := range []string{"days", "review", "culled", "shadows", "shot_suspects", "upgrades_accepted"} {
		var found int
		if err = legacy.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&found); err != nil || found != 1 {
			return fmt.Errorf("legacy database is missing %s", table)
		}
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	dayRows, err := legacy.QueryContext(ctx, "SELECT day,status,reviewed_at FROM days WHERE status='done'")
	if err != nil {
		return err
	}
	for dayRows.Next() {
		var day, status string
		var reviewed sql.NullString
		if err = dayRows.Scan(&day, &status, &reviewed); err != nil {
			dayRows.Close()
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO day_progress(day,status,reviewed_at) VALUES(?,?,?) ON CONFLICT(day) DO NOTHING", day, status, nullString(reviewed)); err != nil {
			dayRows.Close()
			return err
		}
	}
	if err = dayRows.Close(); err != nil {
		return err
	}

	reviewRows, err := legacy.QueryContext(ctx, "SELECT path,favourite,reviewed_at,decision FROM review")
	if err != nil {
		return err
	}
	for reviewRows.Next() {
		var path string
		var favourite bool
		var reviewed, legacyDecision sql.NullString
		if err = reviewRows.Scan(&path, &favourite, &reviewed, &legacyDecision); err != nil {
			reviewRows.Close()
			return err
		}
		var assetID int64
		if err = tx.QueryRowContext(ctx, "SELECT id FROM assets WHERE source_id='archive' AND relative_path=?", path).Scan(&assetID); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			reviewRows.Close()
			return err
		}
		var currentStatus string
		var currentFavourite bool
		var revision int64
		err = tx.QueryRowContext(ctx, "SELECT status,favourite,revision FROM decisions WHERE asset_id=?", assetID).Scan(&currentStatus, &currentFavourite, &revision)
		created := reviewed.String
		if created == "" {
			created = time.Now().UTC().Format(time.RFC3339)
		}
		if err == sql.ErrNoRows {
			status := "unreviewed"
			if reviewed.Valid || legacyDecision.String != "" {
				status = "keep"
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(?,?,?,1)", assetID, status, favourite); err != nil {
				reviewRows.Close()
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite,created_at) VALUES(?,?,0,?,?,?,0,?)", legacyRequestID(path, created), assetID, status, favourite, "unreviewed", created); err != nil {
				reviewRows.Close()
				return err
			}
		} else if err != nil {
			reviewRows.Close()
			return err
		} else if favourite && !currentFavourite {
			if _, err = tx.ExecContext(ctx, "UPDATE decisions SET favourite=1,revision=revision+1 WHERE asset_id=?", assetID); err != nil {
				reviewRows.Close()
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite,created_at) VALUES(?,?,?,?,?,?,?,?)", legacyRequestID(path, created), assetID, revision, currentStatus, true, currentStatus, currentFavourite, created); err != nil {
				reviewRows.Close()
				return err
			}
		}
	}
	if err = reviewRows.Close(); err != nil {
		return err
	}

	if err = copyLegacyCulled(ctx, legacy, tx); err != nil {
		return err
	}
	if err = copyLegacyShadows(ctx, legacy, tx); err != nil {
		return err
	}
	if err = copyLegacyScreenshots(ctx, legacy, tx); err != nil {
		return err
	}
	if err = copyLegacyUpgrades(ctx, legacy, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('legacy_imported_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return err
	}
	return tx.Commit()
}

func copyLegacyCulled(ctx context.Context, legacy *sql.DB, target *sql.Tx) error {
	rows, err := legacy.QueryContext(ctx, "SELECT id,batch,kind,original_path,culled_path,day,size,reason,culled_at,restored_at,purged_at,photos_deleted_at FROM culled")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, size int64
		var batch, kind, original, culled, at string
		var day, reason, restored, purged, photosDeleted sql.NullString
		if err = rows.Scan(&id, &batch, &kind, &original, &culled, &day, &size, &reason, &at, &restored, &purged, &photosDeleted); err != nil {
			return err
		}
		if _, err = target.ExecContext(ctx, "INSERT OR IGNORE INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,day,size_bytes,reason,culled_at,restored_at,purged_at,photos_deleted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", id, batch, kind, original, culled, nullString(day), size, nullString(reason), at, nullString(restored), nullString(purged), nullString(photosDeleted)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func copyLegacyShadows(ctx context.Context, legacy *sql.DB, target *sql.Tx) error {
	rows, err := legacy.QueryContext(ctx, "SELECT kind,group_key,disk,relpath,size,mtime FROM shadows")
	if err != nil {
		return err
	}
	for rows.Next() {
		var kind, group, disk, relative string
		var size, mtime int64
		if err = rows.Scan(&kind, &group, &disk, &relative, &size, &mtime); err != nil {
			return err
		}
		assetPath := "/disks/" + disk + "/" + relative
		assetID, insertErr := upsertExternalAsset(ctx, target, "shadow", assetPath, size, mtime)
		if insertErr != nil {
			return insertErr
		}
		if _, err = target.ExecContext(ctx, "INSERT OR IGNORE INTO shadow_entries(kind,group_key,disk,relative_path,size_bytes,mtime,asset_id) VALUES(?,?,?,?,?,?,?)", kind, group, disk, relative, size, mtime, assetID); err != nil {
			return err
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	hashRows, err := legacy.QueryContext(ctx, "SELECT path,size,mtime,psig,md5,hashed_at FROM hashes WHERE path LIKE '/disks/%' AND (psig IS NOT NULL OR md5 IS NOT NULL)")
	if err != nil {
		return err
	}
	defer hashRows.Close()
	for hashRows.Next() {
		var assetPath, hashedAt string
		var size, mtime int64
		var signature, fullHash sql.NullString
		if err = hashRows.Scan(&assetPath, &size, &mtime, &signature, &fullHash, &hashedAt); err != nil {
			return err
		}
		var assetID int64
		if err = target.QueryRowContext(ctx, "SELECT id FROM assets WHERE source_id='shadow' AND relative_path=? AND size_bytes=?", assetPath, size).Scan(&assetID); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return err
		}
		if _, err = target.ExecContext(ctx, "INSERT INTO asset_evidence(asset_id,mtime,partial_signature,full_hash,hashed_at) VALUES(?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET mtime=excluded.mtime,partial_signature=excluded.partial_signature,full_hash=excluded.full_hash,hashed_at=excluded.hashed_at", assetID, mtime, nullString(signature), nullString(fullHash), hashedAt); err != nil {
			return err
		}
	}
	return hashRows.Err()
}

func copyLegacyScreenshots(ctx context.Context, legacy *sql.DB, target *sql.Tx) error {
	rows, err := legacy.QueryContext(ctx, "SELECT path,day,name,size,width,height,reason,found_at,verdict,decided_at,moved_to FROM shot_suspects")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path, name, reason, found string
		var day, verdict, decided, moved sql.NullString
		var size int64
		var width, height sql.NullInt64
		if err = rows.Scan(&path, &day, &name, &size, &width, &height, &reason, &found, &verdict, &decided, &moved); err != nil {
			return err
		}
		captured := int64(0)
		if day.Valid {
			if parsed, parseErr := time.Parse("2006-01-02", day.String); parseErr == nil {
				captured = parsed.Unix()
			}
		}
		assetID, insertErr := upsertExternalAsset(ctx, target, "screenshots", path, size, captured)
		if insertErr != nil {
			return insertErr
		}
		if _, err = target.ExecContext(ctx, "INSERT OR IGNORE INTO screenshot_suspects(path,day,name,size_bytes,width,height,reason,found_at,verdict,decided_at,moved_to,asset_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", path, nullString(day), name, size, nullInt(width), nullInt(height), reason, found, nullString(verdict), nullString(decided), nullString(moved), assetID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func copyLegacyUpgrades(ctx context.Context, legacy *sql.DB, target *sql.Tx) error {
	rows, err := legacy.QueryContext(ctx, "SELECT archive_file,source_file,accepted_as,bytes,accepted_at,via FROM upgrades_accepted")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var archive, source, accepted, at string
		var size int64
		var via sql.NullString
		if err = rows.Scan(&archive, &source, &accepted, &size, &at, &via); err != nil {
			return err
		}
		if _, err = target.ExecContext(ctx, "INSERT OR IGNORE INTO upgrade_history(archive_file,source_file,accepted_as,size_bytes,accepted_at,via) VALUES(?,?,?,?,?,?)", archive, source, accepted, size, at, nullString(via)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func nullString(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullInt(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func upsertExternalAsset(ctx context.Context, target *sql.Tx, source, assetPath string, size, captured int64) (int64, error) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(assetPath), "."))
	kind := "image"
	if map[string]bool{"mov": true, "mp4": true, "m4v": true, "avi": true, "mkv": true, "3gp": true, "mpg": true, "mpeg": true}[ext] {
		kind = "video"
	} else if map[string]bool{"arw": true, "dng": true, "cr2": true, "nef": true, "raf": true, "orf": true}[ext] {
		kind = "raw"
	}
	if _, err := target.ExecContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,?,?) ON CONFLICT(source_id,relative_path) DO UPDATE SET captured_at=excluded.captured_at,kind=excluded.kind,size_bytes=excluded.size_bytes", assetPath, captured, kind, size, source); err != nil {
		return 0, err
	}
	var id int64
	err := target.QueryRowContext(ctx, "SELECT id FROM assets WHERE source_id=? AND relative_path=?", source, assetPath).Scan(&id)
	return id, err
}
