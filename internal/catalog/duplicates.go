package catalog

import (
	"context"
	"sort"
	"strconv"
)

// DuplicateMember is one file of a group of copies, with the day it is filed
// under.
type DuplicateMember struct {
	Asset
	// Day is the day the file is filed under, as YYYY-MM-DD.
	Day string `json:"day"`
	// Located is true when a video records where it was taken. It is only
	// read for videos compared by their footage, and is false for the rest.
	Located bool `json:"located"`
	// Sidecars is what the copy's own sidecars record, the ones that go to the
	// Bin with it. It is left out when they could not be read in time, or
	// there is no archive mount to read them from, which is not the same as a
	// copy with no sidecars.
	Sidecars *SidecarFacts `json:"sidecars,omitempty"`
}

// DuplicateGroup is a set of files proven to be copies of one another. A
// group already settled, with every copy but one removed, is not listed.
type DuplicateGroup struct {
	// Hash names the group: the full hash the files share, or for copies
	// proven by their footage, "footage:" and the footage hash.
	Hash string `json:"hash"`
	// Proof is how the copies are known to be copies: "bytes" when every file
	// is byte-identical on a full hash, or "footage" when they are videos
	// holding the same pictures and sound, played the same way, whose
	// metadata differs.
	Proof string `json:"proof"`
	// Size is each file's size, in bytes, or for copies proven by their
	// footage, the largest file's.
	Size int64 `json:"size"`
	// Reclaimable is the space keeping one copy would free, in bytes, if the
	// largest is the one kept.
	Reclaimable int64 `json:"reclaimable"`
	// Members are the copies still in the archive, ordered by day and then
	// path.
	Members []DuplicateMember `json:"members"`
}

// ExactDuplicates returns the groups of files proven to be copies: files that
// are byte-identical on a cached full hash, and videos whose footage is
// identical, as footage.go proves it. A file proven a copy of another either
// way is in that file's group. A month-day filter selects groups touching that
// calendar date while retaining every copy elsewhere in the archive so the
// reviewer can make one informed choice.
func (s *Store) ExactDuplicates(ctx context.Context, md string, limit int) ([]DuplicateGroup, error) {
	if md != "" {
		if _, ok := validMonthDay(md); !ok {
			return nil, ErrInvalid
		}
	}
	return s.copyGroups(ctx, limit, func(member DuplicateMember) bool {
		return md == "" || (len(member.Day) == 10 && member.Day[5:] == md)
	})
}

