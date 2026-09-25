package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Favourites travel one way only, from Cull to Immich. Hearting an archive photo
// here asks Immich to show it as a favourite too; taking the heart away asks
// Immich to drop it again, but only when it was Cull that put it there. A photo
// somebody had already favourited in Immich before Cull touched it keeps its
// favourite whatever happens in Cull. Nothing else about an Immich asset is ever
// sent, and Immich's database is never opened: the favourite flag goes through
// Immich's own HTTP API with a key that needs no more than asset.update.
//
// The immich_favourites table is both the queue and the memory of ownership.
// There is one row per archive asset whose heart has ever changed, holding the
// state Cull wants Immich to show, whether Cull is the one that set Immich's
// favourite, and how the last attempt went. A new heart overwrites the wanted
// state in place, so a photo toggled five times while Immich is down is one row
// that syncs once, to whatever the last toggle said.
const (
	// Retries after a failure Immich may recover from start at thirty seconds
	// and double up to an hour, so a long outage costs one request per queued
	// photo per hour rather than a steady stream against a service that is down.
	immichFirstRetry = 30 * time.Second
	immichLastRetry  = time.Hour
	// A failure retrying cannot fix, such as a path Immich has no asset for or
	// a key Immich refuses, is looked at again once a day and at every restart.
	// Immich may simply not have scanned a new file yet, and a fixed key needs a
	// restart to take effect anyway, so neither is worth a faster loop.
	immichFailedRetry = 24 * time.Hour
	// With nothing due the worker still wakes this often, because the private
	// writer shares the catalogue and can save a decision without being able to
	// signal this process. Favourites written outside decisions altogether, by
	// the import commands, are picked up by the backfill at the next start.
	immichIdlePoll = time.Minute
	// One request is bounded well inside the retry interval.
	immichRequestTimeout = 20 * time.Second
	immichBatch          = 50
	// Immich's search answers are small; anything larger is not an answer.
	immichMaxResponse = 8 << 20
	// The prefix under which Immich recorded the archive, when none is given.
	DefaultImmichPathPrefix = "/mnt/family-archive"
)

// ImmichConfig is how the review app reaches Immich. The key is read from the
// environment by the caller and is never logged, printed or stored.
type ImmichConfig struct {
	URL        string
	Key        string
	PathPrefix string
}

// ImmichSync is the background worker that drains immich_favourites. It runs in
// the review app, the process that saves decisions; the private writer never
// talks to Immich.
type ImmichSync struct {
	s      *Store
	base   string
	key    string
	prefix string
	client *http.Client
	now    func() time.Time
}

// String keeps the key out of anything that prints the worker.
func (y *ImmichSync) String() string { return "ImmichSync(" + y.base + ")" }

// GoString does the same for %#v.
func (y *ImmichSync) GoString() string { return y.String() }

// NewImmichSync returns nil, with no error, when the URL or the key is empty:
// the sync is then switched off rather than broken. Hearts are still queued in
// that case, one row per photo at most, so switching it on later catches Immich
// up with every heart given in the meantime.
func NewImmichSync(s *Store, cfg ImmichConfig) (*ImmichSync, error) {
	if strings.TrimSpace(cfg.URL) == "" || strings.TrimSpace(cfg.Key) == "" {
		return nil, nil
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.URL), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("IMMICH_URL must be a plain http or https address such as http://192.168.1.10:2283")
	}
	prefix := strings.TrimRight(strings.TrimSpace(cfg.PathPrefix), "/")
	if prefix == "" {
		prefix = DefaultImmichPathPrefix
	}
	if !path.IsAbs(prefix) || path.Clean(prefix) != prefix {
		return nil, fmt.Errorf("IMMICH_PATH_PREFIX must be an absolute path such as %s", DefaultImmichPathPrefix)
	}
	client := &http.Client{
		Timeout: immichRequestTimeout,
		// The key travels in a custom header, which Go would carry across a
		// redirect to another host. Immich's API does not redirect, so a
		// redirect is refused outright rather than followed with the key.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &ImmichSync{s: s, base: base.String(), key: cfg.Key, prefix: prefix, client: client, now: time.Now}, nil
}

