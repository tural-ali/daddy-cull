package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var archiveDayPattern = regexp.MustCompile(`(?:^|/)(\d{4}-\d{2}-\d{2})(?:/|$)`)
var datedFilenamePattern = regexp.MustCompile(`(?:^|/)(\d{4}-\d{2}-\d{2})_[^/]+$`)

type CalendarCell struct {
	MD      string `json:"md"`
	DOM     int    `json:"dom"`
	Years   int    `json:"years"`
	Files   int    `json:"files"`
	Done    int    `json:"done"`
	Waiting int    `json:"waiting"`
	State   string `json:"state"`
	Today   bool   `json:"today"`
	// Fresh counts files that arrived after the date was reviewed and still
	// wait: the red dot.
	Fresh int `json:"fresh,omitempty"`
}

type CalendarMonth struct {
	Name  string          `json:"name"`
	Cells []*CalendarCell `json:"cells"`
}

type CalendarProgress struct {
	Dates     int `json:"dates"`
	Done      int `json:"done"`
	Part      int `json:"part"`
	FilesDone int `json:"filesDone"`
	Files     int `json:"files"`
}

type CalendarData struct {
	Months    []CalendarMonth  `json:"months"`
	Progress  CalendarProgress `json:"prog"`
	Today     string           `json:"today"`
	Streak    int              `json:"streak"`
	Week      CalendarWeek     `json:"week"`
	Refreshed string           `json:"refreshed,omitempty"`
}

type CalendarWeek struct {
	Days    int `json:"days"`
	Seconds int `json:"seconds"`
}

type TodayYear struct {
	Day    string  `json:"day"`
	Year   int     `json:"year"`
	Files  int     `json:"files"`
	Bytes  int64   `json:"bytes"`
	Status string  `json:"status"`
	Assets []Asset `json:"assets"`
	// Fresh counts this year's files that arrived after it was reviewed and
	// still wait; each one is marked New.
	Fresh int `json:"fresh,omitempty"`
}

type TodayData struct {
	MD       string      `json:"md"`
	Label    string      `json:"label"`
	Previous string      `json:"previous"`
	Next     string      `json:"next"`
	Years    []TodayYear `json:"years"`
	Memories int         `json:"memories"`
	Bytes    int64       `json:"bytes"`
}

type DayProgressChange struct {
	Day       string `json:"day"`
	Status    string `json:"status"`
	RequestID string `json:"requestId"`
}

type DayProgressResult struct {
	Day            string `json:"day"`
	Status         string `json:"status"`
	PreviousStatus string `json:"previousStatus"`
}

