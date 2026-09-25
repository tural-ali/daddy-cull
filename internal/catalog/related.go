package catalog

import (
	"context"
	"path"
	"regexp"
	"strings"
)

// Ported from legacy Moments::stemKey. These are comparison candidates, never
// proof of byte identity or a reason to change another file's decision.
var stemSuffixes = []*regexp.Regexp{
	regexp.MustCompile(`_hevc$`), regexp.MustCompile(`[_-]?edited$`),
	regexp.MustCompile(`\s*\(edited\)$`), regexp.MustCompile(`\s*\(\d+\)$`),
	regexp.MustCompile(`\s*\(r\d+\)$`), regexp.MustCompile(`-\d$`),
}

func relatedKey(p string) string {
	base := strings.ToLower(path.Base(p))
	stem := strings.TrimSuffix(base, path.Ext(base))
	for _, re := range stemSuffixes {
		stem = re.ReplaceAllString(stem, "")
	}
	if stem == "" {
		stem = base
	}
	return path.Dir(p) + "/" + stem
}

// Metadata-only indexing is linear in catalogue size and never opens media.
// It runs before serving requests; the transaction keeps prior results intact
// if indexing fails. A repeated run preserves every review decision.
func (s *Store) IndexRelated(ctx context.Context) error {
	rows, e := s.read.QueryContext(ctx, "SELECT a.id,COALESCE(anchor.relative_path,a.relative_path) FROM assets a LEFT JOIN assets anchor ON anchor.id=a.anchor_id WHERE a.id NOT IN (SELECT asset_id FROM missing_assets)")
	if e != nil {
		return e
	}
	type member struct {
		id  int64
		key string
	}
	var members []member
	for rows.Next() {
		var id int64
		var p string
		if e = rows.Scan(&id, &p); e != nil {
			rows.Close()
			return e
		}
		members = append(members, member{id, relatedKey(p)})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	tx, e := s.write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM related_assets"); e != nil {
		return e
	}
	stmt, e := tx.PrepareContext(ctx, "INSERT INTO related_assets(asset_id,group_key) VALUES(?,?)")
	if e != nil {
		return e
	}
	defer stmt.Close()
	for _, m := range members {
		if _, e = stmt.ExecContext(ctx, m.id, m.key); e != nil {
			return e
		}
	}
	return tx.Commit()
}

const relatedCount = `(SELECT count(*) FROM related_assets rm JOIN related_assets peers ON peers.group_key=rm.group_key WHERE rm.asset_id=a.id AND peers.asset_id!=a.id AND NOT EXISTS (SELECT 1 FROM file_state fs WHERE fs.asset_id=peers.asset_id AND fs.state!='restored'))`

func (s *Store) Related(ctx context.Context, id int64) ([]Asset, error) {
	rows, e := s.read.QueryContext(ctx, assetSelect+` WHERE (a.id=? OR a.id IN (SELECT peers.asset_id FROM related_assets own JOIN related_assets peers ON peers.group_key=own.group_key WHERE own.asset_id=?)) AND NOT EXISTS (SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored') ORDER BY a.relative_path LIMIT 201`, id, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		if e = scanAsset(rows, &a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	if len(out) > 200 {
		return nil, ErrInvalid
	}
	return out, rows.Err()
}