// SocialDuplicates returns the groups of copies, proven as ExactDuplicates
// proves them, that hold a video still waiting on the Saved from social page,
// with every copy of it wherever it is filed.
func (s *Store) SocialDuplicates(ctx context.Context, limit int) ([]DuplicateGroup, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT social.asset_id FROM social_items social LEFT JOIN decisions d ON d.asset_id=social.asset_id
		WHERE social.state='waiting'`+socialPending)
	if err != nil {
		return nil, err
	}
	waiting := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		waiting[id] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return s.copyGroups(ctx, limit, func(member DuplicateMember) bool { return waiting[member.ID] })
}

// copyGroups returns the groups of copies with a file for which touches is
// true.
func (s *Store) copyGroups(ctx context.Context, limit int, touches func(DuplicateMember) bool) ([]DuplicateGroup, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, `WITH live AS (
		SELECT a.id,a.size_bytes FROM assets a
		 WHERE a.size_bytes>0
		   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
		   -- Counted only where it can be shown: a copy with no day, such as one
		   -- the archive scan found gone, would leave a "group" of one.
		   AND EXISTS(SELECT 1 FROM asset_days d WHERE d.asset_id=a.id)
	),
	byte_keys AS (
		SELECT e.full_hash AS hash,l.size_bytes AS size FROM live l JOIN asset_evidence e ON e.asset_id=l.id
		 WHERE e.full_hash IS NOT NULL AND e.full_hash!=''
		 GROUP BY e.full_hash,l.size_bytes HAVING count(*)>1
	),
	footage_keys AS (
		SELECT f.footage_hash AS hash FROM live l JOIN asset_footage f ON f.asset_id=l.id AND f.size_bytes=l.size_bytes
		 WHERE f.footage_hash IS NOT NULL
		 GROUP BY f.footage_hash HAVING count(*)>1
	)
	SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,
	       ad.day,CASE WHEN bk.hash IS NULL THEN '' ELSE bk.hash END,CASE WHEN fk.hash IS NULL THEN '' ELSE fk.hash END,COALESCE(f.located,0)
	  FROM live l
	  JOIN assets a ON a.id=l.id
	  JOIN asset_days ad ON ad.asset_id=a.id
	  LEFT JOIN decisions d ON d.asset_id=a.id
	  LEFT JOIN asset_evidence e ON e.asset_id=a.id
	  LEFT JOIN byte_keys bk ON bk.hash=e.full_hash AND bk.size=a.size_bytes
	  LEFT JOIN asset_footage f ON f.asset_id=a.id AND f.size_bytes=a.size_bytes
	  LEFT JOIN footage_keys fk ON fk.hash=f.footage_hash
	 WHERE bk.hash IS NOT NULL OR fk.hash IS NOT NULL
	 ORDER BY a.relative_path,ad.day`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Each file joins the files that share its full hash and the files that
	// share its footage hash, and a group is every file joined this way.
	type copyFile struct {
		member                   DuplicateMember
		fullHash, bytes, footage string
	}
	var files []copyFile
	seen := make(map[int64]bool)
	parent := make(map[string]string)
	var find func(string) string
	find = func(key string) string {
		for parent[key] != key {
			parent[key] = parent[parent[key]]
			key = parent[key]
		}
		return key
	}
	join := func(a, b string) {
		for _, key := range []string{a, b} {
			if _, ok := parent[key]; !ok {
				parent[key] = key
			}
		}
		if ra, rb := find(a), find(b); ra != rb {
			// The group takes the smaller name, so it is the same whichever
			// order the files come in.
			if rb < ra {
				ra, rb = rb, ra
			}
			parent[rb] = ra
		}
	}
	for rows.Next() {
		var file copyFile
		var member = &file.member
		if err = rows.Scan(&member.Asset.ID, &member.Asset.Path, &member.Asset.CapturedAt, &member.Asset.Kind, &member.Asset.Size, &member.Asset.Status, &member.Asset.Favourite, &member.Asset.Revision, &member.Asset.Source, &member.Asset.AlternativeCount, &member.Asset.RelatedCount, &member.Day, &file.bytes, &file.footage, &member.Located); err != nil {
			return nil, err
		}
		// A file filed under two days is one copy, shown under its first.
		if seen[member.ID] {
			continue
		}
		seen[member.ID] = true
		if file.bytes != "" {
			file.fullHash = file.bytes
			file.bytes = "bytes:" + file.bytes + ":" + strconv.FormatInt(member.Size, 10)
		}
		if file.footage != "" {
			file.footage = "footage:" + file.footage
		}
		self := "file:" + strconv.FormatInt(member.ID, 10)
		for _, key := range []string{file.bytes, file.footage} {
			if key != "" {
				join(self, key)
			}
		}
		files = append(files, file)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	type building struct {
		group    DuplicateGroup
		byteKeys map[string]bool
		fullHash string
		footage  string
		total    int64
		removed  int
		touches  bool
	}
	var order []string
	built := make(map[string]*building)
	for _, file := range files {
		root := find("file:" + strconv.FormatInt(file.member.ID, 10))
		entry := built[root]
		if entry == nil {
			entry = &building{byteKeys: make(map[string]bool)}
			built[root] = entry
			order = append(order, root)
		}
		entry.group.Members = append(entry.group.Members, file.member)
		entry.byteKeys[file.bytes] = true
		entry.fullHash = file.fullHash
		if file.footage != "" && (entry.footage == "" || file.footage < entry.footage) {
			entry.footage = file.footage
		}
		entry.total += file.member.Size
		entry.group.Size = max(entry.group.Size, file.member.Size)
		if file.member.Status == "cull" {
			entry.removed++
		}
		if touches(file.member) {
			entry.touches = true
		}
	}
	groups := make([]DuplicateGroup, 0)
	for _, root := range order {
		entry := built[root]
		members := entry.group.Members
		// Every copy but one removed leaves nothing to choose, whether the one
		// left was kept or is still to be decided.
		if len(members) < 2 || !entry.touches || entry.removed == len(members)-1 {
			continue
		}
		group := entry.group
		group.Reclaimable = entry.total - group.Size
		// One full hash for every file makes them byte-identical, whatever
		// else joined them.
		if len(entry.byteKeys) == 1 && !entry.byteKeys[""] {
			group.Proof = "bytes"
			group.Hash = entry.fullHash
		} else {
			group.Proof = "footage"
			group.Hash = entry.footage
		}
		sort.SliceStable(group.Members, func(i, j int) bool {
			if group.Members[i].Day != group.Members[j].Day {
				return group.Members[i].Day < group.Members[j].Day
			}
			return group.Members[i].Path < group.Members[j].Path
		})
		groups = append(groups, group)
		if len(groups) == limit {
			break
		}
	}
	return groups, nil
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
	   AND a.id NOT IN (` + liveClipAssets + `)
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
