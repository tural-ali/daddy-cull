package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"
)

// Deleting a file from the Bin used to unlink it on the spot, which left a
// mistake noticed the next morning with nothing to restore. Now a deletion is
// only scheduled: the files stay exactly where the Bin put them, leave the Bin's
// list, and are listed on the Log with the day they will go. The writer's reaper
// deletes them for real once the grace period has passed, through the same
// engines and checks as an immediate deletion. Restoring one before then puts it
// back where it came from.
//
// The grace period is read when the reaper runs, not stored with each file, so
// shortening it applies to everything already waiting. That is the behaviour a
// reviewer expects from a setting called "keep deleted files for N days".
const (
	DefaultGraceDays = 30
	MaxGraceDays     = 365
	graceSetting     = "bin_grace_days"
)

type scheduledDeletion struct {
	deletedAt time.Time
	attempts  int
	lastError string
}

// GraceDays is how long a file deleted from the Bin is kept before the reaper
// deletes it. Zero means deletion is immediate.
func (s *Store) GraceDays(ctx context.Context) (int, error) {
	var value string
	err := s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", graceSetting).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultGraceDays, nil
	}
	if err != nil {
		return 0, err
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 || days > MaxGraceDays {
		// Only SetGraceDays writes this, so a bad value means someone edited
		// the database by hand. Deleting on a guess would be worse than not
		// deleting, so the reaper stops until it is fixed.
		return 0, fmt.Errorf("the saved grace period %q is not a number of days between 0 and %d", value, MaxGraceDays)
	}
	return days, nil
}

func (s *Store) SetGraceDays(ctx context.Context, days int) error {
	if days < 0 || days > MaxGraceDays {
		return ErrInvalid
	}
	_, err := s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", graceSetting, strconv.Itoa(days))
	return err
}

func (s *Store) scheduledGroups(ctx context.Context) (map[string]scheduledDeletion, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT grp,deleted_at,attempts,last_error FROM trash_deletions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]scheduledDeletion{}
	for rows.Next() {
		var group, at string
		var entry scheduledDeletion
		if err = rows.Scan(&group, &at, &entry.attempts, &entry.lastError); err != nil {
			return nil, err
		}
		if entry.deletedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("deletion time for %s is unreadable: %w", group, err)
		}
		groups[group] = entry
	}
	return groups, rows.Err()
}

// scheduleDeletion records that these groups were deleted from the Bin. A group
// deleted twice keeps its first time, so its countdown never restarts.
func (s *Store) scheduleDeletion(ctx context.Context, groups []string, at time.Time) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, group := range groups {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO trash_deletions(grp,deleted_at) VALUES(?,?)", group, at.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) unschedule(ctx context.Context, groups []string) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, group := range groups {
		if _, err = tx.ExecContext(ctx, "DELETE FROM trash_deletions WHERE grp=?", group); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) recordReapFailure(ctx context.Context, group, reason string) error {
	_, err := s.write.ExecContext(ctx, "UPDATE trash_deletions SET attempts=attempts+1,last_error=? WHERE grp=?", truncate(reason, 300), group)
	return err
}

