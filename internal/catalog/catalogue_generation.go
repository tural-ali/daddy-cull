package catalog

import (
	"context"
	"database/sql"
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
