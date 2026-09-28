package catalog

import (
	"context"
	"database/sql"
)

// ShadowMember is one copy in a ShadowGroup: a file on one disk.
type ShadowMember struct {
	Asset
	// Disk is the disk the copy is on, such as disk1 or cache.
	Disk string `json:"disk"`
	// RelativePath is where the copy is on its disk, relative to the disk's
	// root.
	RelativePath string `json:"relativePath"`
	// FullHash is a hash of the whole file's contents, in hexadecimal. It is
	// left out until the file has been hashed.
	FullHash string `json:"fullHash,omitempty"`
}

// ShadowGroup is a set of files the merged share cannot tell apart, found by
// the earlier app's scan of the disks.
type ShadowGroup struct {
	// Kind is shadowed for one path held on more than one disk, of which the
	// share shows only one, or case-only for paths that differ only in the
	// case of their letters.
	Kind string `json:"kind"`
	// Key is the path the group shares: for shadowed, the path on every
	// disk; for case-only, one of its spellings.
	Key string `json:"key"`
	// Verified is true when every member has been hashed and all of them
	// have the same hash and size.
	Verified bool `json:"verified"`
	// Size is the first member's size in bytes.
	Size int64 `json:"size"`
	// Reclaimable is how many bytes keeping only one copy would free: Size
	// for each member after the first. It is 0 unless Verified.
	Reclaimable int64 `json:"reclaimable"`
	// Members are the copies, by disk and then path.
	Members []ShadowMember `json:"members"`
}

// LegacyBinItem is one file the earlier app moved into its Bin, from its
// history as imported.
type LegacyBinItem struct {
	// ID is the file's id in the earlier app's Bin; send it to plan
	// restoring or deleting the file.
	ID int64 `json:"id"`
	// Batch names the files the earlier app moved together, a photograph
	// and its sidecars. A plan always takes the whole batch.
	Batch string `json:"batch"`
	// Kind is media for a photograph or video, or sidecar.
	Kind string `json:"kind"`
	// Original is where the file was before it was moved, and where restoring
	// puts it back, such as /disks/disk1/2021/2021-12/2021-12-18/IMG_0016.mp4.
	Original string `json:"original"`
	// Stored is where the earlier app put it, in the .culled folder of its
	// disk, such as /disks/disk1/.culled/2021-12-18/IMG_0016.mp4.
	Stored string `json:"stored"`
	// Day is the day the file is filed under, as 2021-12-18, or undated. It
	// is left out when the earlier app recorded none.
	Day string `json:"day,omitempty"`
	// Size is the file's size in bytes.
	Size int64 `json:"size"`
	// Reason is why the earlier app moved it, such as culled in review. It
	// is left out when none was recorded.
	Reason string `json:"reason,omitempty"`
	// CulledAt is when the file was moved into the Bin, in ISO 8601 as the
	// earlier app recorded it, such as 2026-08-30T00:00:00+00:00.
	CulledAt string `json:"culledAt"`
	// RestoredAt is when the file was put back, left out until it is.
	RestoredAt string `json:"restoredAt,omitempty"`
	// PurgedAt is when the file was deleted for good, left out until it is.
	PurgedAt string `json:"purgedAt,omitempty"`
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
