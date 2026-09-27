package catalog

import (
	"context"
	"database/sql"
	"path"
	"strings"
)

// A camera that shoots RAW+JPEG writes two files of one exposure side by side:
// A7404251.ARW and A7404251.JPG. The day page shows such a pair as one photo,
// the JPEG with the RAW behind it, and a choice on it applies to both. The
// rule is deliberately strict so it never joins two different pictures: the
// same folder, the same name before the extension, and exactly one RAW and one
// JPEG or HEIC. Anything looser (an edited copy, a "(1)" duplicate) stays a
// "similar photo" to compare, never a pair.
//
// The reviewer can split a wrong pair. Splits live in their own table, which
// the index never rewrites, so a rescan cannot join them again.

var pairPartners = map[string]bool{".jpg": true, ".jpeg": true, ".heic": true, ".heif": true}

// rawPairKey is the folder and the file name before its extension, ignoring
// case, or "" for a file that cannot be half of a pair.
func rawPairKey(p, kind string) string {
	ext := strings.ToLower(path.Ext(p))
	if kind != "raw" && !(kind == "image" && pairPartners[ext]) {
		return ""
	}
	base := path.Base(p)
	return path.Dir(p) + "/" + strings.ToLower(strings.TrimSuffix(base, path.Ext(base)))
}

func indexRawPairs(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,relative_path,kind FROM assets WHERE kind IN ('raw','image') AND anchor_id IS NULL AND id NOT IN (SELECT asset_id FROM missing_assets)")
	if err != nil {
		return err
	}
	type halves struct{ raws, partners []int64 }
	byKey := map[string]*halves{}
	for rows.Next() {
		var id int64
		var p, kind string
		if err = rows.Scan(&id, &p, &kind); err != nil {
			rows.Close()
			return err
		}
		key := rawPairKey(p, kind)
		if key == "" {
			continue
		}
		h := byKey[key]
		if h == nil {
			h = &halves{}
			byKey[key] = h
		}
		if kind == "raw" {
			h.raws = append(h.raws, id)
		} else {
			h.partners = append(h.partners, id)
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM raw_pairs"); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO raw_pairs(raw_id,partner_id) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range byKey {
		if len(h.raws) != 1 || len(h.partners) != 1 {
			continue
		}
		if _, err = stmt.ExecContext(ctx, h.raws[0], h.partners[0]); err != nil {
			return err
		}
	}
	return nil
}

// markPairs sets Pair on both halves of every pair among the given files that
// has not been split. A half that is not in the list (already in the Bin, or
// on another page) leaves the other showing alone.
func (s *Store) markPairs(ctx context.Context, assets []*Asset) error {
	if len(assets) == 0 {
		return nil
	}
	byID := make(map[int64]*Asset, len(assets))
	ids := make([]any, 0, len(assets))
	marks := make([]string, 0, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
		ids = append(ids, a.ID)
		marks = append(marks, "?")
	}
	rows, err := s.read.QueryContext(ctx, `SELECT p.raw_id,p.partner_id FROM raw_pairs p
		WHERE p.raw_id IN (`+strings.Join(marks, ",")+`)
		AND NOT EXISTS (SELECT 1 FROM raw_pair_splits sp WHERE sp.raw_id=p.raw_id AND sp.partner_id=p.partner_id)`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw, partner int64
		if err = rows.Scan(&raw, &partner); err != nil {
			return err
		}
		if byID[raw] != nil && byID[partner] != nil {
			byID[raw].Pair, byID[partner].Pair = partner, raw
		}
	}
	return rows.Err()
}

// SetPaired splits a pair (paired false) or joins it again (true). Only a pair
// the index found can be split or joined, in either order of its halves.
func (s *Store) SetPaired(ctx context.Context, a, b int64, paired bool) error {
	var raw, partner int64
	err := s.read.QueryRowContext(ctx, "SELECT raw_id,partner_id FROM raw_pairs WHERE (raw_id=? AND partner_id=?) OR (raw_id=? AND partner_id=?)", a, b, b, a).Scan(&raw, &partner)
	if err == sql.ErrNoRows {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if paired {
		_, err = s.write.ExecContext(ctx, "DELETE FROM raw_pair_splits WHERE raw_id=? AND partner_id=?", raw, partner)
	} else {
		_, err = s.write.ExecContext(ctx, "INSERT OR IGNORE INTO raw_pair_splits(raw_id,partner_id) VALUES(?,?)", raw, partner)
	}
	return err
}
