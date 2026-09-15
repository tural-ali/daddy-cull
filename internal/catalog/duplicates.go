package catalog

import (
	"context"
	"strconv"
)

type DuplicateMember struct {
	Asset
	Day string `json:"day"`
}

type DuplicateGroup struct {
	Hash        string            `json:"hash"`
	Size        int64             `json:"size"`
	Reclaimable int64             `json:"reclaimable"`
	Members     []DuplicateMember `json:"members"`
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
