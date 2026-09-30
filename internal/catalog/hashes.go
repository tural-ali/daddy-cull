package catalog

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"log"
	"os"
	"time"
)

// Two files are only proven the same by hashing every byte of both, and only
// files of the same size can be the same, so the files that share a size with
// another are hashed in the background until the Duplicates page can answer.
// The hash is MD5, which is what the catalogue's imported evidence holds, so a
// file hashed here and a copy hashed before compare. It names files for
// grouping, not for security: the Bin fingerprints every file it moves again,
// with SHA-256, before it touches it.

// hashEvery is the longest a new file waits to be hashed; a changed
// catalogue starts a pass sooner.
const hashEvery = 10 * time.Minute

// hashBatch bounds how many files one pass lists, so a large library is
// worked through in passes that each end, rather than one that never does.
const hashBatch = 5000

// KeepHashes hashes duplicate candidates now and then again every so often,
// until ctx ends. It is quiet when there is nothing new. Files are read one
// at a time: a disk reads one file at a time fastest, and the previews need
// it too.
func (s *Store) KeepHashes(ctx context.Context, roots MediaRoots) {
	for {
		started := time.Now()
		seen, _ := s.CatalogueGeneration(ctx)
		hashed, err := s.FillHashes(ctx, roots)
		if err != nil && ctx.Err() == nil {
			log.Printf("duplicate hashes: %v", err)
		} else if hashed > 0 {
			log.Printf("duplicate hashes: hashed %d in %s", hashed, time.Since(started).Round(time.Second))
		}
		// Videos are compared by their footage after, on the same disk.
		more := s.keepFootage(ctx, roots)
		// A full batch means there is more; go on at once.
		if hashed < hashBatch && !more && !s.waitForChange(ctx, seen, hashEvery) {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// FillHashes hashes, through the read-only mounts, the live files that share
// a size with another and have no full hash yet, and returns how many it
// recorded. Smaller files go first, and the files of one size together, so
// groups settle one after another and the many photos come before the few
// long videos. A file that cannot be reached, or whose size on disk is not
// the size catalogued, is left for a later pass, after the catalogue has
// caught up with it.
func (s *Store) FillHashes(ctx context.Context, roots MediaRoots) (int, error) {
	rows, err := s.read.QueryContext(ctx, `WITH `+liveCandidates+`
	SELECT a.id,a.relative_path,a.size_bytes FROM live l
	  JOIN assets a ON a.id=l.id
	  LEFT JOIN asset_evidence e ON e.asset_id=l.id
	 WHERE l.size_bytes IN (SELECT size_bytes FROM colliding)
	   AND (e.full_hash IS NULL OR e.full_hash='')
	 ORDER BY a.size_bytes,a.id
	 LIMIT ?`, hashBatch)
	if err != nil {
		return 0, err
	}
	var targets []shapeTarget
	for rows.Next() {
		var target shapeTarget
		if err = rows.Scan(&target.id, &target.relative, &target.size); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, target)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	hashed := 0
	for _, target := range targets {
		sum, mtime, ok := hashFile(ctx, roots, target)
		if ctx.Err() != nil {
			return hashed, ctx.Err()
		}
		if !ok {
			continue
		}
		if _, err = s.write.ExecContext(ctx, `INSERT INTO asset_evidence(asset_id,mtime,full_hash,hashed_at) VALUES(?,?,?,?)
			ON CONFLICT(asset_id) DO UPDATE SET mtime=excluded.mtime,full_hash=excluded.full_hash,hashed_at=excluded.hashed_at`,
			target.id, mtime, sum, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return hashed, err
		}
		hashed++
	}
	return hashed, nil
}

// hashFile reads one file through its read-only mount, as the media handler
// opens it, and returns its MD5 and modification time. ok is false when the
// file could not be read whole, or was not the size catalogued or changed
// while it was read.
func hashFile(ctx context.Context, roots MediaRoots, target shapeTarget) (sum string, mtime int64, ok bool) {
	mountRoot, inMount, known := roots.root(target.relative, target.id)
	if !known {
		return "", 0, false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return "", 0, false
	}
	defer root.Close()
	file, err := root.Open(inMount)
	if err != nil {
		return "", 0, false
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != target.size {
		return "", 0, false
	}
	digest := md5.New()
	if _, err = io.Copy(digest, &contextReader{ctx, file}); err != nil {
		return "", 0, false
	}
	after, err := file.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", 0, false
	}
	return hex.EncodeToString(digest.Sum(nil)), before.ModTime().Unix(), true
}
