package catalog

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

type ImportEvidenceRow struct {
	Path             string `json:"path"`
	Source           string `json:"source"`
	Size             int64  `json:"size"`
	MTime            int64  `json:"mtime"`
	PartialSignature string `json:"partialSignature"`
	FullHash         string `json:"fullHash"`
	PerceptualHash   string `json:"perceptualHash"`
	HashedAt         string `json:"hashedAt"`
}

func validHex(value string, bytes int) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytes
}

// ImportEvidence enriches an existing catalogue from a read-only legacy export.
// It matches immutable source paths and sizes, and commits all rows or none.
func (s *Store) ImportEvidence(ctx context.Context, input io.Reader) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 65536), 1048576)
	count := 0
	skipped := 0
	for scanner.Scan() {
		var row ImportEvidenceRow
		if err = json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return err
		}
		root := "/archive/"
		if row.Source == "takeout" {
			root = "/upgrades/"
		} else if row.Source != "archive" {
			return ErrInvalid
		}
		if !strings.HasPrefix(row.Path, root) || path.Clean(row.Path) != row.Path || row.Size < 0 || row.MTime < 0 || !validHex(row.PartialSignature, 16) || !validHex(row.FullHash, 16) || !validHex(row.PerceptualHash, 8) || (row.PartialSignature == "" && row.FullHash == "" && row.PerceptualHash == "") {
			return ErrInvalid
		}
		if row.HashedAt != "" {
			if _, parseErr := time.Parse(time.RFC3339, row.HashedAt); parseErr != nil {
				return ErrInvalid
			}
		}
		var assetID, actualSize int64
		if err = tx.QueryRowContext(ctx, "SELECT id,size_bytes FROM assets WHERE source_id=? AND relative_path=?", row.Source, row.Path).Scan(&assetID, &actualSize); err != nil {
			if err == sql.ErrNoRows {
				skipped++
				continue
			}
			return err
		}
		if actualSize != row.Size {
			skipped++
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_evidence(asset_id,mtime,partial_signature,full_hash,perceptual_hash,hashed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET mtime=excluded.mtime,partial_signature=excluded.partial_signature,full_hash=excluded.full_hash,perceptual_hash=excluded.perceptual_hash,hashed_at=excluded.hashed_at`, assetID, row.MTime, nullable(row.PartialSignature), nullable(row.FullHash), nullable(row.PerceptualHash), nullable(row.HashedAt)); err != nil {
			return err
		}
		count++
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("empty evidence import refused")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('evidence_imported_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('evidence_skipped',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", fmt.Sprint(skipped)); err != nil {
		return err
	}
	return tx.Commit()
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
