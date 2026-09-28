package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// Some changes reach the catalogue with nobody on a page to see them: files
// graduating from iCloud at night, and photos deleted on a phone being marked
// for the Bin. Each such run is kept as a notification, which the frame's bell
// lists until someone has seen it.
//
// A file that arrives is recorded in asset_arrivals. A day already reviewed
// that gains one is opened again, so the calendar shows it waiting, and its
// date carries a red dot until the date is opened, or until every file that
// arrived after the day was last reviewed has been decided.

type NotificationDay struct {
	Day   string `json:"day"`
	MD    string `json:"md"`
	Files int    `json:"files"`
	// Reopened says the day had been reviewed and was opened again by the
	// arrival; Fresh that files from it are still waiting now.
	Reopened bool `json:"reopened"`
	Fresh    int  `json:"fresh"`
}

type Notification struct {
	ID        int64             `json:"id"`
	Kind      string            `json:"kind"`
	CreatedAt string            `json:"createdAt"`
	Files     int               `json:"files"`
	Bytes     int64             `json:"bytes"`
	Read      bool              `json:"read"`
	Days      []NotificationDay `json:"days"`
}

type Notifications struct {
	Unread int            `json:"unread"`
	Items  []Notification `json:"items"`
}

// freshArrivalFrom joins an arrival to its day, the day's review and the
// file's decision; freshArrival then holds for a file nobody has seen on its
// date's page yet that arrived after its day was last reviewed, is still on
// disk and has not been decided. A day reviewed, reopened and reviewed again
// is judged by the later review.
const freshArrivalFrom = ` FROM asset_arrivals aa
	JOIN asset_days ad ON ad.asset_id=aa.asset_id
	LEFT JOIN day_progress dp ON dp.day=ad.day
	LEFT JOIN decisions d ON d.asset_id=aa.asset_id`

const freshArrival = ` aa.seen_at IS NULL AND COALESCE(dp.status,'pending')!='done'
	AND COALESCE(d.status,'unreviewed')='unreviewed' AND COALESCE(d.favourite,0)=0
	AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=aa.asset_id AND fs.state!='restored')
	AND julianday(aa.arrived_at)>max(COALESCE(julianday(dp.reviewed_at),0),
		COALESCE((SELECT max(julianday(e.created_at)) FROM day_progress_events e WHERE e.day=ad.day AND e.status='done'),0))`

// The layouts of reviewed_at and of event times, to the millisecond. Both
// read back through julianday and parseActivity.
const (
	reviewStamp = "2006-01-02T15:04:05.000Z"
	eventStamp  = "2006-01-02 15:04:05.000"
)

// arrival is one file a scan added, with the day it files under.
type arrival struct {
	id   int64
	day  string
	size int64
}

// recordArrivalsTx keeps the files one scan added as a notification, inside
// the scan's own transaction, and opens again every reviewed day they land on.
// The day's reviewed_at is left in place: the review happened, and the streak
// still counts it.
func recordArrivalsTx(ctx context.Context, tx *sql.Tx, arrivals []arrival, now time.Time) error {
	if len(arrivals) == 0 {
		return nil
	}
	stamp := now.UTC().Format(eventStamp)
	perDay := map[string]int{}
	var bytes int64
	for _, a := range arrivals {
		bytes += a.size
		if a.day != "" {
			perDay[a.day]++
		}
	}
	r, err := tx.ExecContext(ctx, "INSERT INTO notifications(kind,created_at,files,bytes) VALUES('arrivals',?,?,?)", stamp, len(arrivals), bytes)
	if err != nil {
		return err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return err
	}
	days := make([]string, 0, len(perDay))
	for day := range perDay {
		days = append(days, day)
	}
	sort.Strings(days)
	for _, day := range days {
		status := "pending"
		if err = tx.QueryRowContext(ctx, "SELECT status FROM day_progress WHERE day=?", day).Scan(&status); err != nil && err != sql.ErrNoRows {
			return err
		}
		reopened := status == "done"
		if reopened {
			if _, err = tx.ExecContext(ctx, "UPDATE day_progress SET status='pending' WHERE day=?", day); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO day_progress_events(request_id,day,status,previous_status,created_at) VALUES(?,?,'pending','done',?)", fmt.Sprintf("arrival-%d-%s", id, day), day, stamp); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO notification_days(notification_id,day,files,reopened) VALUES(?,?,?,?)", id, day, perDay[day], reopened); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO asset_arrivals(asset_id,arrived_at) VALUES(?,?) ON CONFLICT(asset_id) DO UPDATE SET arrived_at=excluded.arrived_at,seen_at=NULL")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range arrivals {
		if _, err = stmt.ExecContext(ctx, a.id, stamp); err != nil {
			return err
		}
	}
	return nil
}

// recordPhoneDeletions keeps one run's marks for the Bin as a notification.
func (s *Store) recordPhoneDeletions(ctx context.Context, marked int, bytes int64) error {
	if marked == 0 {
		return nil
	}
	_, err := s.write.ExecContext(ctx, "INSERT INTO notifications(kind,created_at,files,bytes) VALUES('phone-deletions',?,?,?)", time.Now().UTC().Format(eventStamp), marked, bytes)
	return err
}

