package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// A copy kept over another loses nothing the other's sidecars record. When
// the copy going to the Bin has sidecars and the one kept has none, as an
// export from Photos beside the camera's own file does, or a JPEG beside the
// HEIC of the same shot, the sidecars are
// copied beside the kept file, under its name, before the Bin takes them. The
// copy going keeps its own, so restoring it brings them back as they were,
// and the ones written stay with the kept file.

// SidecarCopy is a sidecar of a file the plan moves, written beside the copy
// of that file that is kept.
type SidecarCopy struct {
	// From is the sidecar the plan moves, as one of its files' Original.
	From string `json:"from"`
	// To is where it is written, beside the kept copy and named after it,
	// such as 2022/2022-07/2022-07-03/IMG_5055.MOV.xmp.
	To string `json:"to"`
	// Keeper is the kept copy, relative to the archive root.
	Keeper string `json:"keeper"`
	// Done is true once it is written, or once a file found where it was
	// going was the same sidecar already.
	Done bool `json:"done,omitempty"`
}

// recordSidecars are the sidecars that record what a copy is kept for: who
// is in it, its keywords, rating and caption. A thumbnail or a low-resolution
// proxy belongs to its own file, and an edit list to the picture it edits.
var recordSidecars = map[string]bool{"xmp": true, "json": true, "xml": true}

// keptCopy is the one copy kept of a file: the only file in the archive
// proven the same, byte for byte, by its footage, or as the other format of
// one exposure, that is not marked for the Bin. It is empty when there is no
// such copy, or more than one.
func (b *BinEngine) keptCopy(ctx context.Context, id int64) (string, error) {
	var size int64
	var full, footage sql.NullString
	if e := b.s.read.QueryRowContext(ctx, `SELECT a.size_bytes,e.full_hash,f.footage_hash FROM assets a
	  LEFT JOIN asset_evidence e ON e.asset_id=a.id
	  LEFT JOIN asset_footage f ON f.asset_id=a.id AND f.size_bytes=a.size_bytes
	 WHERE a.id=?`, id).Scan(&size, &full, &footage); e != nil {
		return "", e
	}
	rows, e := b.s.read.QueryContext(ctx, `WITH copies AS (
		SELECT e.asset_id FROM asset_evidence e JOIN assets a ON a.id=e.asset_id WHERE ?!='' AND e.full_hash=? AND a.size_bytes=?
		UNION
		SELECT f.asset_id FROM asset_footage f JOIN assets a ON a.id=f.asset_id AND a.size_bytes=f.size_bytes WHERE ?!='' AND f.footage_hash=?
		UNION
		SELECT CASE WHEN p.heic_id=? THEN p.jpeg_id ELSE p.heic_id END FROM (`+provenFormatPairs+`) p WHERE ? IN (p.heic_id,p.jpeg_id)
	)
	SELECT a.relative_path FROM copies c JOIN assets a ON a.id=c.asset_id LEFT JOIN decisions d ON d.asset_id=a.id
	 WHERE a.id!=? AND COALESCE(d.status,'unreviewed')!='cull' AND a.source_id='archive'
	   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	   AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
	 LIMIT 2`, full.String, full.String, size, footage.String, footage.String, id, id, id)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	var kept []string
	for rows.Next() {
		var p string
		if e = rows.Scan(&p); e != nil {
			return "", e
		}
		kept = append(kept, p)
	}
	if e = rows.Err(); e != nil || len(kept) != 1 || !strings.HasPrefix(kept[0], "/archive/") {
		return "", e
	}
	rel := strings.TrimPrefix(kept[0], "/archive/")
	if !safeRelative(rel) {
		return "", nil
	}
	return rel, nil
}