// recordReap keeps the last run where the Settings page can show it, so the
// reviewer can see that automatic deletion is actually happening.
func (s *Store) recordReap(ctx context.Context, at time.Time, deleted int, failure string) error {
	_, err := s.write.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('bin_reaped_at',?),('bin_reaped_files',?),('bin_reap_error',?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, at.UTC().Format(time.RFC3339), strconv.Itoa(deleted), truncate(failure, 300))
	return err
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// DeletingItem is a file deleted from the Bin and still on disk.
type DeletingItem struct {
	TrashItem
	DeletedAt string `json:"deletedAt"`
	DueAt     string `json:"dueAt"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"lastError,omitempty"`
}

// DeletingReport is what the Log and Settings pages show about deletions still
// waiting and about the reaper's last run.
type DeletingReport struct {
	GraceDays     int            `json:"graceDays"`
	GraceError    string         `json:"graceError,omitempty"`
	Items         []DeletingItem `json:"items"`
	LastRun       string         `json:"lastRun"`
	LastDeleted   int            `json:"lastDeleted"`
	LastError     string         `json:"lastError"`
	CheckInterval int            `json:"checkIntervalMinutes"`
}

// ReapInterval is how often the writer looks for deletions whose grace period
// has passed. A file is therefore deleted at most this long after it is due.
const ReapInterval = 15 * time.Minute

func (s *Store) Deleting(ctx context.Context) (DeletingReport, error) {
	report := DeletingReport{Items: []DeletingItem{}, CheckInterval: int(ReapInterval / time.Minute)}
	grace, err := s.GraceDays(ctx)
	if err != nil {
		report.GraceError = err.Error()
	}
	report.GraceDays = grace
	items, err := s.deleting(ctx, grace)
	if err != nil {
		return report, err
	}
	report.Items = items
	var files string
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='bin_reaped_at'").Scan(&report.LastRun)
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='bin_reaped_files'").Scan(&files)
	_ = s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='bin_reap_error'").Scan(&report.LastError)
	report.LastDeleted, _ = strconv.Atoi(files)
	return report, nil
}

// deleting lists the held files whose group has been deleted from the Bin,
// soonest to go first.
func (s *Store) deleting(ctx context.Context, grace int) ([]DeletingItem, error) {
	items, err := s.held(ctx)
	if err != nil {
		return nil, err
	}
	scheduled, err := s.scheduledGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeletingItem, 0)
	for _, item := range items {
		entry, ok := scheduled[item.Group]
		if !ok {
			continue
		}
		out = append(out, DeletingItem{
			TrashItem: item,
			DeletedAt: entry.deletedAt.UTC().Format(time.RFC3339),
			DueAt:     entry.deletedAt.Add(time.Duration(grace) * 24 * time.Hour).UTC().Format(time.RFC3339),
			Attempts:  entry.attempts,
			LastError: entry.lastError,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DueAt < out[j].DueAt })
	return out, nil
}

// Reap deletes every file whose grace period has passed by now. It holds the
// writer's lock, so it never races a restore the reviewer started on the Log.
// A group that fails is kept and tried again on the next run, with the reason
// shown on the Log; a group the Bin no longer holds, because it was restored or
// deleted some other way, simply has its schedule dropped.
func (t *TrashWriter) Reap(ctx context.Context, now time.Time) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	grace, err := t.s.GraceDays(ctx)
	if err != nil {
		t.s.recordReap(ctx, now, 0, err.Error())
		return 0, err
	}
	scheduled, err := t.s.scheduledGroups(ctx)
	if err != nil {
		return 0, err
	}
	if len(scheduled) == 0 {
		return 0, t.s.recordReap(ctx, now, 0, "")
	}
	items, err := t.s.held(ctx)
	if err != nil {
		return 0, err
	}
	present := map[string]bool{}
	due := make([]TrashItem, 0)
	for _, item := range items {
		present[item.Group] = true
		entry, ok := scheduled[item.Group]
		if ok && !now.Before(entry.deletedAt.Add(time.Duration(grace)*24*time.Hour)) {
			due = append(due, item)
		}
	}
	gone := make([]string, 0)
	for group := range scheduled {
		if !present[group] {
			gone = append(gone, group)
		}
	}
	if err = t.s.unschedule(ctx, gone); err != nil {
		return 0, err
	}
	if len(due) == 0 {
		return 0, t.s.recordReap(ctx, now, 0, "")
	}
	result, outcome := t.act(ctx, due, true)
	if err = t.s.unschedule(ctx, setKeys(outcome.done)); err != nil {
		return result.Done, err
	}
	summary := ""
	for _, group := range setKeys(outcome.failed) {
		if err = t.s.recordReapFailure(ctx, group, outcome.failed[group]); err != nil {
			return result.Done, err
		}
		summary = outcome.failed[group]
	}
	if n := len(outcome.failed); n > 0 {
		summary = fmt.Sprintf("%d group%s of files could not be deleted and will be tried again: %s", n, plural(n), summary)
	}
	return result.Done, t.s.recordReap(ctx, now, result.Done, summary)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func setKeys[V any](set map[string]V) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// StartReaper runs Reap now and then every ReapInterval until ctx ends. It runs
// in the writer, the only process that may delete a file.
func (t *TrashWriter) StartReaper(ctx context.Context) {
	go func() {
		run := func() {
			runCtx, cancel := context.WithTimeout(ctx, 60*time.Minute)
			defer cancel()
			deleted, err := t.Reap(runCtx, time.Now())
			switch {
			case err != nil:
				log.Printf("automatic Bin deletion: %v", err)
			case deleted > 0:
				log.Printf("automatic Bin deletion: %d file%s deleted after the grace period", deleted, plural(deleted))
			}
		}
		run()
		ticker := time.NewTicker(ReapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

// groupsOf returns the distinct groups of items in order.
func groupsOf(items []TrashItem) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, item := range items {
		if !seen[item.Group] {
			seen[item.Group] = true
			out = append(out, item.Group)
		}
	}
	return out
}
