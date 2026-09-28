package catalog

import (
	"context"
	"database/sql"
	"strings"
)

// A photo or video can be turned in Cull a quarter at a time, when it was
// taken with the phone held sideways and no tag says so. The turn is Cull's
// own: it is saved in the catalogue and shown wherever Cull shows the file,
// and the file in the archive is left exactly as it is.

// maxTurns is how many files one request may turn, a whole selection.
const maxTurns = 500

// Turn turns each file by quarters, clockwise when positive, and returns
// where each ended up, 0 to 3.
func (s *Store) Turn(ctx context.Context, ids []int64, quarters int) (map[int64]int, error) {
	if len(ids) == 0 || len(ids) > maxTurns || quarters == 0 || quarters < -3 || quarters > 3 {
		return nil, ErrInvalid
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	turned := make(map[int64]int, len(ids))
	for _, id := range ids {
		var current sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT t.quarters FROM assets a LEFT JOIN asset_turns t ON t.asset_id=a.id WHERE a.id=?`, id).Scan(&current)
		if err == sql.ErrNoRows {
			return nil, ErrInvalid
		}
		if err != nil {
			return nil, err
		}
		next := ((int(current.Int64)+quarters)%4 + 4) % 4
		if next == 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM asset_turns WHERE asset_id=?`, id)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO asset_turns(asset_id,quarters) VALUES(?,?)
				ON CONFLICT(asset_id) DO UPDATE SET quarters=excluded.quarters, turned_at=CURRENT_TIMESTAMP`, id, next)
		}
		if err != nil {
			return nil, err
		}
		turned[id] = next
	}
	return turned, tx.Commit()
}

// markTurns sets Turn on the files the reviewer turned.
func (s *Store) markTurns(ctx context.Context, byID map[int64]*Asset, ids []any, marks []string) error {
	rows, err := s.read.QueryContext(ctx, `SELECT asset_id,quarters FROM asset_turns WHERE asset_id IN (`+strings.Join(marks, ",")+`)`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var quarters int
		if err = rows.Scan(&id, &quarters); err != nil {
			return err
		}
		if a := byID[id]; a != nil {
			a.Turn = quarters
		}
	}
	return rows.Err()
}
