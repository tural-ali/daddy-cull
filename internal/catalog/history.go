package catalog

import "context"

type HistoryEvent struct {
	RequestID         string `json:"requestId"`
	Asset             Asset  `json:"asset"`
	Status            string `json:"status"`
	Favourite         bool   `json:"favourite"`
	PreviousStatus    string `json:"previousStatus"`
	PreviousFavourite bool   `json:"previousFavourite"`
	CreatedAt         string `json:"createdAt"`
}

// eventTime renders an event's time in one UTC form a browser reads exactly.
// The table holds two shapes: SQLite's CURRENT_TIMESTAMP, which is UTC but says
// so nowhere, and RFC 3339 from the history imported from the earlier tool. They
// also sort wrongly against each other as text, since a 'T' outranks a space. A
// value SQLite cannot read is passed through rather than lost.
func eventTime(column string) string {
	return "COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ'," + column + ")," + column + ")"
}

func (s *Store) History(ctx context.Context, limit int) ([]HistoryEvent, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),`+relatedCount+`,e.request_id,e.status,e.favourite,e.previous_status,e.previous_favourite,`+eventTime("e.created_at")+` FROM decision_events e JOIN assets a ON a.id=e.asset_id LEFT JOIN decisions d ON d.asset_id=a.id ORDER BY `+eventTime("e.created_at")+` DESC, e.rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]HistoryEvent, 0)
	for rows.Next() {
		var event HistoryEvent
		if err = rows.Scan(&event.Asset.ID, &event.Asset.Path, &event.Asset.CapturedAt, &event.Asset.Kind, &event.Asset.Size, &event.Asset.Status, &event.Asset.Favourite, &event.Asset.Revision, &event.Asset.Source, &event.Asset.AlternativeCount, &event.Asset.RelatedCount, &event.RequestID, &event.Status, &event.Favourite, &event.PreviousStatus, &event.PreviousFavourite, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
