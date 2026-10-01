package catalog

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// An iPhone writes HEIC, and a JPEG of the same shot often lands beside it
// with the same name: sent as Most Compatible, exported, or saved by an app.
// The day page shows the two as one photo, the HEIC in front, as it does a
// RAW and its exports, and offers them as copies to keep one of, which it
// does not for a RAW. A name alone is not enough, as different pictures share
// one often enough ("2024-11-20(9).heic"), so a pair is one photo only when
// both files record the same moment, to a fraction of a second, from the same
// camera. The index finds the pairs by name; this file reads what each file
// records and proves them.

// provenFormatPairs lists the HEIC and JPEG of each pair whose metadata,
// read at the size each file has now, records the same moment taken, to the
// fraction of a second, and the same camera.
const provenFormatPairs = `SELECT fp.heic_id,fp.jpeg_id FROM format_pairs fp
	JOIN assets h ON h.id=fp.heic_id JOIN exposures eh ON eh.asset_id=h.id AND eh.size_bytes=h.size_bytes
	JOIN assets j ON j.id=fp.jpeg_id JOIN exposures ej ON ej.asset_id=j.id AND ej.size_bytes=j.size_bytes
	WHERE eh.taken!='' AND eh.subsec!='' AND eh.model!='' AND ej.taken=eh.taken AND ej.subsec=eh.subsec AND ej.model=eh.model`

