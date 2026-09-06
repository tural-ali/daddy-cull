package catalog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

type ImportAsset struct {
	Path       string `json:"path"`
	Source     string `json:"source"`
	CapturedAt int64  `json:"capturedAt"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	Favourite  bool   `json:"favourite"`
	AnchorPath string `json:"anchorPath"`
}

// ImportSnapshot only creates a new isolated catalogue. It never opens original files.
func (s *Store) ImportSnapshot(ctx context.Context, input io.Reader) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM assets").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("import requires an empty catalogue; existing decisions are protected")
	}
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 65536), 1048576)
	for scan.Scan() {
		var a ImportAsset
		if err = json.Unmarshal(scan.Bytes(), &a); err != nil {
			return err
		}
		root := "/archive/"
		if a.Source == "takeout" {
			root = "/upgrades/"
		} else if a.Source != "archive" {
			return ErrInvalid
		}
		if !strings.HasPrefix(a.Path, root) || path.Clean(a.Path) != a.Path || a.CapturedAt < 0 || a.Size < 0 {
			return ErrInvalid
		}
		var anchor any
		if a.AnchorPath != "" {
			var id int64
			if err = tx.QueryRow("SELECT id FROM assets WHERE source_id='archive' AND relative_path=?", a.AnchorPath).Scan(&id); err != nil {
				return err
			}
			anchor = id
		}
		r, e := tx.Exec("INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id,anchor_id) VALUES(?,?,?,?,?,?)", a.Path, a.CapturedAt, a.Kind, a.Size, a.Source, anchor)
		if e != nil {
			return e
		}
		id, _ := r.LastInsertId()
		if a.Favourite {
			if _, err = tx.Exec("INSERT INTO decisions VALUES(?,'unreviewed',1,0)", id); err != nil {
				return err
			}
		}
		count++
	}
	if err = scan.Err(); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("empty snapshot refused")
	}
	if _, err = tx.Exec("UPDATE stats SET total=? WHERE id=1", count); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('library','real'),('snapshot_at',datetime('now'))"); err != nil {
		return err
	}
	return tx.Commit()
}
