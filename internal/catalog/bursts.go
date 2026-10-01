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
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.captured_at,COALESCE(x.taken,''),COALESCE(x.model,''),COALESCE(e.perceptual_hash,'')
 FROM assets a LEFT JOIN exposures x ON x.asset_id=a.id AND x.size_bytes=a.size_bytes LEFT JOIN asset_evidence e ON e.asset_id=a.id
 WHERE substr(a.relative_path,1,?)=? AND a.kind IN ('image','raw')
 AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
 AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
 ORDER BY abs(a.id-?) LIMIT 2000`, len(folder), folder, id)
	if err != nil {
		return nil, err
	}
	type fact struct {
		id, at       int64
		camera, hash string
	}
	facts := []fact{}
	var root fact
	for rows.Next() {
		var f fact
		var taken string
		var at int64
		if err = rows.Scan(&f.id, &at, &taken, &f.camera, &f.hash); err != nil {
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
	available := map[int64]bool{}
	for _, f := range facts {
		available[f.id] = true
	}
	reasons := map[int64]string{id: "Selected photo"}
	related, err := s.Related(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, a := range related {
		if a.ID != id && available[a.ID] {
			reasons[a.ID] = "Related filename"
		}
	}
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
		if visuallyClose(root.hash, f.hash) {
			reasons[f.id] = "Similar visual fingerprint"
		}
	}
	ids := make([]int64, 0, len(reasons))
	for candidate := range reasons {
		ids = append(ids, candidate)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i] == id {
			return true
		}
		if ids[j] == id {
			return false
		}
		return ids[i] < ids[j]
	})
	if len(ids) > 40 {
		ids = ids[:40]
	}
	// Include the companions before marking stacks, so the 40-shot window can
	// never split a RAW from its exports at the boundary.
	pool := map[int64]*Asset{}
	for _, candidate := range ids {
		peers, e := s.Related(ctx, candidate)
		if e != nil {
			return nil, e
		}
		for _, peer := range peers {
			if peer.Kind != "image" && peer.Kind != "raw" {
				continue
			}
			var missing bool
			if e = s.read.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM missing_assets WHERE asset_id=?)", peer.ID).Scan(&missing); e != nil {
				return nil, e
			}
			if missing {
				continue
			}
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
	for _, candidate := range ids {
		a := pool[candidate]
		if a == nil || seen[candidate] {
			continue
		}
		group := append([]int64{candidate}, a.Stack...)
		if len(out)+len(group) > 400 {
			break
		}
		for _, member := range group {
			if seen[member] {
				continue
			}
			copy := *pool[member]
			copy.ComparisonReason = reasons[candidate]
			out = append(out, copy)
			seen[member] = true
		}
	}
	return out, nil
}
