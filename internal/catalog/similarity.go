package catalog

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"log"
	"math"
	"math/bits"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

const similarityAlgorithm = "dhash-colour-v1"
const comparisonWindowSize = 80

type visualFingerprint struct {
	hash                     string
	aspect, red, green, blue float64
}

// fingerprint averages a 9 by 8 grid, then compares neighbouring luminance.
// Uniform and nearly uniform images provide too little evidence to compare.
func fingerprint(img image.Image) visualFingerprint {
	b := img.Bounds()
	if b.Dx() < 9 || b.Dy() < 8 {
		return visualFingerprint{}
	}
	var grid [8][9]float64
	var r, g, blue, sum, squared float64
	for y := range 8 {
		for x := range 9 {
			var rr, gg, bb, count float64
			for yy := b.Min.Y + y*b.Dy()/8; yy < b.Min.Y+(y+1)*b.Dy()/8; yy++ {
				for xx := b.Min.X + x*b.Dx()/9; xx < b.Min.X+(x+1)*b.Dx()/9; xx++ {
					r0, g0, b0, _ := img.At(xx, yy).RGBA()
					rr += float64(r0) / 257
					gg += float64(g0) / 257
					bb += float64(b0) / 257
					count++
				}
			}
			rr /= count
			gg /= count
			bb /= count
			gray := .299*rr + .587*gg + .114*bb
			grid[y][x] = gray
			r += rr
			g += gg
			blue += bb
			sum += gray
			squared += gray * gray
		}
	}
	var hash uint64
	for y := range 8 {
		for x := range 8 {
			if grid[y][x] > grid[y][x+1] {
				hash |= 1 << (y*8 + x)
			}
		}
	}
	ones := bits.OnesCount64(hash)
	if squared/72-(sum/72)*(sum/72) < 64 || ones < 8 || ones > 56 {
		return visualFingerprint{}
	}
	return visualFingerprint{fmt.Sprintf("%016x", hash), float64(b.Dx()) / float64(b.Dy()), r / 72, g / 72, blue / 72}
}

func similarVisual(a, b visualFingerprint) bool {
	return visuallyClose(a.hash, b.hash) && a.aspect > 0 && b.aspect > 0 &&
		math.Abs(a.aspect-b.aspect)/math.Max(a.aspect, b.aspect) <= .12 &&
		math.Abs(a.red-b.red) <= 45 && math.Abs(a.green-b.green) <= 45 && math.Abs(a.blue-b.blue) <= 45
}

