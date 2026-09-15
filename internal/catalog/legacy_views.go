package catalog

import (
	"context"
	"database/sql"
)

type ShadowMember struct {
	Asset
	Disk         string `json:"disk"`
	RelativePath string `json:"relativePath"`
	FullHash     string `json:"fullHash,omitempty"`
}

type ShadowGroup struct {
	Kind        string         `json:"kind"`
	Key         string         `json:"key"`
	Verified    bool           `json:"verified"`
	Size        int64          `json:"size"`
	Reclaimable int64          `json:"reclaimable"`
	Members     []ShadowMember `json:"members"`
}

type LegacyBinItem struct {
	ID         int64  `json:"id"`
	Batch      string `json:"batch"`
	Kind       string `json:"kind"`
	Original   string `json:"original"`
	Stored     string `json:"stored"`
	Day        string `json:"day,omitempty"`
	Size       int64  `json:"size"`
	Reason     string `json:"reason,omitempty"`
	CulledAt   string `json:"culledAt"`
	RestoredAt string `json:"restoredAt,omitempty"`
	PurgedAt   string `json:"purgedAt,omitempty"`
}

func (s *Store) ShadowGroups(ctx context.Context, limit int) ([]ShadowGroup, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,se.kind,se.group_key,se.disk,se.relative_path,COALESCE(evidence.full_hash,'') FROM shadow_entries se JOIN assets a ON a.id=se.asset_id LEFT JOIN decisions d ON d.asset_id=a.id LEFT JOIN asset_evidence evidence ON evidence.asset_id=a.id ORDER BY se.kind,se.group_key,se.disk,se.relative_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]ShadowGroup, 0)
	indices := make(map[string]int)
	for rows.Next() {
		var member ShadowMember
		var kind, key string
		if err = rows.Scan(&member.Asset.ID, &member.Asset.Path, &member.Asset.CapturedAt, &member.Asset.Kind, &member.Asset.Size, &member.Asset.Status, &member.Asset.Favourite, &member.Asset.Revision, &member.Asset.Source, &member.Asset.AlternativeCount, &member.Asset.RelatedCount, &kind, &key, &member.Disk, &member.RelativePath, &member.FullHash); err != nil {
			return nil, err
		}
		groupKey := kind + "\x00" + key
		index, exists := indices[groupKey]
		if !exists {
			if len(groups) >= limit {
				continue
			}
			index = len(groups)
			indices[groupKey] = index
			groups = append(groups, ShadowGroup{Kind: kind, Key: key, Verified: member.FullHash != "", Size: member.Size, Members: make([]ShadowMember, 0)})
		}
		group := &groups[index]
		if member.FullHash == "" || (len(group.Members) > 0 && member.FullHash != group.Members[0].FullHash) || member.Size != group.Size {
			group.Verified = false
		}
		group.Members = append(group.Members, member)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for index := range groups {
		if groups[index].Verified && len(groups[index].Members) > 1 {
			groups[index].Reclaimable = groups[index].Size * int64(len(groups[index].Members)-1)
		}
	}
	return groups, nil
}

func (s *Store) LegacyBin(ctx context.Context, limit int) ([]LegacyBinItem, error) {
	if limit < 1 || limit > 5000 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, "SELECT legacy_id,batch,kind,original_path,culled_path,COALESCE(day,''),size_bytes,COALESCE(reason,''),culled_at,COALESCE(restored_at,''),COALESCE(purged_at,'') FROM legacy_culled ORDER BY legacy_id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]LegacyBinItem, 0)
	for rows.Next() {
		var item LegacyBinItem
		if err = rows.Scan(&item.ID, &item.Batch, &item.Kind, &item.Original, &item.Stored, &item.Day, &item.Size, &item.Reason, &item.CulledAt, &item.RestoredAt, &item.PurgedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func countQuery(ctx context.Context, db *sql.DB, query string) int {
	var count int
	_ = db.QueryRowContext(ctx, query).Scan(&count)
	return count
}
