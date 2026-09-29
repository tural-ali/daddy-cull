package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
)

// Asset is a file in the catalogue, with the choice saved for it.
type Asset struct {
	// RelatedCount counts files related to this one, such as the other half
	// of a Live Photo.
	RelatedCount int `json:"relatedCount"`
	// ID is the file's id in the catalogue. It never changes, wherever the
	// file moves.
	ID int64 `json:"id"`
	// Path is where the file is, under its source's root, such as
	// /archive/2019/2019-08/2019-08-14/IMG_1234.HEIC.
	Path string `json:"path"`
	// CapturedAt is when it was taken, in Unix seconds. Exactly midnight UTC
	// means only the date is known, from the folder it is filed in.
	CapturedAt int64 `json:"capturedAt"`
	// Kind is image or video.
	Kind string `json:"kind"`
	// Size is the file's size in bytes.
	Size int64 `json:"size"`
	// Status is unreviewed, keep, later or cull.
	Status string `json:"status"`
	// Favourite is whether the file has a heart.
	Favourite bool `json:"favourite"`
	// Revision counts the choices saved for the file; send it with the next.
	Revision int64 `json:"revision"`
	// Source is where the file comes from, such as archive.
	Source string `json:"source"`
	// AlternativeCount counts other versions of the same picture.
	AlternativeCount int `json:"alternativeCount"`
	// Stack lists the other files of the same exposure, a RAW and the JPEG,
	// HEIC or TIFF files exported beside it, on the pages that show them as
	// one photo.
	Stack []int64 `json:"stack,omitempty"`
	// New marks a file that reached the archive after its day was reviewed
	// and still waits for a decision, on the day page.
	New bool `json:"new,omitempty"`
	// Duration is how long a video runs, in seconds, once it has been read.
	Duration float64 `json:"duration,omitempty"`
	// Width and Height are the picture's size as it is shown, turned by its
	// orientation, once it has been read.
	Width int `json:"width,omitempty"`
	// Height is the picture's height as it is shown, once it has been read.
	Height int `json:"height,omitempty"`
	// Turn is how many quarter turns clockwise the reviewer turned the file
	// in Cull, 0 to 3. Width and Height are before it.
	Turn int `json:"turn,omitempty"`
}
type Cursor struct {
	Version int    `json:"v"`
	Groups  bool   `json:"groups,omitempty"`
	Status  string `json:"status,omitempty"`
	Time    int64  `json:"t"`
	ID      int64  `json:"id"`
	MaxID   int64  `json:"max"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Grouped bool   `json:"grouped"`
	Matches bool   `json:"matches,omitempty"`
	From    int64  `json:"from,omitempty"`
}

// Page is one page of files, and where the next one starts.
type Page struct {
	// Assets are this page's files, in capture order.
	Assets []Asset `json:"assets"`
	// Next is passed as after to read the next page; empty when there is none.
	Next string `json:"next"`
}

func (s *Store) Page(ctx context.Context, token, kind, source string, limit int) (Page, error) {
	return s.page(ctx, token, kind, source, limit, false, "", false, "")
}

func (s *Store) ReviewPage(ctx context.Context, token, kind string, limit int) (Page, error) {
	return s.page(ctx, token, kind, "", limit, true, "", false, "")
}

func (s *Store) page(ctx context.Context, token, kind, source string, limit int, grouped bool, from string, matches bool, status string, groupMode ...bool) (Page, error) {
	p := Page{Assets: make([]Asset, 0)}
	groups := len(groupMode) > 0 && groupMode[0]
	if status != "" && status != "unreviewed" && status != "later" && status != "keep" && status != "cull" {
		return p, ErrInvalid
	}
	if limit < 1 || limit > 200 {
		return p, ErrInvalid
	}
	if kind != "" && kind != "image" && kind != "raw" && kind != "video" {
		return p, ErrInvalid
	}
	if source != "" && source != "archive" && source != "takeout" {
		return p, ErrInvalid
	}
	var start int64
	if from != "" {
		t, e := time.Parse("2006-01-02", from)
		if e != nil || t.Unix() < 0 {
			return p, ErrInvalid
		}
		start = t.Unix()
	}
	c := Cursor{Version: 1, Groups: groups, Time: start - 1, Kind: kind, Source: source, Grouped: grouped, From: start, Matches: matches, Status: status}
	if token != "" {
		if len(token) > 512 {
			return p, ErrInvalid
		}
		b, e := base64.RawURLEncoding.DecodeString(token)
		if e != nil || json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Time < 0 || c.ID < 1 || c.MaxID < c.ID || c.Kind != kind || c.Source != source || c.Grouped != grouped || c.From != start || c.Matches != matches || c.Status != status || c.Groups != groups {
			return p, ErrInvalid
		}
	} else if err := s.read.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM assets").Scan(&c.MaxID); err != nil {
		return p, err
	}
	query := assetSelect + " WHERE (a.captured_at,a.id)>(?,?) AND a.id<=? AND NOT EXISTS (SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')"
	args := []any{c.Time, c.ID, c.MaxID}
	if status != "" {
		query += " AND COALESCE(d.status,'unreviewed')=?"
		args = append(args, status)
	}
	if grouped && !groups {
		query += " AND a.anchor_id IS NULL"
	}
	if matches {
		query += " AND EXISTS (SELECT 1 FROM assets alt WHERE alt.anchor_id=a.id)"
	}
	if kind != "" {
		query += " AND a.kind=?"
		args = append(args, kind)
	}
	if source != "" {
		query += " AND a.source_id=?"
		args = append(args, source)
	}
	// Select one eligible representative per related group. A kept representative
	// must never hide an undecided sibling. Bin and filter scope apply to both.
	if groups {
		query += ` AND NOT EXISTS (SELECT 1 FROM related_assets own JOIN related_assets peers ON peers.group_key=own.group_key JOIN assets peer ON peer.id=peers.asset_id LEFT JOIN decisions pd ON pd.asset_id=peer.id WHERE own.asset_id=a.id AND (peer.captured_at,peer.id)<(a.captured_at,a.id) AND peer.captured_at>=? AND peer.id<=? AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=peer.id AND fs.state!='restored')`
		args = append(args, start, c.MaxID)
		if status != "" {
			query += " AND COALESCE(pd.status,'unreviewed')=?"
			args = append(args, status)
		}
		if kind != "" {
			query += " AND peer.kind=?"
			args = append(args, kind)
		}
		if matches {
			query += " AND EXISTS(SELECT 1 FROM assets alt WHERE alt.anchor_id=peer.id)"
		}
		if source != "" {
			query += " AND peer.source_id=?"
			args = append(args, source)
		}
		query += ")"
	}
	query += " ORDER BY a.captured_at,a.id LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Asset
		if err = scanAsset(rows, &a); err != nil {
			return p, err
		}
		p.Assets = append(p.Assets, a)
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	if len(p.Assets) > limit {
		p.Assets = p.Assets[:limit]
		last := p.Assets[limit-1]
		c.ID = last.ID
		c.Time = last.CapturedAt
		b, _ := json.Marshal(c)
		p.Next = base64.RawURLEncoding.EncodeToString(b)
	}
	return p, nil
}

const assetSelect = "SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id)," + relatedCount + " FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id"

type scanner interface{ Scan(...any) error }

func scanAsset(row scanner, a *Asset) error {
	return row.Scan(&a.ID, &a.Path, &a.CapturedAt, &a.Kind, &a.Size, &a.Status, &a.Favourite, &a.Revision, &a.Source, &a.AlternativeCount, &a.RelatedCount)
}

func (s *Store) Alternatives(ctx context.Context, id int64) ([]Asset, error) {
	var anchor int64
	if err := s.read.QueryRowContext(ctx, "SELECT COALESCE(anchor_id,id) FROM assets WHERE id=?", id).Scan(&anchor); err != nil {
		return nil, err
	}
	rows, err := s.read.QueryContext(ctx, assetSelect+" WHERE a.id=? OR a.anchor_id=? ORDER BY a.id LIMIT 201", anchor, anchor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := []Asset{}
	for rows.Next() {
		var a Asset
		if err = scanAsset(rows, &a); err != nil {
			return nil, err
		}
		assets = append(assets, a)
	}
	if len(assets) > 200 {
		return nil, ErrInvalid
	}
	return assets, rows.Err()
}