// Run queues every existing favourite that Immich has not yet been told about,
// then drains the queue until ctx ends. It is safe to run in only one process
// at a time, which is how the compose file runs it.
func (y *ImmichSync) Run(ctx context.Context) {
	queued, err := y.s.QueueImmichBackfill(ctx, y.now())
	if err != nil {
		log.Printf("immich: could not queue existing favourites: %s", y.redact(err.Error()))
	} else if queued > 0 {
		log.Printf("immich: queued %d favourites for Immich", queued)
	}
	for {
		if err = y.drain(ctx); err != nil && ctx.Err() == nil {
			log.Printf("immich: favourite queue unavailable: %s", y.redact(err.Error()))
		}
		wait := immichIdlePoll
		if next, ok := y.s.nextImmichAttempt(ctx); ok {
			wait = min(max(time.Until(time.Unix(next, 0)), time.Second), immichIdlePoll)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-y.s.immichWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// wakeImmich tells a running worker that a heart changed. It never blocks.
func (s *Store) wakeImmich() {
	select {
	case s.immichWake <- struct{}{}:
	default:
	}
}

// queueImmichFavourite records, inside a decision's own transaction, the state
// Immich should end up showing. Only archive files exist in Immich, so a heart
// on anything else is not queued. A newer heart replaces an older one that has
// not synced yet and is due at once, whatever the last attempt's backoff was.
func queueImmichFavourite(ctx context.Context, tx *sql.Tx, assetID int64, favourite bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO immich_favourites(asset_id,desired,state,attempts,last_error,next_attempt_at,updated_at)
		SELECT id,?,'pending',0,'',0,strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM assets WHERE id=? AND source_id='archive'
		ON CONFLICT(asset_id) DO UPDATE SET desired=excluded.desired,state='pending',attempts=0,last_error='',next_attempt_at=0,updated_at=excluded.updated_at`, favourite, assetID)
	return err
}

// QueueImmichBackfill brings the queue up to date with favourites that did not
// arrive through a saved decision: those imported with the catalogue or from
// the legacy app, and those given before the sync existed. It also re-queues a
// row whose wanted state no longer matches the heart, and makes every failed
// row due now, so a restart is also the way to retry after fixing Immich. It
// is idempotent and returns how many hearts it queued.
func (s *Store) QueueImmichBackfill(ctx context.Context, now time.Time) (int64, error) {
	stamp := now.UTC().Format(time.RFC3339)
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var queued int64
	for i, statement := range []string{
		`INSERT INTO immich_favourites(asset_id,desired,state,attempts,last_error,next_attempt_at,updated_at)
			SELECT d.asset_id,1,'pending',0,'',0,? FROM decisions d JOIN assets a ON a.id=d.asset_id
			WHERE d.favourite=1 AND a.source_id='archive' AND NOT EXISTS(SELECT 1 FROM immich_favourites f WHERE f.asset_id=d.asset_id)`,
		`UPDATE immich_favourites SET desired=1-desired,state='pending',attempts=0,last_error='',next_attempt_at=0,updated_at=?
			WHERE desired!=COALESCE((SELECT d.favourite FROM decisions d WHERE d.asset_id=immich_favourites.asset_id),0)`,
		`UPDATE immich_favourites SET next_attempt_at=0,updated_at=? WHERE state='failed' AND next_attempt_at!=0`,
	} {
		result, execErr := tx.ExecContext(ctx, statement, stamp)
		if execErr != nil {
			return 0, execErr
		}
		if n, _ := result.RowsAffected(); i < 2 {
			queued += n
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return queued, nil
}

// ImmichQueueCounts is the read-only summary shown in Settings: favourites
// Immich shows because of Cull or already had, hearts still waiting, and hearts
// Immich could not take.
func (s *Store) ImmichQueueCounts(ctx context.Context) (synced, pending, failed int) {
	synced = countQuery(ctx, s.read, "SELECT count(*) FROM immich_favourites WHERE state='done' AND desired=1")
	pending = countQuery(ctx, s.read, "SELECT count(*) FROM immich_favourites WHERE state='pending'")
	failed = countQuery(ctx, s.read, "SELECT count(*) FROM immich_favourites WHERE state='failed'")
	return
}

func (s *Store) nextImmichAttempt(ctx context.Context) (int64, bool) {
	var next sql.NullInt64
	if err := s.read.QueryRowContext(ctx, "SELECT min(next_attempt_at) FROM immich_favourites WHERE state IN ('pending','failed')").Scan(&next); err != nil || !next.Valid {
		return 0, false
	}
	return next.Int64, true
}

type immichRow struct {
	assetID   int64
	path      string
	desired   bool
	immichID  string
	setByCull bool
	attempts  int
}

// drain works through every row that is due, a batch at a time. No database
// transaction is held while Immich is being asked anything: the catalogue has a
// single writer connection, and a heart saved while a request is in flight must
// not wait for Immich.
func (y *ImmichSync) drain(ctx context.Context) error {
	for ctx.Err() == nil {
		rows, err := y.due(ctx)
		if err != nil || len(rows) == 0 {
			return err
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return nil
			}
			syncErr := y.apply(ctx, row)
			if ctx.Err() != nil {
				// A shutdown mid-request is not Immich's failure, so it is
				// neither counted nor backed off; the row stays due.
				return nil
			}
			if err = y.finish(ctx, row, syncErr); err != nil {
				return err
			}
		}
	}
	return nil
}

func (y *ImmichSync) due(ctx context.Context) ([]immichRow, error) {
	rows, err := y.s.read.QueryContext(ctx, `SELECT f.asset_id,a.relative_path,f.desired,f.immich_id,f.set_by_cull,f.attempts
		FROM immich_favourites f JOIN assets a ON a.id=f.asset_id
		WHERE f.state IN ('pending','failed') AND f.next_attempt_at<=? ORDER BY f.next_attempt_at,f.asset_id LIMIT ?`, y.now().Unix(), immichBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []immichRow
	for rows.Next() {
		var row immichRow
		if err = rows.Scan(&row.assetID, &row.path, &row.desired, &row.immichID, &row.setByCull, &row.attempts); err != nil {
			return nil, err
		}
		due = append(due, row)
	}
	return due, rows.Err()
}

// apply brings one Immich asset to the state Cull wants.
//
// To favourite, Immich is always asked first, because only its answer says
// whether the favourite is already there. If it is, and Cull did not put it
// there, it belongs to Immich and is left alone for ever. If it is not, Cull
// writes down that it owns the favourite before asking Immich to set it: were
// the order reversed, a set that succeeded but whose reply was lost would look
// on retry like a favourite Immich already had, and Cull would then never take
// it away again.
//
// To unfavourite, nothing is sent at all unless Cull owns the favourite.
func (y *ImmichSync) apply(ctx context.Context, row immichRow) error {
	if !row.desired {
		if !row.setByCull {
			return nil
		}
		id := row.immichID
		if id == "" {
			asset, err := y.lookup(ctx, row.path)
			if err != nil {
				return err
			}
			id = asset.ID
		}
		if err := y.setFavourite(ctx, id, false); err != nil {
			return err
		}
		return y.s.recordImmichOwner(ctx, row.assetID, id, false, y.now())
	}
	asset, err := y.lookup(ctx, row.path)
	if err != nil {
		return err
	}
	if asset.IsFavorite {
		return y.s.recordImmichOwner(ctx, row.assetID, asset.ID, row.setByCull && row.immichID == asset.ID, y.now())
	}
	if err = y.s.recordImmichOwner(ctx, row.assetID, asset.ID, true, y.now()); err != nil {
		return err
	}
	return y.setFavourite(ctx, asset.ID, true)
}

// recordImmichOwner is written whatever has happened to the wanted state in the
// meantime, because it describes Immich, not Cull's intent.
func (s *Store) recordImmichOwner(ctx context.Context, assetID int64, immichID string, setByCull bool, now time.Time) error {
	_, err := s.write.ExecContext(ctx, "UPDATE immich_favourites SET immich_id=?,set_by_cull=?,updated_at=? WHERE asset_id=?", immichID, setByCull, now.UTC().Format(time.RFC3339), assetID)
	return err
}

// finish records how an attempt went. The outcome is only written while the
// wanted state is still the one that was acted on; a heart toggled during the
// request has already made the row due again, and must stay so.
func (y *ImmichSync) finish(ctx context.Context, row immichRow, syncErr error) error {
	now := y.now()
	stamp := now.UTC().Format(time.RFC3339)
	if syncErr == nil {
		_, err := y.s.write.ExecContext(ctx, "UPDATE immich_favourites SET state='done',attempts=0,last_error='',next_attempt_at=0,updated_at=? WHERE asset_id=? AND desired=?", stamp, row.assetID, row.desired)
		return err
	}
	attempts := row.attempts + 1
	state, delay := "pending", immichBackoff(attempts)
	var failure *immichError
	if errors.As(syncErr, &failure) && failure.permanent {
		state, delay = "failed", immichFailedRetry
	}
	message := y.redact(syncErr.Error())
	log.Printf("immich: asset %d, attempt %d: %s; next try in %s", row.assetID, attempts, message, delay)
	_, err := y.s.write.ExecContext(ctx, "UPDATE immich_favourites SET state=?,attempts=?,last_error=?,next_attempt_at=?,updated_at=? WHERE asset_id=? AND desired=?", state, attempts, message, now.Add(delay).Unix(), stamp, row.assetID, row.desired)
	return err
}

func immichBackoff(attempts int) time.Duration {
	delay := immichFirstRetry
	for i := 1; i < attempts && delay < immichLastRetry; i++ {
		delay *= 2
	}
	return min(delay, immichLastRetry)
}

type immichAsset struct {
	ID           string `json:"id"`
	IsFavorite   bool   `json:"isFavorite"`
	OriginalPath string `json:"originalPath"`
}

// lookup finds the one Immich asset for an archive file. Cull records the file
// as /archive/<rel>, and Immich's external library records the same file under
// its own mount, /mnt/family-archive/<rel> by default. Immich's originalPath
// search matches loosely, so only an item whose path is exactly the one asked
// for counts, and anything but exactly one such item is refused rather than
// guessed at.
func (y *ImmichSync) lookup(ctx context.Context, cullPath string) (immichAsset, error) {
	rel, ok := archiveRelative(cullPath)
	if !ok {
		return immichAsset{}, &immichError{permanent: true, message: "not an archive file"}
	}
	original := y.prefix + "/" + rel
	body, err := json.Marshal(map[string]string{"originalPath": original})
	if err != nil {
		return immichAsset{}, err
	}
	var found struct {
		Assets struct {
			Items []immichAsset `json:"items"`
		} `json:"assets"`
	}
	if err = y.call(ctx, http.MethodPost, "/api/search/metadata", body, &found); err != nil {
		return immichAsset{}, err
	}
	var matches []immichAsset
	for _, item := range found.Assets.Items {
		if item.OriginalPath == original {
			matches = append(matches, item)
		}
	}
	switch {
	case len(matches) == 0:
		return immichAsset{}, &immichError{permanent: true, message: fmt.Sprintf("Immich has no asset at %s", original)}
	case len(matches) > 1:
		return immichAsset{}, &immichError{permanent: true, message: fmt.Sprintf("Immich has %d assets at %s", len(matches), original)}
	case matches[0].ID == "":
		return immichAsset{}, &immichError{permanent: true, message: fmt.Sprintf("Immich returned an asset at %s with no id", original)}
	}
	return matches[0], nil
}

// setFavourite is the only write Cull ever makes to Immich.
func (y *ImmichSync) setFavourite(ctx context.Context, immichID string, favourite bool) error {
	body, err := json.Marshal(struct {
		IDs        []string `json:"ids"`
		IsFavorite bool     `json:"isFavorite"`
	}{[]string{immichID}, favourite})
	if err != nil {
		return err
	}
	return y.call(ctx, http.MethodPut, "/api/assets", body, nil)
}

// call sends one request to Immich. Its errors describe the request by method,
// endpoint and status, plus the short reason Immich gives in its "message"
// field, such as "Not found or no asset.update access", which is what tells a
// key without rights over the asset from a missing asset. Never the key, and
// never the rest of the response body.
func (y *ImmichSync) call(ctx context.Context, method, endpoint string, body []byte, out any) error {
	request, err := http.NewRequestWithContext(ctx, method, y.base+endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("x-api-key", y.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := y.client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return &immichError{message: fmt.Sprintf("Immich unreachable for %s %s: %v", method, endpoint, err)}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var reason struct {
			Message any `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, immichMaxResponse)).Decode(&reason)
		io.Copy(io.Discard, io.LimitReader(response.Body, immichMaxResponse))
		said := ""
		if text := strings.TrimSpace(fmt.Sprint(reason.Message)); reason.Message != nil && text != "" {
			if len(text) > 160 {
				text = text[:160]
			}
			said = ": " + text
		}
		// Immich being busy, restarting or behind a failing proxy passes;
		// a refused key or a rejected request does not pass by retrying.
		transient := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return &immichError{permanent: !transient, message: fmt.Sprintf("Immich answered %s %s with %d%s", method, endpoint, response.StatusCode, said)}
	}
	if out == nil {
		io.Copy(io.Discard, io.LimitReader(response.Body, immichMaxResponse))
		return nil
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, immichMaxResponse)).Decode(out); err != nil {
		return &immichError{message: fmt.Sprintf("Immich sent an unreadable answer to %s %s", method, endpoint)}
	}
	return nil
}

// redact is a last guard for the key, in case some lower layer ever repeats a
// request header in an error. Every message that reaches a log or the table
// passes through it.
func (y *ImmichSync) redact(message string) string {
	if y.key == "" {
		return message
	}
	return strings.ReplaceAll(message, y.key, "[redacted]")
}

// An immichError is permanent when retrying cannot help.
type immichError struct {
	permanent bool
	message   string
}

func (e *immichError) Error() string { return e.message }