func (s *Store) similarityTargets(ctx context.Context, id int64, limit int) ([]shapeTarget, error) {
	args := []any{similarityAlgorithm, time.Now().Add(-24 * time.Hour).Unix(), limit}
	from := "assets a"
	order := "a.captured_at DESC,a.id DESC"
	if id > 0 {
		var relative, source string
		if err := s.read.QueryRowContext(ctx, "SELECT relative_path,source_id FROM assets WHERE id=?", id).Scan(&relative, &source); err != nil {
			return nil, err
		}
		folder := path.Dir(relative) + "/"
		from = `(SELECT a.* FROM assets a WHERE a.source_id=? AND a.relative_path>=? AND a.relative_path<?
		 AND instr(substr(a.relative_path,?),'/')=0 AND a.kind IN ('image','raw')
		 AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
		 AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
		 ORDER BY abs(a.id-?),a.id LIMIT 80) a`
		args = append([]any{source, folder, strings.TrimSuffix(folder, "/") + "0", len(folder) + 1, id}, args...)
		order = fmt.Sprintf("abs(a.id-%d),a.id", id)
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.size_bytes FROM `+from+`
	 LEFT JOIN photo_similarity p ON p.asset_id=a.id
	 WHERE a.kind IN ('image','raw') AND (p.asset_id IS NULL OR p.size_bytes!=a.size_bytes OR p.algorithm!=? OR (p.fingerprint='' AND p.checked_at<?))
	 AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
	 AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
	 ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []shapeTarget{}
	for rows.Next() {
		var target shapeTarget
		if err = rows.Scan(&target.id, &target.relative, &target.size); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

// KeepSimilarities indexes one cached preview at a time, yielding to review.
// A comparison request prioritises a bounded local window without reading media
// in the HTTP handler. Failed previews are remembered so they cannot starve it.
func (s *Store) KeepSimilarities(ctx context.Context, roots MediaRoots) {
	s.comparisonRunning.Store(true)
	defer s.comparisonRunning.Store(false)
	for ctx.Err() == nil {
		id := s.comparisonPriority.Load()
		targets, err := s.similarityTargets(ctx, id, 32)
		if err == nil && len(targets) == 0 && id > 0 {
			s.comparisonPriority.CompareAndSwap(id, 0)
			id = 0
			targets, err = s.similarityTargets(ctx, 0, 32)
		}
		if err == nil {
			for _, target := range targets {
				if ctx.Err() != nil {
					return
				}
				if err = s.indexSimilarity(ctx, roots, target); err != nil {
					break
				}
				// Let a newly requested comparison interrupt a background batch.
				if s.comparisonPriority.Load() != id {
					break
				}
			}
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("photo similarity: %v", err)
		}
		delay := 5 * time.Second
		if s.comparisonPriority.Load() > 0 && len(targets) > 0 {
			delay = 100 * time.Millisecond
		}
		if len(targets) == 0 {
			delay = time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.comparisonWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Store) indexSimilarity(ctx context.Context, roots MediaRoots, target shapeTarget) error {
	// Metadata is needed promptly for independently numbered burst shots too.
	var needsMetadata bool
	if err := s.read.QueryRowContext(ctx, "SELECT NOT EXISTS(SELECT 1 FROM exposures WHERE asset_id=? AND size_bytes=?)", target.id, target.size).Scan(&needsMetadata); err != nil {
		return err
	}
	if needsMetadata && roots.RawTool != "" {
		if out, ok := readMetadata(ctx, roots, target, "-DateTimeOriginal", "-SubSecTimeOriginal", "-Model"); ok {
			taken, subsec, model := parseExposure(out)
			if _, err := s.write.ExecContext(ctx, `INSERT INTO exposures(asset_id,size_bytes,taken,subsec,model) VALUES(?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET size_bytes=excluded.size_bytes,taken=excluded.taken,subsec=excluded.subsec,model=excluded.model`, target.id, target.size, taken, subsec, model); err != nil {
				return err
			}
		}
	}
	value := readFingerprint(ctx, roots, target)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, err := s.write.ExecContext(ctx, `INSERT INTO photo_similarity(asset_id,size_bytes,algorithm,fingerprint,aspect,red,green,blue,checked_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET size_bytes=excluded.size_bytes,algorithm=excluded.algorithm,fingerprint=excluded.fingerprint,aspect=excluded.aspect,red=excluded.red,green=excluded.green,blue=excluded.blue,checked_at=excluded.checked_at`, target.id, target.size, similarityAlgorithm, value.hash, value.aspect, value.red, value.green, value.blue, time.Now().Unix())
	return err
}

func readFingerprint(ctx context.Context, roots MediaRoots, target shapeTarget) visualFingerprint {
	ext := strings.ToLower(path.Ext(target.relative))
	if videoContainer[ext] {
		return visualFingerprint{}
	}
	mount, relative, ok := roots.root(target.relative, target.id)
	if !ok {
		return visualFingerprint{}
	}
	root, err := os.OpenRoot(mount)
	if err != nil {
		return visualFingerprint{}
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return visualFingerprint{}
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != target.size {
		return visualFingerprint{}
	}
	subject := strconv.FormatInt(target.id, 10)
	var tile []byte
	if rawImage[ext] {
		tile, err = rawPreview(ctx, roots.RawTool, file, roots.Cache, subject, target.size, before.ModTime().Unix(), gridPixels)
	} else if viewableImage[ext] {
		tile, err = withWorker(ctx, func() ([]byte, error) {
			return thumbnail(file, roots.Cache, subject, target.size, before.ModTime().Unix())
		})
	} else {
		err = fmt.Errorf("requires decoder")
	}
	if err != nil && roots.FFmpeg != "" {
		tile, err = stillFrame(ctx, roots.FFmpeg, file, roots.Cache, subject, target.size, before.ModTime().Unix(), gridPixels)
	}
	if err != nil {
		return visualFingerprint{}
	}
	after, err := file.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return visualFingerprint{}
	}
	img, _, err := image.Decode(bytes.NewReader(tile))
	if err != nil {
		return visualFingerprint{}
	}
	return fingerprint(img)
}
