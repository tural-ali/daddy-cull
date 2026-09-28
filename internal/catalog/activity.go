package catalog

import (
	"context"
	"sort"
	"time"
)

// ReviewActivity is how steadily the archive is being reviewed: the run of
// consecutive days with at least one review, and the last seven days.
type ReviewActivity struct {
	Streak      int  `json:"streak"`
	Today       bool `json:"reviewedToday"`
	WeekDays    int  `json:"weekDays"`
	WeekSeconds int  `json:"weekSeconds"`
}

// A pause longer than this between two choices is a break, not review time.
const reviewPause = 5 * time.Minute

var activityLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"}

func parseActivity(value string) (time.Time, bool) {
	for _, layout := range activityLayouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// reviewTimes reads every moment a review happened: each decision and each
// date marked reviewed, in the viewer's time zone and in order. A day opened
// again by new files keeps its reviewed_at, since that review still happened;
// taking a mark back clears it.
func (s *Store) reviewTimes(ctx context.Context, loc *time.Location) ([]time.Time, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT created_at FROM decision_events
		UNION ALL SELECT created_at FROM day_progress_events WHERE status='done'
		UNION ALL SELECT reviewed_at FROM day_progress WHERE reviewed_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var times []time.Time
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		if t, ok := parseActivity(value); ok {
			times = append(times, t.In(loc))
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times, nil
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

func reviewDaySet(times []time.Time) map[string]bool {
	days := make(map[string]bool, len(times))
	for _, t := range times {
		days[dayKey(t)] = true
	}
	return days
}

// currentStreak counts back from today, or from yesterday while today has no
// review yet, so a streak survives until the end of a day without one.
func currentStreak(days map[string]bool, loc *time.Location, now time.Time) (int, bool) {
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc)
	today := days[dayKey(day)]
	if !today {
		day = day.AddDate(0, 0, -1)
	}
	streak := 0
	for days[dayKey(day)] {
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak, today
}

// Activity counts a day as reviewed when any photograph was decided on it or
// any date was marked reviewed, in the viewer's time zone so the day turns
// over at their midnight. A streak survives until the end of a day with no
// review yet: it counts back from yesterday until today has one.
func (s *Store) Activity(ctx context.Context, loc *time.Location, now time.Time) (ReviewActivity, error) {
	var activity ReviewActivity
	times, err := s.reviewTimes(ctx, loc)
	if err != nil {
		return activity, err
	}
	now = now.In(loc)
	key := dayKey
	days := reviewDaySet(times)
	activity.Streak, activity.Today = currentStreak(days, loc, now)
	weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -6)
	for offset := 0; offset < 7; offset++ {
		if days[key(weekStart.AddDate(0, 0, offset).Add(12*time.Hour))] {
			activity.WeekDays++
		}
	}
	for i := 1; i < len(times); i++ {
		if times[i-1].Before(weekStart) {
			continue
		}
		if gap := times[i].Sub(times[i-1]); gap <= reviewPause {
			activity.WeekSeconds += int(gap.Seconds())
		}
	}
	return activity, nil
}

// StreakCalendar is the run of review days for the streak calendar: the
// current run, the longest run ever, and every day with a review.
type StreakCalendar struct {
	// Streak is how many days in a row something was reviewed, counting back
	// from today, or from yesterday while today has no review yet.
	Streak int `json:"streak"`
	// Today is whether something was reviewed today.
	Today bool `json:"reviewedToday"`
	// Best is the longest run of days in a row ever reviewed.
	Best int `json:"best"`
	// Days are every day with a review, as YYYY-MM-DD, oldest first.
	Days []string `json:"days"`
}

// Streak lists the days with a review, in the viewer's time zone, for the
// calendar behind the streak in the header.
func (s *Store) Streak(ctx context.Context, loc *time.Location, now time.Time) (StreakCalendar, error) {
	result := StreakCalendar{Days: []string{}}
	times, err := s.reviewTimes(ctx, loc)
	if err != nil {
		return result, err
	}
	days := reviewDaySet(times)
	result.Streak, result.Today = currentStreak(days, loc, now)
	for day := range days {
		result.Days = append(result.Days, day)
	}
	sort.Strings(result.Days)
	run := 0
	var previous time.Time
	for _, day := range result.Days {
		t, _ := time.ParseInLocation("2006-01-02", day, loc)
		t = t.Add(12 * time.Hour)
		if run > 0 && dayKey(previous.AddDate(0, 0, 1)) == day {
			run++
		} else {
			run = 1
		}
		previous = t
		if run > result.Best {
			result.Best = run
		}
	}
	return result, nil
}
