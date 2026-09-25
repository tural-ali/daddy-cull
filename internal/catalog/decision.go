package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrConflict = errors.New("decision changed; reload before retrying")
var ErrInvalid = errors.New("invalid request")

// Decisions are metadata only. No filesystem capability exists in this package.
type Decision struct {
	RequestID        string `json:"requestId"`
	AssetID          int64  `json:"assetId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Status           string `json:"status"`
	Favourite        bool   `json:"favourite"`
}
type Saved struct {
	Revision          int64  `json:"revision"`
	PreviousStatus    string `json:"previousStatus"`
	PreviousFavourite bool   `json:"previousFavourite"`
}

func (s *Store) Decide(ctx context.Context, d Decision) (Saved, error) {
	results, err := s.DecideBatch(ctx, []Decision{d})
	if err != nil {
		return Saved{}, err
	}
	return results[0], nil
}

func (s *Store) DecideBatch(ctx context.Context, ds []Decision) ([]Saved, error) {
	if len(ds) < 1 || len(ds) > 20 {
		return nil, ErrInvalid
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	results := make([]Saved, 0, len(ds))
	seen := map[int64]bool{}
	for _, d := range ds {
		if seen[d.AssetID] {
			return nil, ErrInvalid
		}
		seen[d.AssetID] = true
		r, e := decideTx(ctx, tx, d)
		if e != nil {
			return nil, e
		}
		results = append(results, r)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for i, d := range ds {
		if d.Favourite != results[i].PreviousFavourite {
			s.wakeImmich()
			break
		}
	}
	return results, nil
}

func decideTx(ctx context.Context, tx *sql.Tx, d Decision) (Saved, error) {
	var result Saved
	if d.AssetID < 1 || d.ExpectedRevision < 0 || len(d.RequestID) < 8 || len(d.RequestID) > 100 {
		return result, ErrInvalid
	}
	if d.Status != "unreviewed" && d.Status != "keep" && d.Status != "later" && d.Status != "cull" {
		return result, ErrInvalid
	}
	var old Decision
	err := tx.QueryRowContext(ctx, "SELECT asset_id,expected_revision,status,favourite,previous_status,previous_favourite FROM decision_events WHERE request_id=?", d.RequestID).Scan(&old.AssetID, &old.ExpectedRevision, &old.Status, &old.Favourite, &result.PreviousStatus, &result.PreviousFavourite)
	if err == nil {
		old.RequestID = d.RequestID
		if old != d {
			return result, ErrConflict
		}
		result.Revision = d.ExpectedRevision + 1
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	var blocked int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM file_state WHERE asset_id=? AND state!='restored'", d.AssetID).Scan(&blocked); err != nil {
		return result, err
	}
	if blocked > 0 {
		return result, fmt.Errorf("%w: file is in a Bin operation", ErrConflict)
	}
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0) FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id WHERE a.id=?", d.AssetID).Scan(&result.PreviousStatus, &result.PreviousFavourite, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("%w: unknown asset", ErrInvalid)
	}
	if err != nil {
		return result, err
	}
	if revision != d.ExpectedRevision {
		return result, ErrConflict
	}
	result.Revision = revision + 1
	_, err = tx.ExecContext(ctx, "INSERT INTO decisions VALUES(?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET status=excluded.status,favourite=excluded.favourite,revision=excluded.revision", d.AssetID, d.Status, d.Favourite, result.Revision)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO decision_events(request_id,asset_id,expected_revision,status,favourite,previous_status,previous_favourite) VALUES(?,?,?,?,?,?,?)", d.RequestID, d.AssetID, d.ExpectedRevision, d.Status, d.Favourite, result.PreviousStatus, result.PreviousFavourite)
	if err != nil {
		return result, err
	}
	if d.Favourite != result.PreviousFavourite {
		// The heart is mirrored to Immich by a background worker, never from
		// here: this only records what Immich should end up showing, in the same
		// transaction as the decision, so the two can never disagree and a slow
		// or absent Immich cannot slow or fail a save.
		if err = queueImmichFavourite(ctx, tx, d.AssetID, d.Favourite); err != nil {
			return result, err
		}
	}
	return result, nil
}