func archiveDay(path string, capturedAt int64) (string, bool) {
	path = strings.ReplaceAll(path, `\`, "/")
	for _, match := range archiveDayPattern.FindAllStringSubmatch(path, -1) {
		if _, err := time.Parse("2006-01-02", match[1]); err == nil {
			return match[1], true
		}
	}
	if match := datedFilenamePattern.FindStringSubmatch(path); match != nil {
		if _, err := time.Parse("2006-01-02", match[1]); err == nil {
			return match[1], true
		}
	}
	if capturedAt > 0 {
		return time.Unix(capturedAt, 0).UTC().Format("2006-01-02"), true
	}
	return "", false
}

func (s *Store) IndexCalendar(ctx context.Context) error {
	rows, err := s.read.QueryContext(ctx, "SELECT id,relative_path,captured_at FROM assets WHERE source_id='archive' AND id NOT IN (SELECT asset_id FROM missing_assets)")
	if err != nil {
		return err
	}
	type indexedDay struct {
		id  int64
		day string
	}
	indexed := make([]indexedDay, 0)
	for rows.Next() {
		var id, capturedAt int64
		var path string
		if err = rows.Scan(&id, &path, &capturedAt); err != nil {
			rows.Close()
			return err
		}
		if day, ok := archiveDay(path, capturedAt); ok {
			indexed = append(indexed, indexedDay{id: id, day: day})
		}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM asset_days"); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range indexed {
		if _, err = stmt.ExecContext(ctx, item.id, item.day); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validMonthDay(md string) (time.Time, bool) {
	if len(md) != 5 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", "2000-"+md)
	return t, err == nil
}

func (s *Store) Calendar(ctx context.Context, now time.Time) (CalendarData, error) {
	data := CalendarData{Today: now.Format("01-02"), Months: make([]CalendarMonth, 12)}
	type aggregate struct{ years, files, done, waiting int }
	byMD := make(map[string]aggregate)
	// A review mark belongs to one year's day, so a date stays reviewed only
	// until media from a new year lands on it: that year's day is unmarked, and
	// the date comes back with just the new files waiting. A file that has left
	// the archive no longer counts, exactly as the day page leaves it out.
	rows, err := s.read.QueryContext(ctx, `SELECT substr(ad.day,6,5),
		count(DISTINCT substr(ad.day,1,4)),
		count(*),
		count(DISTINCT CASE WHEN dp.status='done' THEN ad.day END),
		coalesce(sum(dp.status IS NULL OR dp.status!='done'),0)
		FROM asset_days ad
		LEFT JOIN day_progress dp ON dp.day=ad.day
		WHERE NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=ad.asset_id AND fs.state!='restored')
		GROUP BY substr(ad.day,6,5)`)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var md string
		var a aggregate
		if err = rows.Scan(&md, &a.years, &a.files, &a.done, &a.waiting); err != nil {
			rows.Close()
			return data, err
		}
		byMD[md] = a
	}
	if err = rows.Close(); err != nil {
		return data, err
	}
	fresh, err := s.FreshDates(ctx)
	if err != nil {
		return data, err
	}
	for month := 1; month <= 12; month++ {
		first := time.Date(2000, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		days := time.Date(2000, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Day()
		cells := make([]*CalendarCell, 31)
		for day := 1; day <= days; day++ {
			md := fmt.Sprintf("%02d-%02d", month, day)
			a := byMD[md]
			state := "none"
			if a.years > 0 {
				state = "todo"
				data.Progress.Dates++
				data.Progress.Files += a.files
				if a.done == a.years {
					state = "done"
					data.Progress.Done++
					data.Progress.FilesDone += a.files
				} else if a.done > 0 {
					state = "part"
					data.Progress.Part++
				}
			}
			cells[day-1] = &CalendarCell{MD: md, DOM: day, Years: a.years, Files: a.files, Done: a.done, Waiting: a.waiting, State: state, Today: md == data.Today, Fresh: fresh[md]}
		}
		data.Months[month-1] = CalendarMonth{Name: first.Format("January"), Cells: cells}
	}
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='snapshot_at'").Scan(&data.Refreshed)
	return data, nil
}

func (s *Store) Today(ctx context.Context, md string) (TodayData, error) {
	date, ok := validMonthDay(md)
	if !ok {
		return TodayData{}, ErrInvalid
	}
	data := TodayData{
		MD:       md,
		Label:    date.Format("2 January"),
		Previous: date.AddDate(0, 0, -1).Format("01-02"),
		Next:     date.AddDate(0, 0, 1).Format("01-02"),
		Years:    make([]TodayYear, 0),
	}
	rows, err := s.read.QueryContext(ctx, `SELECT ad.day,CAST(substr(ad.day,1,4) AS INTEGER),count(*),sum(a.size_bytes),COALESCE(dp.status,'pending') FROM asset_days ad JOIN assets a ON a.id=ad.asset_id LEFT JOIN day_progress dp ON dp.day=ad.day WHERE substr(ad.day,6,5)=? AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored') GROUP BY ad.day ORDER BY ad.day`, md)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var year TodayYear
		if err = rows.Scan(&year.Day, &year.Year, &year.Files, &year.Bytes, &year.Status); err != nil {
			rows.Close()
			return data, err
		}
		year.Assets = make([]Asset, 0)
		data.Years = append(data.Years, year)
		data.Memories += year.Files
		data.Bytes += year.Bytes
	}
	if err = rows.Close(); err != nil {
		return data, err
	}
	for index := range data.Years {
		assetRows, queryErr := s.read.QueryContext(ctx, assetSelect+` JOIN asset_days ad ON ad.asset_id=a.id WHERE ad.day=? AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored') ORDER BY a.captured_at,a.id`, data.Years[index].Day)
		if queryErr != nil {
			return data, queryErr
		}
		for assetRows.Next() {
			var asset Asset
			if queryErr = scanAsset(assetRows, &asset); queryErr != nil {
				assetRows.Close()
				return data, queryErr
			}
			data.Years[index].Assets = append(data.Years[index].Assets, asset)
		}
		if queryErr = assetRows.Close(); queryErr != nil {
			return data, queryErr
		}
	}
	fresh, err := s.freshOn(ctx, md)
	if err != nil {
		return data, err
	}
	var all []*Asset
	for index := range data.Years {
		for i := range data.Years[index].Assets {
			asset := &data.Years[index].Assets[i]
			if fresh[asset.ID] {
				asset.New = true
				data.Years[index].Fresh++
			}
			all = append(all, asset)
		}
	}
	if err := s.markPairs(ctx, all); err != nil {
		return data, err
	}
	return data, s.markDurations(ctx, all)
}

