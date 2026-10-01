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
// A HEIC and a JPEG of one name, with no RAW, stack too, but only once their
// metadata proves them one exposure, as format_pairs.go reads it: a name alone
// is shared by different pictures often enough.
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
	type files struct{ raws, exports, heics, jpegs []int64 }
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
		switch ext := strings.ToLower(path.Ext(p)); {
		case kind == "raw":
			f.raws = append(f.raws, id)
			continue
		case ext == ".heic" || ext == ".heif":
			f.heics = append(f.heics, id)
		case ext == ".jpg" || ext == ".jpeg":
			f.jpegs = append(f.jpegs, id)
		}
		f.exports = append(f.exports, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, table := range []string{"raw_stacks", "format_pairs"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO raw_stacks(export_id,raw_id) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	pair, err := tx.PrepareContext(ctx, "INSERT INTO format_pairs(jpeg_id,heic_id) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer pair.Close()
	for _, f := range byKey {
		// A HEIC and a JPEG of one name, and nothing else of it, are a pair
		// to prove; see format_pairs.go.
		if len(f.raws) == 0 && len(f.heics) == 1 && len(f.jpegs) == 1 && len(f.exports) == 2 {
			if _, err = pair.ExecContext(ctx, f.jpegs[0], f.heics[0]); err != nil {
				return err
			}
		}
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
// leaving out exports that were split from it: a RAW and its exports, and a
// HEIC and a JPEG proven one exposure. A file that is not in the list
// (already in the Bin, or on another page) is left out of its stack, and a
// file left with no other shows alone. With stacks turned off in Settings,
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
	in := strings.Join(marks, ",")
	// Each stack is found by the file it is built on, its RAW or its HEIC,
	// and the others are joined to it.
	stacks := map[int64][]int64{}
	for _, query := range []string{
		`SELECT st.raw_id,st.export_id FROM raw_stacks st
		WHERE st.raw_id IN (` + in + `)
		AND NOT EXISTS (SELECT 1 FROM raw_pair_splits sp WHERE sp.raw_id=st.raw_id AND sp.partner_id=st.export_id)`,
		`SELECT fp.heic_id,fp.jpeg_id FROM (` + provenFormatPairs + `) fp
		WHERE fp.heic_id IN (` + in + `)
		AND NOT EXISTS (SELECT 1 FROM raw_pair_splits sp WHERE sp.raw_id=fp.heic_id AND sp.partner_id=fp.jpeg_id)`,
	} {
		if err = s.stacksFrom(ctx, query, ids, byID, stacks); err != nil {
			return err
		}
	}
	for lead, others := range stacks {
		files := append([]int64{lead}, others...)
		sort.Slice(files, func(i, j int) bool { return files[i] < files[j] })
		for _, id := range files {
			rest := make([]int64, 0, len(files)-1)
			for _, other := range files {
				if other != id {
					rest = append(rest, other)
				}
			}
			byID[id].Stack = rest
		}
	}
	return nil
}

// stacksFrom adds to stacks the files query joins to the file each stack is
// built on, for the files in byID.
func (s *Store) stacksFrom(ctx context.Context, query string, ids []any, byID map[int64]*Asset, stacks map[int64][]int64) error {
	rows, err := s.read.QueryContext(ctx, query, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var lead, other int64
		if err = rows.Scan(&lead, &other); err != nil {
			return err
		}
		if byID[other] != nil {
			stacks[lead] = append(stacks[lead], other)
		}
	}
	return rows.Err()
}

// SetPaired takes a file out of its stack (paired false) or puts it back
// (true): an export from its RAW's, or the JPEG from its HEIC's. Only files
// the index found stacked can be split or joined, in either order.
func (s *Store) SetPaired(ctx context.Context, a, b int64, paired bool) error {
	var lead, other int64
	err := s.read.QueryRowContext(ctx, `SELECT raw_id,export_id FROM raw_stacks WHERE (raw_id=? AND export_id=?) OR (raw_id=? AND export_id=?)
		UNION ALL SELECT heic_id,jpeg_id FROM (`+provenFormatPairs+`) WHERE (heic_id=? AND jpeg_id=?) OR (heic_id=? AND jpeg_id=?)
		LIMIT 1`, a, b, b, a, a, b, b, a).Scan(&lead, &other)
	if err == sql.ErrNoRows {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if paired {
		_, err = s.write.ExecContext(ctx, "DELETE FROM raw_pair_splits WHERE raw_id=? AND partner_id=?", lead, other)
	} else {
		_, err = s.write.ExecContext(ctx, "INSERT OR IGNORE INTO raw_pair_splits(raw_id,partner_id) VALUES(?,?)", lead, other)
	}
	return err
}
