package catalog

import (
	"context"
	"database/sql"
	"time"
)

// The catalogue generation tells an open page that the archive changed under
// it. Graduation adds files at night from another process (cull -scan-archive
// and -phone-deletions, run by the host's graduation script), and a tab left
// open overnight would otherwise go on showing the day before. Triggers on
// assets count every file added or removed, whoever does it; a phone deletion
// marks files without adding any, so it counts itself.

// CatalogueGeneration is a number that changes whenever the catalogue does.
func (s *Store) CatalogueGeneration(ctx context.Context) (int64, error) {
	var n int64
	err := s.read.QueryRowContext(ctx, "SELECT value FROM catalogue_generation WHERE id=1").Scan(&n)
	return n, err
}

func bumpCatalogueGeneration(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE catalogue_generation SET value=value+1 WHERE id=1")
	return err
}

// changePoll is how often a background worker waiting for its next pass
// looks at the generation, which is one row.
const changePoll = 30 * time.Second

// waitForChange returns once the catalogue has changed since generation seen,
// or longest has passed, and reports false only when ctx ended first. The
// library is catalogued by the writer, another process, so this is how the
// workers here hear of new files within a minute rather than at their next
// pass.
func (s *Store) waitForChange(ctx context.Context, seen int64, longest time.Duration) bool {
	deadline := time.NewTimer(longest)
	defer deadline.Stop()
	tick := time.NewTicker(changePoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return true
		case <-tick.C:
			if now, err := s.CatalogueGeneration(ctx); err == nil && now != seen {
				return true
			}
		}
	}
}