// FillExposures reads capture time and camera for up to 500 photographs, for
// the files not read yet or changed since, and returns how many it recorded.
// A file that cannot be opened is left for a later pass; one the reader
// cannot make sense of is recorded with nothing, which proves nothing.
func (s *Store) FillExposures(ctx context.Context, roots MediaRoots) (int, error) {
	// Sample the remaining backlog so unreadable files cannot occupy the first
	// 500 slots forever and prevent older photographs from being indexed.
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.size_bytes FROM assets a
		LEFT JOIN exposures x ON x.asset_id=a.id
		WHERE a.kind IN ('image','raw')
		AND (x.asset_id IS NULL OR x.size_bytes<>a.size_bytes)
		AND NOT EXISTS (SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')
		AND NOT EXISTS (SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
		ORDER BY random() LIMIT 500`)
	if err != nil {
		return 0, err
	}
	var targets []shapeTarget
	for rows.Next() {
		var target shapeTarget
		if err = rows.Scan(&target.id, &target.relative, &target.size); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, target)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(targets) == 0 {
		return 0, nil
	}
	return readEach(ctx, targets, func(target shapeTarget) (bool, error) {
		out, ok := readMetadata(ctx, roots, target, "-DateTimeOriginal", "-SubSecTimeOriginal", "-Model")
		if !ok {
			return false, nil
		}
		taken, subsec, model := parseExposure(out)
		_, err := s.write.ExecContext(ctx, `INSERT INTO exposures(asset_id,size_bytes,taken,subsec,model) VALUES(?,?,?,?,?)
			ON CONFLICT(asset_id) DO UPDATE SET size_bytes=excluded.size_bytes,taken=excluded.taken,subsec=excluded.subsec,model=excluded.model`,
			target.id, target.size, taken, subsec, model)
		return err == nil, err
	})
}

// parseExposure reads the metadata reader's answer: the moment the picture
// was taken, its fraction of a second, and the camera's model, each empty
// when the file does not record it. The reader gives a value as a number or
// as text depending on how it looks, so both are read as the text written.
func parseExposure(out []byte) (taken, subsec, model string) {
	var answers []map[string]json.RawMessage
	if json.Unmarshal(out, &answers) != nil || len(answers) != 1 {
		return "", "", ""
	}
	text := func(name string) string {
		raw := answers[0][name]
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			return number.String()
		}
		return ""
	}
	taken = text("DateTimeOriginal")
	// A camera with no clock set records zeros, which every such camera
	// shares.
	if strings.HasPrefix(taken, "0000") {
		taken = ""
	}
	return taken, text("SubSecTimeOriginal"), text("Model")
}

// sameExposures returns each HEIC and JPEG proven one exposure, with a file
// on the month and day md, as a group of copies to keep one of: the HEIC
// first, as the one kept unless the reviewer chooses otherwise. A pair shown
// apart in Settings, or split by the reviewer, is still one exposure, and is
// listed too. A pair with one file removed, or with a file gone, is settled.
func (s *Store) sameExposures(ctx context.Context, md string) ([]DuplicateGroup, error) {
	rows, err := s.read.QueryContext(ctx, `WITH pairs AS (`+provenFormatPairs+`)
	SELECT p.heic_id,a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,ad.day
	  FROM pairs p
	  JOIN assets a ON a.id IN (p.heic_id,p.jpeg_id)
	  JOIN asset_days ad ON ad.asset_id=a.id
	  LEFT JOIN decisions d ON d.asset_id=a.id
	 WHERE EXISTS(SELECT 1 FROM asset_days x WHERE x.asset_id IN (p.heic_id,p.jpeg_id) AND substr(x.day,6,5)=?)
	   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id IN (p.heic_id,p.jpeg_id) AND fs.state!='restored')
	 ORDER BY ad.day,p.heic_id,a.id=p.jpeg_id`, md)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var order []int64
	byHEIC := make(map[int64]*DuplicateGroup)
	seen := make(map[int64]bool)
	for rows.Next() {
		var heic int64
		var member DuplicateMember
		if err = rows.Scan(&heic, &member.Asset.ID, &member.Asset.Path, &member.Asset.CapturedAt, &member.Asset.Kind, &member.Asset.Size, &member.Asset.Status, &member.Asset.Favourite, &member.Asset.Revision, &member.Asset.Source, &member.Asset.AlternativeCount, &member.Asset.RelatedCount, &member.Day); err != nil {
			return nil, err
		}
		// A file filed under two days is one copy, shown under its first.
		if seen[member.ID] {
			continue
		}
		seen[member.ID] = true
		group := byHEIC[heic]
		if group == nil {
			group = &DuplicateGroup{Hash: "exposure:" + strconv.FormatInt(heic, 10), Proof: "exposure"}
			byHEIC[heic] = group
			order = append(order, heic)
		}
		group.Members = append(group.Members, member)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	groups := make([]DuplicateGroup, 0, len(order))
	for _, heic := range order {
		group := *byHEIC[heic]
		if len(group.Members) != 2 || group.Members[0].Status == "cull" || group.Members[1].Status == "cull" {
			continue
		}
		// The HEIC is kept unless the reviewer chooses the JPEG.
		sort.SliceStable(group.Members, func(i, j int) bool { return group.Members[i].ID == heic })
		group.Size = max(group.Members[0].Size, group.Members[1].Size)
		group.Reclaimable = group.Members[1].Size
		groups = append(groups, group)
	}
	return groups, nil
}

// stackedPartners counts the files that show behind another as one photo,
// among the files for which where, a condition on assets a, is true: an
// export behind its RAW, or a JPEG behind its HEIC. A file split from its
// stack is counted by itself.
func (s *Store) stackedPartners(ctx context.Context, where string) (int64, error) {
	var stacked int64
	err := s.read.QueryRowContext(ctx, `WITH partners AS (
		SELECT raw_id AS lead,export_id AS other FROM raw_stacks
		UNION ALL SELECT heic_id,jpeg_id FROM (`+provenFormatPairs+`)
	)
	SELECT count(*) FROM partners p
	 WHERE EXISTS(SELECT 1 FROM assets a WHERE a.id=p.lead AND `+where+`)
	   AND EXISTS(SELECT 1 FROM assets a WHERE a.id=p.other AND `+where+`)
	   AND NOT EXISTS(SELECT 1 FROM raw_pair_splits sp WHERE sp.raw_id=p.lead AND sp.partner_id=p.other)`).Scan(&stacked)
	return stacked, err
}
