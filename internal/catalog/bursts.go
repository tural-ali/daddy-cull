package catalog

import (
	"context"
	"math/bits"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

func exposureTime(taken string, fallback int64) int64 {
	if at, err := time.Parse("2006:01:02 15:04:05", taken); err == nil {
		return at.Unix()
	}
	// Folder dates have no clock information and are not burst evidence.
	if fallback%86400 != 0 {
		return fallback
	}
	return 0
}
func visuallyClose(a, b string) bool {
	if len(a) != 16 || len(b) != 16 {
		return false
	}
	x, e := strconv.ParseUint(a, 16, 64)
	y, f := strconv.ParseUint(b, 16, 64)
	return e == nil && f == nil && bits.OnesCount64(x^y) <= 6
}

// Burst returns explicit comparison candidates, never duplicate proof.
// Same-folder, near-time shots and available perceptual hashes are evidence;
// folder-only dates and different known camera models cannot form a burst.
func (s *Store) Burst(ctx context.Context, id int64) ([]Asset, error) {
	var own Asset
	if err := scanAsset(s.read.QueryRowContext(ctx, assetSelect+" WHERE a.id=? AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored') AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)", id), &own); err != nil {
		return nil, err
	}
	folder := path.Dir(own.Path) + "/"
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.captured_at,COALESCE(x.taken,''),COALESCE(x.model,''),COALESCE(p.fingerprint,''),COALESCE(p.aspect,0),COALESCE(p.red,0),COALESCE(p.green,0),COALESCE(p.blue,0)
 FROM assets a LEFT JOIN exposures x ON x.asset_id=a.id AND x.size_bytes=a.size_bytes
 LEFT JOIN photo_similarity p ON p.asset_id=a.id AND p.size_bytes=a.size_bytes AND p.algorithm=?
 WHERE a.source_id=? AND a.relative_path>=? AND a.relative_path<? AND a.kind IN ('image','raw')
 AND instr(substr(a.relative_path,?),'/')=0
 AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
 ORDER BY abs(a.id-?) LIMIT 2000`, similarityAlgorithm, own.Source, folder, strings.TrimSuffix(folder, "/")+"0", len(folder)+1, id)
	if err != nil {
		return nil, err
	}
	type fact struct {
		id, at int64
		camera string
		visual visualFingerprint
	}
	facts := []fact{}
	var root fact
	for rows.Next() {
		var f fact
		var taken string
		var at int64
		if err = rows.Scan(&f.id, &at, &taken, &f.camera, &f.visual.hash, &f.visual.aspect, &f.visual.red, &f.visual.green, &f.visual.blue); err != nil {
			rows.Close()
			return nil, err
		}
		f.at = exposureTime(taken, at)
		facts = append(facts, f)
		if f.id == id {
			root = f
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	byID := map[int64]fact{}
	for _, f := range facts {
		byID[f.id] = f
	}
	pending := false
	if s.comparisonRunning.Load() {
		targets, e := s.similarityTargets(ctx, id, 1)
		if e != nil {
			return nil, e
		}
		pending = len(targets) > 0
		if pending {
			s.comparisonPriority.Store(id)
			select {
			case s.comparisonWake <- struct{}{}:
			default:
			}
		}
	}
	reasons := map[int64]string{id: "Selected photo"}
	for _, f := range facts {
		if f.id == id {
			continue
		}
		sameCamera := root.camera == "" || f.camera == "" || strings.EqualFold(root.camera, f.camera)
		delta := f.at - root.at
		if delta < 0 {
			delta = -delta
		}
		if root.at > 0 && f.at > 0 && delta <= 8 && sameCamera {
			reasons[f.id] = "Taken within 8 seconds"
		}
		if similarVisual(root.visual, f.visual) {
			reasons[f.id] = "Similar visual fingerprint"
			if root.at > 0 && f.at > 0 && delta <= 8 && sameCamera {
				reasons[f.id] = "Nearby capture and similar appearance"
			}
		}
	}
	ids := make([]int64, 0, len(reasons))
	for candidate := range reasons {
		ids = append(ids, candidate)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i] == id || ids[j] == id {
			return ids[i] == id && ids[j] != id
		}
		left, right := byID[ids[i]], byID[ids[j]]
		if root.at > 0 {
			if (left.at > 0) != (right.at > 0) {
				return left.at > 0
			}
		}
		if root.at > 0 && left.at > 0 && right.at > 0 {
			ld, rd := left.at-root.at, right.at-root.at
			if ld < 0 {
				ld = -ld
			}
			if rd < 0 {
				rd = -rd
			}
			if ld != rd {
				return ld < rd
			}
		}
		return ids[i] < ids[j]
	})
	if len(ids) > 80 {
		ids = ids[:80]
	}
	// Include the companions before marking stacks, so the 40-shot window can
	// never split a RAW from its exports at the boundary.
	pool := map[int64]*Asset{}
	for _, candidate := range ids {
		peers, e := s.burstStack(ctx, candidate, own.Source)
		if e != nil {
			return nil, e
		}
		for _, peer := range peers {
			copy := peer
			pool[peer.ID] = &copy
		}
	}
	pointers := make([]*Asset, 0, len(pool))
	for _, asset := range pool {
		pointers = append(pointers, asset)
	}
	if err = s.markStacks(ctx, pointers); err != nil {
		return nil, err
	}
	out := []Asset{}
	seen := map[int64]bool{}
	shots := 0
	for _, candidate := range ids {
		a := pool[candidate]
		if a == nil || seen[candidate] {
			continue
		}
		group := append([]int64{candidate}, a.Stack...)
		if len(out)+len(group) > 400 || shots == 40 {
			break
		}
		shots++
		for _, member := range group {
			if seen[member] {
				continue
			}
			copy := *pool[member]
			copy.ComparisonReason = reasons[candidate]
			copy.ComparisonPending = member == id && pending
			out = append(out, copy)
			seen[member] = true
		}
	}
	return out, nil
}

// burstStack loads only indexed format companions, not a normalised filename
// bucket that might contain hundreds of unrelated independently numbered shots.
func (s *Store) burstStack(ctx context.Context, id int64, source string) ([]Asset, error) {
	rows, err := s.read.QueryContext(ctx, `WITH pairs AS (
	 SELECT raw_id AS lead,export_id AS other FROM raw_stacks
	 UNION ALL SELECT heic_id,jpeg_id FROM (`+provenFormatPairs+`)
	), leads AS (SELECT ? AS id UNION SELECT lead FROM pairs WHERE other=?) `+assetSelect+`
	 WHERE a.id IN (SELECT ? UNION SELECT id FROM leads UNION SELECT other FROM pairs WHERE lead IN (SELECT id FROM leads))
	 AND a.source_id=? AND a.kind IN ('image','raw')
	 AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
	 AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
	 ORDER BY a.id LIMIT 401`, id, id, id, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		if err = scanAsset(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if len(out) > 400 {
		return nil, ErrInvalid
	}
	return out, rows.Err()
}
