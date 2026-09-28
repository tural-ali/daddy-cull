package catalog

import (
	"context"
	"fmt"
)

// DateTally is what the review of one calendar date came to, across every
// year filed under it. Files already emptied from the Bin, or purged, still
// count as removed: they were removed from this date, and a tally that forgot
// them would shrink every time the Bin was emptied.
type DateTally struct {
	// Total counts every file ever filed under the date, removed ones
	// included.
	Total int `json:"total"`
	// Removed counts the files removed: marked cull, in the Bin or deleted.
	Removed int `json:"removed"`
	// Bytes is the size of the removed files together, in bytes.
	Bytes int64 `json:"bytes"`
	// Kept counts the files kept that are still in the archive.
	Kept int `json:"kept"`
	// Favourites counts the files with a heart that are still in the archive.
	Favourites int `json:"favourites"`
}

// KeptAsset is a file that marking the date reviewed kept, with the revision
// the keep was saved at, so the page can undo it.
type KeptAsset struct {
	// ID is the file's id in the catalogue.
	ID int64 `json:"id"`
	// Revision is the file's revision after the keep; send it with a choice
	// that undoes it.
	Revision int64 `json:"revision"`
}

// DateReviewed is what marking a calendar date reviewed did.
type DateReviewed struct {
	// Days are the years' days this request marked reviewed, as YYYY-MM-DD,
	// oldest first. Days already reviewed are not listed.
	Days []string `json:"days"`
	// Kept are the files nobody had decided on, which this request kept.
	Kept []KeptAsset `json:"kept"`
	// Tally is what the review of the date came to.
	Tally DateTally `json:"tally"`
}

// MarkDateReviewed finishes a calendar date in one transaction: every file on
// it still undecided is kept, every year's day not yet reviewed is marked, and
// the tally is read back. Each keep is an ordinary journaled decision, so it
// shows in the Log and can be undone like any other.
//
// The request id makes a retry safe: the keeps and marks it derives are
// recorded under it, and a file or day already done is left alone.
func (s *Store) MarkDateReviewed(ctx context.Context, md, requestID string) (DateReviewed, error) {
	var result DateReviewed
	if _, ok := validMonthDay(md); !ok || len(requestID) < 8 || len(requestID) > 80 {
		return result, ErrInvalid
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	type undecided struct {
		id, revision int64
		favourite    bool
	}
	var open []undecided
	rows, err := tx.QueryContext(ctx, `SELECT a.id,COALESCE(d.revision,0),COALESCE(d.favourite,0)
		FROM asset_days ad JOIN assets a ON a.id=ad.asset_id LEFT JOIN decisions d ON d.asset_id=a.id
		WHERE substr(ad.day,6,5)=? AND COALESCE(d.status,'unreviewed')='unreviewed'
		AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
		ORDER BY a.captured_at,a.id`, md)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var u undecided
		if err = rows.Scan(&u.id, &u.revision, &u.favourite); err != nil {
			rows.Close()
			return result, err
		}
		open = append(open, u)
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	result.Kept = make([]KeptAsset, 0, len(open))
	for _, u := range open {
		saved, err := decideTx(ctx, tx, Decision{RequestID: fmt.Sprintf("%s-k%d", requestID, u.id), AssetID: u.id, ExpectedRevision: u.revision, Status: "keep", Favourite: u.favourite})
		if err != nil {
			return result, err
		}
		result.Kept = append(result.Kept, KeptAsset{ID: u.id, Revision: saved.Revision})
	}

	var days []string
	dayRows, err := tx.QueryContext(ctx, `SELECT DISTINCT ad.day FROM asset_days ad LEFT JOIN day_progress dp ON dp.day=ad.day
		WHERE substr(ad.day,6,5)=? AND COALESCE(dp.status,'pending')!='done'
		AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=ad.asset_id AND fs.state!='restored')
		ORDER BY ad.day`, md)
	if err != nil {
		return result, err
	}
	for dayRows.Next() {
		var day string
		if err = dayRows.Scan(&day); err != nil {
			dayRows.Close()
			return result, err
		}
		days = append(days, day)
	}
	if err = dayRows.Close(); err != nil {
		return result, err
	}
	result.Days = make([]string, 0, len(days))
	for _, day := range days {
		if _, err = setDayProgressTx(ctx, tx, DayProgressChange{Day: day, Status: "done", RequestID: requestID + "-" + day}); err != nil {
			return result, err
		}
		result.Days = append(result.Days, day)
	}

	if err = tx.QueryRowContext(ctx, `SELECT count(*),
		COALESCE(sum(gone OR status='cull'),0),
		COALESCE(sum(CASE WHEN gone OR status='cull' THEN size_bytes END),0),
		COALESCE(sum(NOT gone AND status='keep'),0),
		COALESCE(sum(NOT gone AND favourite=1),0)
		FROM (SELECT a.size_bytes,COALESCE(d.status,'unreviewed') status,COALESCE(d.favourite,0) favourite,
			EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored') gone
			FROM asset_days ad JOIN assets a ON a.id=ad.asset_id LEFT JOIN decisions d ON d.asset_id=a.id
			WHERE substr(ad.day,6,5)=?)`, md).Scan(&result.Tally.Total, &result.Tally.Removed, &result.Tally.Bytes, &result.Tally.Kept, &result.Tally.Favourites); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
