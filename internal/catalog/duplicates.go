package catalog

import (
	"context"
	"strconv"
)

// DuplicateMember is one file of a group of copies, with the day it is filed
// under.
type DuplicateMember struct {
	Asset
	// Day is the day the file is filed under, as YYYY-MM-DD.
	Day string `json:"day"`
}

// DuplicateGroup is a set of files whose bytes are identical, as proven by a
// full hash of each. A group already settled, with one copy kept and every
// other removed, is not listed.
type DuplicateGroup struct {
	// Hash is the full hash the files share, as the catalogue holds it.
	Hash string `json:"hash"`
	// Size is each file's size, in bytes.
	Size int64 `json:"size"`
	// Reclaimable is the space keeping one copy would free, in bytes: Size
	// times one less than the number of copies.
	Reclaimable int64 `json:"reclaimable"`
	// Members are the copies still in the archive, ordered by day and then
	// path.
	Members []DuplicateMember `json:"members"`
}

// ExactDuplicates only returns byte-identical files backed by a cached full hash.
// A month-day filter selects groups touching that calendar date while retaining
// every copy elsewhere in the archive so the reviewer can make one informed choice.
func (s *Store) ExactDuplicates(ctx context.Context, md string, limit int) ([]DuplicateGroup, error) {
	if md != "" {
		if _, ok := validMonthDay(md); !ok {
			return nil, ErrInvalid
		}
	}
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, `WITH duplicate_keys AS (
		SELECT evidence.full_hash AS hash,assets.size_bytes AS size,count(*) AS members,min(assets.relative_path) AS first_path
		  FROM asset_evidence evidence
		  JOIN assets ON assets.id=evidence.asset_id
		  LEFT JOIN decisions key_decisions ON key_decisions.asset_id=assets.id
		 WHERE evidence.full_hash IS NOT NULL AND evidence.full_hash!='' AND assets.size_bytes>0
		   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=assets.id AND fs.state!='restored')
		   -- Counted only where it can be shown: a copy with no day, such as one
		   -- the archive scan found gone, would leave a "group" of one.
		   AND EXISTS(SELECT 1 FROM asset_days key_day WHERE key_day.asset_id=assets.id)
		 GROUP BY evidence.full_hash,assets.size_bytes
		HAVING count(*)>1
		   AND NOT (sum(CASE WHEN key_decisions.status='cull' THEN 1 ELSE 0 END)=count(*)-1 AND sum(CASE WHEN key_decisions.status='keep' THEN 1 ELSE 0 END)=1)
		   AND EXISTS(
			SELECT 1 FROM asset_evidence touching
			JOIN assets touching_asset ON touching_asset.id=touching.asset_id
			JOIN asset_days touching_day ON touching_day.asset_id=touching.asset_id
			WHERE touching.full_hash=evidence.full_hash
			  AND touching_asset.size_bytes=assets.size_bytes
			  AND (?='' OR substr(touching_day.day,6,5)=?)
			  AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=touching_asset.id AND fs.state!='restored')
		)
		 ORDER BY first_path
		 LIMIT ?
	)
	SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,keys.hash,ad.day,keys.size,keys.members
	  FROM duplicate_keys keys
	  JOIN asset_evidence evidence ON evidence.full_hash=keys.hash
	  JOIN assets a ON a.id=evidence.asset_id AND a.size_bytes=keys.size
	  JOIN asset_days ad ON ad.asset_id=a.id
	  LEFT JOIN decisions d ON d.asset_id=a.id
	 WHERE NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	 ORDER BY keys.first_path,ad.day,a.relative_path`, md, md, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]DuplicateGroup, 0)
	indices := make(map[string]int)
	for rows.Next() {
		var member DuplicateMember
		var hash string
		var size, count int64
		if err = rows.Scan(&member.Asset.ID, &member.Asset.Path, &member.Asset.CapturedAt, &member.Asset.Kind, &member.Asset.Size, &member.Asset.Status, &member.Asset.Favourite, &member.Asset.Revision, &member.Asset.Source, &member.Asset.AlternativeCount, &member.Asset.RelatedCount, &hash, &member.Day, &size, &count); err != nil {
			return nil, err
		}
		key := hash + "\x00" + strconv.FormatInt(size, 10)
		index, exists := indices[key]
		if !exists {
			index = len(groups)
			indices[key] = index
			groups = append(groups, DuplicateGroup{Hash: hash, Size: size, Reclaimable: size * (count - 1), Members: make([]DuplicateMember, 0, count)})
		}
		groups[index].Members = append(groups[index].Members, member)
	}
	return groups, rows.Err()
}

// DuplicateCandidate is a set of live files that share a byte size but whose
// identity is not settled, because at least one of them has no cached full hash.
// Sharing a size is not evidence of anything on its own; it only marks the file
// as worth hashing, which is the point of showing it.
type DuplicateCandidate struct {
	// Size is the size the files share, in bytes.
	Size int64 `json:"size"`
	// Reclaimable is the most keeping one copy could free, in bytes, if every
	// file turned out identical: Size times one less than the number of files.
	Reclaimable int64 `json:"reclaimable"`
	// Hashed counts the files in the set that have a full hash.
	Hashed int `json:"hashed"`
	// Members are the files of this size, ordered by path.
	Members []DuplicateMember `json:"members"`
}

