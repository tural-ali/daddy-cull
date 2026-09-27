package catalog

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

// Photos deleted on a phone after they graduated into the archive.
//
// icloudpd removes the local copy of anything in Recently Deleted. Once iCloud
// staging is cleared after a day, the local copy is the archive one, which
// icloudpd must not touch; it writes the deletion down instead, and the
// graduation script hands that list here, each line joined to the archive
// file it graduated to.
//
// A deletion becomes a mark for the Bin, never a move and never a purge: the
// file stays where it is until someone carries the Bin out, with its 30 days
// to change their mind. A file someone kept or gave a heart in Cull is left as
// it is, because that choice was made here about this archive and a phone
// deletion may be someone tidying their own library. Each deletion is handled
// once: a mark taken back in Cull is not put back the next night, although
// icloudpd reports the photo for as long as it sits in Recently Deleted.

type PhoneDeletionResult struct {
	Marked        int `json:"marked"`
	AlreadyMarked int `json:"alreadyMarked"`
	Kept          int `json:"kept"`
	Favourite     int `json:"favourite"`
	InBin         int `json:"inBin"`
	NotCatalogued int `json:"notCatalogued"`
	Changed       int `json:"changed"`
	HandledBefore int `json:"handledBefore"`
}

func (r PhoneDeletionResult) String() string {
	return fmt.Sprintf("marked for the Bin %d, already marked %d, kept in Cull %d, favourites %d, in the Bin %d, not catalogued %d, changed since %d, handled before %d",
		r.Marked, r.AlreadyMarked, r.Kept, r.Favourite, r.InBin, r.NotCatalogued, r.Changed, r.HandledBefore)
}

type phoneDeletion struct {
	archivePath string // "/archive/..."
	size        int64
	zone        string
	relPath     string
	reportedAt  string
}

// parsePhoneDeletion reads "<host path> TAB <size> TAB <zone> TAB <path in
// zone> TAB <reported at>", with the host path under root.
func parsePhoneDeletion(root, line string) (phoneDeletion, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != 5 {
		return phoneDeletion{}, fmt.Errorf("%w: expected 5 fields: %q", ErrInvalid, line)
	}
	rest, ok := strings.CutPrefix(fields[0], strings.TrimSuffix(root, "/")+"/")
	if !ok || rest == "" || path.Clean(rest) != rest || strings.HasPrefix(rest, "../") || rest == ".." {
		return phoneDeletion{}, fmt.Errorf("%w: not a file under %s: %q", ErrInvalid, root, fields[0])
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		return phoneDeletion{}, fmt.Errorf("%w: size: %q", ErrInvalid, fields[1])
	}
	if fields[2] == "" || fields[3] == "" || fields[4] == "" {
		return phoneDeletion{}, fmt.Errorf("%w: empty field: %q", ErrInvalid, line)
	}
	return phoneDeletion{archivePath: "/archive/" + rest, size: size, zone: fields[2], relPath: fields[3], reportedAt: fields[4]}, nil
}

// MarkPhoneDeletions reads the graduation script's list and marks for the Bin
// what it can. A malformed line stops everything before anything is marked.
func (s *Store) MarkPhoneDeletions(ctx context.Context, root string, in io.Reader) (PhoneDeletionResult, error) {
	var result PhoneDeletionResult
	var deletions []phoneDeletion
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 65536), 1048576)
	for scan.Scan() {
		line := strings.TrimRight(scan.Text(), "\r")
		if line == "" {
			continue
		}
		d, err := parsePhoneDeletion(root, line)
		if err != nil {
			return result, err
		}
		deletions = append(deletions, d)
	}
	if err := scan.Err(); err != nil {
		return result, err
	}
	for _, d := range deletions {
		if err := s.markPhoneDeletion(ctx, d, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) markPhoneDeletion(ctx context.Context, d phoneDeletion, result *PhoneDeletionResult) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seen int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM phone_deletions WHERE zone=? AND rel_path=? AND size_bytes=?", d.zone, d.relPath, d.size).Scan(&seen); err != nil {
		return err
	}
	if seen > 0 {
		result.HandledBefore++
		return nil
	}
	var (
		assetID            sql.NullInt64
		size, revision     int64
		status             string
		favourite, binning bool
	)
	err = tx.QueryRowContext(ctx, `SELECT a.id,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),
	 EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	 FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id WHERE a.source_id='archive' AND a.relative_path=?`, d.archivePath).Scan(&assetID, &size, &status, &favourite, &revision, &binning)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var outcome string
	switch {
	case !assetID.Valid:
		outcome, result.NotCatalogued = "not-catalogued", result.NotCatalogued+1
	case binning:
		outcome, result.InBin = "in-bin", result.InBin+1
	case size != d.size:
		outcome, result.Changed = "changed", result.Changed+1
	case status == "cull":
		outcome, result.AlreadyMarked = "already-marked", result.AlreadyMarked+1
	case favourite:
		outcome, result.Favourite = "favourite", result.Favourite+1
	case status == "keep":
		outcome, result.Kept = "kept", result.Kept+1
	default:
		sum := sha256.Sum256([]byte(d.zone + "\x00" + d.relPath + "\x00" + strconv.FormatInt(d.size, 10)))
		if _, err = decideTx(ctx, tx, Decision{RequestID: "phone-deletion:" + hex.EncodeToString(sum[:16]), AssetID: assetID.Int64, ExpectedRevision: revision, Status: "cull"}); err != nil {
			return err
		}
		outcome, result.Marked = "marked", result.Marked+1
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO phone_deletions(zone,rel_path,size_bytes,reported_at,asset_id,outcome,handled_at) VALUES(?,?,?,?,?,?,?)",
		d.zone, d.relPath, d.size, d.reportedAt, assetID, outcome, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}