// FreshDates counts, for each calendar date, the files that arrived after
// their day was last reviewed and still wait: the dates with a red dot.
func (s *Store) FreshDates(ctx context.Context) (map[string]int, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT substr(ad.day,6,5),count(*)`+freshArrivalFrom+` WHERE`+freshArrival+` GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fresh := map[string]int{}
	for rows.Next() {
		var md string
		var n int
		if err = rows.Scan(&md, &n); err != nil {
			return nil, err
		}
		fresh[md] = n
	}
	return fresh, rows.Err()
}

// freshOn lists the files on one calendar date that make its red dot.
func (s *Store) freshOn(ctx context.Context, md string) (map[int64]bool, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT aa.asset_id`+freshArrivalFrom+` WHERE substr(ad.day,6,5)=? AND`+freshArrival, md)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fresh := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		fresh[id] = true
	}
	return fresh, rows.Err()
}

// maxSeenArrivals bounds one date page's report of the new files it showed.
const maxSeenArrivals = 10000

// SeeArrivals records that a date's page showed these files as new, so the
// date loses its red dot and they are not new the next time. A file that
// arrives again afterwards is new again; see recordArrivalsTx.
func (s *Store) SeeArrivals(ctx context.Context, ids []int64) error {
	if len(ids) == 0 || len(ids) > maxSeenArrivals {
		return ErrInvalid
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE asset_arrivals SET seen_at=? WHERE asset_id=? AND seen_at IS NULL")
	if err != nil {
		return err
	}
	defer stmt.Close()
	stamp := time.Now().UTC().Format(eventStamp)
	for _, id := range ids {
		if id < 1 {
			return ErrInvalid
		}
		if _, err = stmt.ExecContext(ctx, stamp, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnreadNotifications is the count on the bell.
func (s *Store) UnreadNotifications(ctx context.Context) (int, error) {
	var n int
	err := s.read.QueryRowContext(ctx, "SELECT count(*) FROM notifications WHERE read_at IS NULL").Scan(&n)
	return n, err
}

// ListNotifications returns the newest notifications first, each with the
// days it touched and how many of their files still wait.
func (s *Store) ListNotifications(ctx context.Context, limit int) (Notifications, error) {
	result := Notifications{Items: make([]Notification, 0)}
	if limit < 1 || limit > 200 {
		return result, ErrInvalid
	}
	var err error
	if result.Unread, err = s.UnreadNotifications(ctx); err != nil {
		return result, err
	}
	rows, err := s.read.QueryContext(ctx, "SELECT id,kind,created_at,files,bytes,read_at IS NOT NULL FROM notifications ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return result, err
	}
	index := map[int64]int{}
	for rows.Next() {
		var n Notification
		if err = rows.Scan(&n.ID, &n.Kind, &n.CreatedAt, &n.Files, &n.Bytes, &n.Read); err != nil {
			rows.Close()
			return result, err
		}
		n.Days = make([]NotificationDay, 0)
		index[n.ID] = len(result.Items)
		result.Items = append(result.Items, n)
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	if len(result.Items) == 0 {
		return result, nil
	}
	freshByDay := map[string]int{}
	freshRows, err := s.read.QueryContext(ctx, `SELECT ad.day,count(*)`+freshArrivalFrom+` WHERE`+freshArrival+` GROUP BY 1`)
	if err != nil {
		return result, err
	}
	for freshRows.Next() {
		var day string
		var n int
		if err = freshRows.Scan(&day, &n); err != nil {
			freshRows.Close()
			return result, err
		}
		freshByDay[day] = n
	}
	if err = freshRows.Close(); err != nil {
		return result, err
	}
	oldest := result.Items[len(result.Items)-1].ID
	dayRows, err := s.read.QueryContext(ctx, "SELECT notification_id,day,files,reopened FROM notification_days WHERE notification_id>=? ORDER BY notification_id,day", oldest)
	if err != nil {
		return result, err
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var id int64
		var d NotificationDay
		if err = dayRows.Scan(&id, &d.Day, &d.Files, &d.Reopened); err != nil {
			return result, err
		}
		at, ok := index[id]
		if !ok {
			continue
		}
		d.MD = d.Day[5:]
		d.Fresh = freshByDay[d.Day]
		result.Items[at].Days = append(result.Items[at].Days, d)
	}
	return result, dayRows.Err()
}

// ReadNotifications marks every notification up to through as seen, so one
// opened while newer ones arrived leaves those unread.
func (s *Store) ReadNotifications(ctx context.Context, through int64) error {
	if through < 1 {
		return ErrInvalid
	}
	_, err := s.write.ExecContext(ctx, "UPDATE notifications SET read_at=? WHERE id<=? AND read_at IS NULL", time.Now().UTC().Format(eventStamp), through)
	return err
}
