package catalog

import (
	"context"
	"strings"
)

// MarkedAsset is a file a reviewer has marked for the Bin, with the moment the
// mark was made.
type MarkedAsset struct {
	Asset
	MarkedAt string `json:"markedAt"`
}

// MarkedForBin lists what is waiting to be moved, most recently marked first.
//
// Ordering by the mark rather than by capture date is the whole point. A
// reviewer who has just marked three clips comes straight here to carry them
// out, and finding them means finding the newest thing on the page. Ordered by
// when the photograph was taken they land wherever their year puts them, in
// among marks made weeks ago, which reads as the mark not having been recorded.
//
// Rows imported from the earlier tool have no event and so no time; they sort
// after everything marked in this tool, by capture date as before.
//
// Only archive files are listed. Screenshots and Google Takeout sources have
// their own routes out and are never offered to this writer.
func (s *Store) MarkedForBin(ctx context.Context, limit int) ([]MarkedAsset, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	const markedAt = `(SELECT MAX(e.created_at) FROM decision_events e WHERE e.asset_id=a.id AND e.status='cull')`
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,COALESCE(`+markedAt+`,'')
	 FROM assets a JOIN decisions d ON d.asset_id=a.id
	 WHERE d.status='cull' AND a.source_id='archive'
	   AND NOT EXISTS (SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	 ORDER BY `+markedAt+` DESC, a.captured_at DESC, a.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marked := make([]MarkedAsset, 0)
	for rows.Next() {
		var item MarkedAsset
		a := &item.Asset
		if err = rows.Scan(&a.ID, &a.Path, &a.CapturedAt, &a.Kind, &a.Size, &a.Status, &a.Favourite, &a.Revision, &a.Source, &a.AlternativeCount, &a.RelatedCount, &item.MarkedAt); err != nil {
			return nil, err
		}
		item.MarkedAt = markedTimestamp(item.MarkedAt)
		marked = append(marked, item)
	}
	return marked, rows.Err()
}

// markedTimestamp normalises the two shapes of time the events table holds into
// one a browser reads correctly. SQLite's CURRENT_TIMESTAMP is UTC but says so
// nowhere, and a browser reads an unmarked timestamp as local time, which would
// report a mark made a minute ago as hours old or hours away.
func markedTimestamp(value string) string {
	if value == "" || strings.ContainsAny(value, "Z+") || strings.Count(value, "-") > 2 {
		return value
	}
	return strings.Replace(value, " ", "T", 1) + "Z"
}