func (s *Store) SetDayProgress(ctx context.Context, change DayProgressChange) (DayProgressResult, error) {
	if _, err := time.Parse("2006-01-02", change.Day); err != nil || (change.Status != "pending" && change.Status != "done") || len(change.RequestID) < 8 || len(change.RequestID) > 100 {
		return DayProgressResult{}, ErrInvalid
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return DayProgressResult{}, err
	}
	defer tx.Rollback()
	result, err := setDayProgressTx(ctx, tx, change)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// setDayProgressTx records one year's day as reviewed or not, idempotently by
// request id, inside a transaction the caller commits.
func setDayProgressTx(ctx context.Context, tx *sql.Tx, change DayProgressChange) (DayProgressResult, error) {
	var existing DayProgressResult
	err := tx.QueryRowContext(ctx, "SELECT day,status,previous_status FROM day_progress_events WHERE request_id=?", change.RequestID).Scan(&existing.Day, &existing.Status, &existing.PreviousStatus)
	if err == nil {
		if existing.Day != change.Day || existing.Status != change.Status {
			return DayProgressResult{}, ErrConflict
		}
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return DayProgressResult{}, err
	}
	var present int
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM asset_days WHERE day=?)", change.Day).Scan(&present); err != nil || present == 0 {
		if err != nil {
			return DayProgressResult{}, err
		}
		return DayProgressResult{}, ErrInvalid
	}
	previous := "pending"
	if err = tx.QueryRowContext(ctx, "SELECT status FROM day_progress WHERE day=?", change.Day).Scan(&previous); err != nil && err != sql.ErrNoRows {
		return DayProgressResult{}, err
	}
	// To the millisecond, so a file that arrives in the same second as a
	// review is still placed before or after it.
	now := time.Now().UTC()
	reviewedAt := any(nil)
	if change.Status == "done" {
		reviewedAt = now.Format(reviewStamp)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO day_progress(day,status,reviewed_at) VALUES(?,?,?) ON CONFLICT(day) DO UPDATE SET status=excluded.status,reviewed_at=excluded.reviewed_at", change.Day, change.Status, reviewedAt); err != nil {
		return DayProgressResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO day_progress_events(request_id,day,status,previous_status,created_at) VALUES(?,?,?,?,?)", change.RequestID, change.Day, change.Status, previous, now.Format(eventStamp)); err != nil {
		return DayProgressResult{}, err
	}
	return DayProgressResult{Day: change.Day, Status: change.Status, PreviousStatus: previous}, nil
}
