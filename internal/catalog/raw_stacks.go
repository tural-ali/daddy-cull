package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"sort"
	"strings"
)

// A camera that shoots RAW+JPEG writes two files of one exposure side by side,
// A7404251.ARW and A7404251.JPG, and an editor that exports a RAW writes its
// JPEG, HEIC or TIFF next to it under the same name. The day page shows such a
// stack as one photo, an export with the RAW and the other exports behind it,
// and a choice on it applies to every file: a RAW is never kept without its
// exports, or its exports without it. The rule is deliberately strict so it
// never joins two different pictures: the same folder, the same name before
// the extension, exactly one RAW and at least one JPEG, HEIC or TIFF. Anything
// looser (an edited copy, a "(1)" duplicate, a PNG screenshot) stays a
// "similar photo" to compare, never part of a stack.
//
// Settings can show the files of a stack separately instead. The reviewer can
// also take one export out of a wrong stack. Splits live in their own table,
// which the index never rewrites, so a rescan cannot join them again.

var stackExports = map[string]bool{".jpg": true, ".jpeg": true, ".heic": true, ".heif": true, ".tif": true, ".tiff": true}

// rawStackKey is the folder and the file name before its extension, ignoring
// case, or "" for a file that cannot be part of a stack.
func rawStackKey(p, kind string) string {
	ext := strings.ToLower(path.Ext(p))
	if kind != "raw" && !(kind == "image" && stackExports[ext]) {
		return ""
	}
	base := path.Base(p)
	return path.Dir(p) + "/" + strings.ToLower(strings.TrimSuffix(base, path.Ext(base)))
}

func indexRawStacks(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,relative_path,kind FROM assets WHERE kind IN ('raw','image') AND anchor_id IS NULL AND id NOT IN (SELECT asset_id FROM missing_assets)")
	if err != nil {
		return err
	}
	type files struct{ raws, exports []int64 }
	byKey := map[string]*files{}
	for rows.Next() {
		var id int64
		var p, kind string
		if err = rows.Scan(&id, &p, &kind); err != nil {
			rows.Close()
			return err
		}
		key := rawStackKey(p, kind)
		if key == "" {
			continue
		}
		f := byKey[key]
		if f == nil {
			f = &files{}
			byKey[key] = f
		}
		if kind == "raw" {
			f.raws = append(f.raws, id)
		} else {
			f.exports = append(f.exports, id)
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM raw_stacks"); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO raw_stacks(export_id,raw_id) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, f := range byKey {
		if len(f.raws) != 1 || len(f.exports) == 0 {
			continue
		}
		for _, export := range f.exports {
			if _, err = stmt.ExecContext(ctx, export, f.raws[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

// The reviewer's choice of how a RAW and its exports show. Anything but
// "separate" means together, the default.
const rawStackSetting = "raw_stacks"

// RawTogether reports whether a RAW and its exports show as one photo.
func (s *Store) RawTogether(ctx context.Context) (bool, error) {
	var value string
	err := s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", rawStackSetting).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return value != "separate", nil
}

func (s *Store) SetRawTogether(ctx context.Context, together bool) error {
	value := "together"
	if !together {
		value = "separate"
	}
	_, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", rawStackSetting, value)
	return err
}

// markStacks sets Stack on every file of every stack among the given files,
// leaving out exports that were split from it. A file that is not in the list
// (already in the Bin, or on another page) is left out of its stack, and a
// RAW left with no export shows alone. With stacks turned off in Settings,
// every file shows alone.
func (s *Store) markStacks(ctx context.Context, assets []*Asset) error {
	if len(assets) == 0 {
		return nil
	}
	together, err := s.RawTogether(ctx)
	if err != nil || !together {
		return err
	}
	byID := make(map[int64]*Asset, len(assets))
	ids := make([]any, 0, len(assets))
	marks := make([]string, 0, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
		ids = append(ids, a.ID)
		marks = append(marks, "?")
	}
	rows, err := s.read.QueryContext(ctx, `SELECT st.raw_id,st.export_id FROM raw_stacks st
		WHERE st.raw_id IN (`+strings.Join(marks, ",")+`)
		AND NOT EXISTS (SELECT 1 FROM raw_pair_splits sp WHERE sp.raw_id=st.raw_id AND sp.partner_id=st.export_id)`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	stacks := map[int64][]int64{}
	for rows.Next() {
		var raw, export int64
		if err = rows.Scan(&raw, &export); err != nil {
			return err
		}
		if byID[export] != nil {
			stacks[raw] = append(stacks[raw], export)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for raw, exports := range stacks {
		files := append([]int64{raw}, exports...)
		sort.Slice(files, func(i, j int) bool { return files[i] < files[j] })
		for _, id := range files {
			others := make([]int64, 0, len(files)-1)
			for _, other := range files {
				if other != id {
					others = append(others, other)
				}
			}
			byID[id].Stack = others
		}
	}
	return nil
}

// SetPaired takes an export out of its RAW's stack (paired false) or puts it
// back (true). Only files the index found stacked can be split or joined, in
// either order.
func (s *Store) SetPaired(ctx context.Context, a, b int64, paired bool) error {
	var raw, export int64
	err := s.read.QueryRowContext(ctx, "SELECT raw_id,export_id FROM raw_stacks WHERE (raw_id=? AND export_id=?) OR (raw_id=? AND export_id=?)", a, b, b, a).Scan(&raw, &export)
	if err == sql.ErrNoRows {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if paired {
		_, err = s.write.ExecContext(ctx, "DELETE FROM raw_pair_splits WHERE raw_id=? AND partner_id=?", raw, export)
	} else {
		_, err = s.write.ExecContext(ctx, "INSERT OR IGNORE INTO raw_pair_splits(raw_id,partner_id) VALUES(?,?)", raw, export)
	}
	return err
}
