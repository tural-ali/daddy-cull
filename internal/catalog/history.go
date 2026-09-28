package catalog

import "context"

// HistoryEvent is one choice saved, as the Log shows it.
type HistoryEvent struct {
	// RequestID is the id the choice was sent with.
	RequestID string `json:"requestId"`
	// Asset is the file as it is now, with its current status.
	Asset Asset `json:"asset"`
	// Status is the status this choice saved: unreviewed, keep, later or cull.
	Status string `json:"status"`
	// Favourite is whether the file had a heart after this choice.
	Favourite bool `json:"favourite"`
	// PreviousStatus is the status the choice replaced.
	PreviousStatus string `json:"previousStatus"`
	// PreviousFavourite is whether the file had a heart before the choice.
	PreviousFavourite bool `json:"previousFavourite"`
	// CreatedAt is when the choice was saved, as YYYY-MM-DDTHH:MM:SSZ in UTC.
	CreatedAt string `json:"createdAt"`
	// Where the file is now when it is no longer in its day folder: "bin";
	// "deleting" once it is deleted from the Bin and waits out its days
	// there; "deleted" once it is gone.
	Where string `json:"where,omitempty"`
}

// eventTime renders an event's time in one UTC form a browser reads exactly.
// The table holds two shapes: SQLite's CURRENT_TIMESTAMP, which is UTC but says
// so nowhere, and RFC 3339 from the history imported from the earlier tool. They
// also sort wrongly against each other as text, since a 'T' outranks a space. A
// value SQLite cannot read is passed through rather than lost.
func eventTime(column string) string {
	return "COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ'," + column + ")," + column + ")"
}

// History lists saved choices newest first, a page at a time: offset skips
// that many of the newest, so the Log can read back to the very first.
func (s *Store) History(ctx context.Context, limit, offset int) ([]HistoryEvent, error) {
	if limit < 1 || limit > 500 || offset < 0 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,e.request_id,e.status,e.favourite,e.previous_status,e.previous_favourite,`+eventTime("e.created_at")+`,COALESCE((SELECT CASE WHEN fs.state='bin' AND EXISTS(SELECT 1 FROM trash_deletions td WHERE td.grp='bin:'||fs.plan_id) THEN 'deleting' ELSE fs.state END FROM file_state fs WHERE fs.asset_id=a.id),'') FROM decision_events e JOIN assets a ON a.id=e.asset_id LEFT JOIN decisions d ON d.asset_id=a.id ORDER BY `+eventTime("e.created_at")+` DESC, e.rowid DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]HistoryEvent, 0)
	for rows.Next() {
		var event HistoryEvent
		var state string
		if err = rows.Scan(&event.Asset.ID, &event.Asset.Path, &event.Asset.CapturedAt, &event.Asset.Kind, &event.Asset.Size, &event.Asset.Status, &event.Asset.Favourite, &event.Asset.Revision, &event.Asset.Source, &event.Asset.AlternativeCount, &event.Asset.RelatedCount, &event.RequestID, &event.Status, &event.Favourite, &event.PreviousStatus, &event.PreviousFavourite, &event.CreatedAt, &state); err != nil {
			return nil, err
		}
		switch state {
		case "bin", "quarantining":
			event.Where = "bin"
		case "deleting":
			event.Where = "deleting"
		case "purged", "purged_recovered":
			event.Where = "deleted"
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