// DuplicateReport answers the question the page exists to answer, and says how
// far it can be trusted. Two files can only be byte-identical if they are the
// same length, so the files sharing a size with another file are the entire
// population that could contain a duplicate. Measuring coverage against that
// population rather than against the whole catalogue is what makes an empty
// result readable: when every candidate is hashed the answer is complete, even
// if almost nothing else in the archive has ever been hashed.
type DuplicateReport struct {
	// Groups are the proven groups of identical files, as GET /api/duplicates
	// lists them.
	Groups []DuplicateGroup `json:"groups"`
	// Unproven are the sets of files sharing a size where at least one has
	// no full hash yet, largest size first. The md filter does not apply.
	Unproven []DuplicateCandidate `json:"unproven"`
	// Candidates counts the files still in the archive that share their size
	// with another: every file that could be a copy.
	Candidates int `json:"candidates"`
	// Hashed counts the candidates that have a full hash.
	Hashed int `json:"hashed"`
	// Settled is true when every candidate is hashed, so the groups are the
	// whole answer.
	Settled bool `json:"settled"`
}

// liveCandidates is the shared definition of the population that could hold a
// byte-identical duplicate: files still present, with a real size, sharing that
// size with at least one other such file.
const liveCandidates = `live AS (
	SELECT a.id,a.size_bytes FROM assets a
	 WHERE a.size_bytes>0
	   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	   AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
),
colliding AS (SELECT size_bytes FROM live GROUP BY size_bytes HAVING count(*)>1)`

// DuplicateStatus reports how much of the duplicate question has been answered
// without listing anything, for callers that only need the coverage figures.
func (s *Store) DuplicateStatus(ctx context.Context) (candidates int, hashed int, err error) {
	err = s.read.QueryRowContext(ctx, `WITH `+liveCandidates+`
	SELECT count(*),COALESCE(sum(CASE WHEN e.full_hash IS NOT NULL AND e.full_hash!='' THEN 1 ELSE 0 END),0)
	  FROM live l LEFT JOIN asset_evidence e ON e.asset_id=l.id
	 WHERE l.size_bytes IN (SELECT size_bytes FROM colliding)`).Scan(&candidates, &hashed)
	return candidates, hashed, err
}

// UnprovenDuplicates lists the size groups a full hash has not settled, largest
// first, so the reviewer sees what the page still cannot answer rather than an
// empty screen that reads as an all-clear.
func (s *Store) UnprovenDuplicates(ctx context.Context, limit int) ([]DuplicateCandidate, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, `WITH `+liveCandidates+`,
	unsettled AS (
		SELECT l.size_bytes
		  FROM live l LEFT JOIN asset_evidence e ON e.asset_id=l.id
		 WHERE l.size_bytes IN (SELECT size_bytes FROM colliding)
		 GROUP BY l.size_bytes
		HAVING sum(CASE WHEN e.full_hash IS NOT NULL AND e.full_hash!='' THEN 1 ELSE 0 END)<count(*)
		 ORDER BY l.size_bytes DESC
		 LIMIT ?
	)
	SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,ad.day,(e.full_hash IS NOT NULL AND e.full_hash!='')
	  FROM unsettled u
	  JOIN assets a ON a.size_bytes=u.size_bytes
	  JOIN asset_days ad ON ad.asset_id=a.id
	  LEFT JOIN decisions d ON d.asset_id=a.id
	  LEFT JOIN asset_evidence e ON e.asset_id=a.id
	 WHERE NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	 ORDER BY a.size_bytes DESC,a.relative_path`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]DuplicateCandidate, 0)
	indices := make(map[int64]int)
	for rows.Next() {
		var member DuplicateMember
		var hashed bool
		if err = rows.Scan(&member.Asset.ID, &member.Asset.Path, &member.Asset.CapturedAt, &member.Asset.Kind, &member.Asset.Size, &member.Asset.Status, &member.Asset.Favourite, &member.Asset.Revision, &member.Asset.Source, &member.Asset.AlternativeCount, &member.Asset.RelatedCount, &member.Day, &hashed); err != nil {
			return nil, err
		}
		index, exists := indices[member.Asset.Size]
		if !exists {
			index = len(candidates)
			indices[member.Asset.Size] = index
			candidates = append(candidates, DuplicateCandidate{Size: member.Asset.Size})
		}
		if hashed {
			candidates[index].Hashed++
		}
		candidates[index].Members = append(candidates[index].Members, member)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// Reclaimable is the most that could be freed if every copy in the group
	// turned out to be identical. It is an upper bound, not a promise.
	for i := range candidates {
		candidates[i].Reclaimable = candidates[i].Size * int64(len(candidates[i].Members)-1)
	}
	return candidates, nil
}

// DuplicateOverview assembles the proven groups, the unsettled ones and the
// coverage figures in a single read, so the page never has to infer its own
// reliability from a bare group count.
func (s *Store) DuplicateOverview(ctx context.Context, md string, limit int) (DuplicateReport, error) {
	report := DuplicateReport{Groups: make([]DuplicateGroup, 0), Unproven: make([]DuplicateCandidate, 0)}
	groups, err := s.ExactDuplicates(ctx, md, limit)
	if err != nil {
		return report, err
	}
	unproven, err := s.UnprovenDuplicates(ctx, limit)
	if err != nil {
		return report, err
	}
	candidates, hashed, err := s.DuplicateStatus(ctx)
	if err != nil {
		return report, err
	}
	report.Groups, report.Unproven = groups, unproven
	report.Candidates, report.Hashed = candidates, hashed
	report.Settled = candidates == hashed
	return report, nil
}