// sidecarCopies plans writing the sidecars of a file going to the Bin beside
// the copy of it that is kept, when that copy has no sidecars of its own or
// shared. One sidecar of each kind is written, the first found, and none is
// planned where a file already is.
func (b *BinEngine) sidecarCopies(ctx context.Context, id int64, sidecars []string) ([]SidecarCopy, error) {
	var records []string
	for _, sidecar := range sidecars {
		if recordSidecars[strings.ToLower(strings.TrimPrefix(path.Ext(sidecar), "."))] {
			records = append(records, sidecar)
		}
	}
	if len(records) == 0 {
		return nil, nil
	}
	keeper, e := b.keptCopy(ctx, id)
	if e != nil || keeper == "" {
		return nil, e
	}
	if _, e = b.regular(keeper); e != nil {
		return nil, nil
	}
	own, shared, e := b.sidecars(keeper)
	if e != nil || len(own) > 0 || len(shared) > 0 {
		return nil, e
	}
	var copies []SidecarCopy
	kinds := map[string]bool{}
	for _, sidecar := range records {
		kind := strings.ToLower(path.Ext(sidecar))
		if kinds[kind] {
			continue
		}
		kinds[kind] = true
		to := path.Join(path.Dir(keeper), path.Base(keeper)+path.Ext(sidecar))
		if _, e = b.root.Lstat(to); !errors.Is(e, os.ErrNotExist) {
			continue
		}
		copies = append(copies, SidecarCopy{From: sidecar, To: to, Keeper: keeper})
	}
	return copies, nil
}

// writeCopies writes the plan's sidecar copies not written yet, each from
// wherever its sidecar is now: in the archive, or in the Bin once moved. A
// sidecar copy is never written over a file, and is not written when the
// kept copy has gone.
func (b *BinEngine) writeCopies(ctx context.Context, p *BinPlan) error {
	for c := range p.Copies {
		copy := &p.Copies[c]
		if copy.Done {
			continue
		}
		index := -1
		for i, f := range p.Files {
			if f.Original == copy.From {
				index = i
			}
		}
		if index < 0 {
			return fmt.Errorf("a sidecar to copy is not in the plan: %s", copy.From)
		}
		file := p.Files[index]
		source := file.Original
		if file.Phase == "bin" {
			source = stored(p, index)
		}
		if _, e := b.regular(copy.Keeper); e != nil {
			p.Warnings = append(p.Warnings, "The copy kept has gone, so its sidecar was not written: "+copy.To)
			copy.Done = true
			continue
		}
		if e := b.writeCopy(ctx, p.ID+"-"+strconv.Itoa(c), source, copy.To, file); e != nil {
			return e
		}
		copy.Done = true
		if e := b.save(p); e != nil {
			return e
		}
	}
	return nil
}

// writeCopy writes the sidecar at source to target, checked against the
// fingerprint the plan took of it, through a temporary file linked into
// place, so a file already at target is never replaced. One already there
// with the same contents is the copy written before an interruption.
func (b *BinEngine) writeCopy(ctx context.Context, name, source, target string, expected BinFile) error {
	if e := b.verify(ctx, source, expected); e != nil {
		return e
	}
	if _, e := b.root.Lstat(target); e == nil {
		if b.verify(ctx, target, expected) == nil {
			return nil
		}
		return fmt.Errorf("a file appeared where a sidecar was going; nothing was replaced: %s", target)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	in, e := b.root.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	temp := path.Join(path.Dir(target), ".daddy-cull-"+name+".tmp")
	_ = b.root.Remove(temp)
	out, e := b.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
	if e != nil {
		return e
	}
	defer b.root.Remove(temp)
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, hash), &contextReader{ctx, in})
	if e == nil {
		e = out.Sync()
	}
	if closeErr := out.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if n != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.Hash {
		return fmt.Errorf("content changed: %s", source)
	}
	modified := time.Unix(0, expected.Mtime)
	if e = b.root.Chtimes(temp, modified, modified); e != nil {
		return e
	}
	if e = b.root.Link(temp, target); e != nil {
		return fmt.Errorf("a file appeared where a sidecar was going; nothing was replaced: %w", e)
	}
	return b.syncDir(target)
}
