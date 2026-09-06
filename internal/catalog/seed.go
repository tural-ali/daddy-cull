package catalog

import (
	"context"
	"fmt"
)

// Seed creates metadata only. It never opens or creates media files.
// Batches bound writer lock time and RAM. An interrupted seed stays browseable;
// use a new database for a fresh benchmark rather than overwriting decisions.
func (s *Store) Seed(ctx context.Context, n int) error {
	if n < 1 || n > 10000000 {
		return fmt.Errorf("seed count must be 1..10000000")
	}
	var existing int
	if err := s.write.QueryRowContext(ctx, "SELECT count(*) FROM assets").Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return fmt.Errorf("seed requires an empty database")
	}
	for start := 1; start <= n; start += 1000 {
		end := min(start+999, n)
		tx, err := s.write.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id,anchor_id) VALUES(?,?,?,?,?,?,?)")
		if err != nil {
			tx.Rollback()
			return err
		}
		for i := start; i <= end; i++ {
			kind, ext := "image", "heic"
			if i%5 == 0 {
				kind, ext = "video", "mov"
			} else if i%7 == 0 {
				kind, ext = "raw", "arw"
			}
			// Four assets per timestamp exercise tie-breaking in every page.
			source := "archive"
			var anchor any
			if i%4 == 0 {
				source = "takeout"
				if i%40 != 0 {
					anchor = i - 1
				}
			}
			_, err = stmt.ExecContext(ctx, i, fmt.Sprintf("synthetic/%09d.%s", i, ext), 1577836800+i/4, kind, 1000000+i%50000000, source, anchor)
			if err != nil {
				stmt.Close()
				tx.Rollback()
				return err
			}
		}
		stmt.Close()
		if _, err = tx.ExecContext(ctx, "UPDATE stats SET total=total+? WHERE id=1", end-start+1); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	_, err := s.write.ExecContext(ctx, "ANALYZE")
	return err
}
