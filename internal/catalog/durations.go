package catalog

import (
	"context"
	"database/sql"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A video tile says how long the clip runs, as Google Photos does, so the
// running time is read once per file with the prober that ships beside the
// frame extractor and kept in the catalogue. The size it was read at is kept
// with it: a file replaced by a different one is read again.

// durationWorkers is how many clips are probed at once. Probing reads a
// container's header rather than decoding, so a few at a time is plenty and
// leaves the preview workers to the tiles.
const durationWorkers = 4

// durationTimeout bounds one probe. A header takes milliseconds; an old AVI
// with no index is scanned to its end, which on a slow disk takes seconds.
const durationTimeout = 60 * time.Second

// durationEvery is the longest a new clip waits for its duration; a changed
// catalogue starts a pass sooner.
const durationEvery = 15 * time.Minute

// KeepDurations fills in running times now and then again every so often,
// until ctx ends. It is quiet when there is nothing new.
func (s *Store) KeepDurations(ctx context.Context, roots MediaRoots) {
	probe := probeTool(roots.FFmpeg)
	if probe == "" {
		log.Print("video durations off: no ffprobe beside the frame extractor")
		return
	}
	for {
		started := time.Now()
		seen, _ := s.CatalogueGeneration(ctx)
		read, err := s.FillDurations(ctx, roots, probe)
		if err != nil && ctx.Err() == nil {
			log.Printf("video durations: %v", err)
		} else if read > 0 {
			log.Printf("video durations: read %d in %s", read, time.Since(started).Round(time.Second))
		}
		if !s.waitForChange(ctx, seen, durationEvery) {
			return
		}
	}
}

type durationTarget struct {
	id       int64
	relative string
	size     int64
}

// FillDurations reads the running time of every video that has none yet, or
// whose size has changed since it was read, and returns how many it recorded.
// A file that cannot be opened is left for a later pass; one the prober cannot
// read is recorded as zero, so it is not tried again until it changes.
func (s *Store) FillDurations(ctx context.Context, roots MediaRoots, probe string) (int, error) {
	targets, err := s.durationTargets(ctx)
	if err != nil || len(targets) == 0 {
		return 0, err
	}
	work := make(chan durationTarget)
	var mu sync.Mutex
	var recorded int
	var firstErr error
	var wg sync.WaitGroup
	for range durationWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range work {
				seconds, ok := readDuration(ctx, roots, probe, target)
				if !ok {
					continue
				}
				_, err := s.write.ExecContext(ctx, `INSERT INTO video_durations(asset_id,size_bytes,seconds) VALUES(?,?,?)
					ON CONFLICT(asset_id) DO UPDATE SET size_bytes=excluded.size_bytes,seconds=excluded.seconds`, target.id, target.size, seconds)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				} else if err == nil {
					recorded++
				}
				mu.Unlock()
			}
		}()
	}
	for _, target := range targets {
		select {
		case work <- target:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return recorded, firstErr
}

// durationTargets lists the videos still to be read. Kind is not trusted on
// its own, since the earlier tool recorded some MPG clips as images, so the
// extension decides; files in the Bin or deleted from it are left alone.
func (s *Store) durationTargets(ctx context.Context) ([]durationTarget, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.size_bytes FROM assets a
		LEFT JOIN video_durations d ON d.asset_id=a.id
		WHERE (d.asset_id IS NULL OR d.size_bytes<>a.size_bytes)
		AND NOT EXISTS (SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state IN ('bin','purged'))
		ORDER BY a.captured_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []durationTarget
	for rows.Next() {
		var target durationTarget
		if err = rows.Scan(&target.id, &target.relative, &target.size); err != nil {
			return nil, err
		}
		if videoContainer[strings.ToLower(filepath.Ext(target.relative))] {
			targets = append(targets, target)
		}
	}
	return targets, rows.Err()
}

// readDuration opens one file through its read-only mount, as the media
// handler does, and asks the prober how long it runs. ok is false when the
// file could not be reached, which says nothing about the file itself.
func readDuration(ctx context.Context, roots MediaRoots, probe string, target durationTarget) (seconds float64, ok bool) {
	mountRoot, inMount, known := roots.root(target.relative, target.id)
	if !known {
		return 0, false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return 0, false
	}
	defer root.Close()
	file, err := root.Open(inMount)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, durationTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", "/dev/fd/3")
	command.ExtraFiles = []*os.File{file}
	out, err := command.Output()
	if ctx.Err() != nil {
		// Cut short, not unreadable: try again on a later pass.
		return 0, false
	}
	if err != nil {
		return 0, true
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, true
	}
	return value, true
}

// markDurations sets Duration on the given files that have a running time.
func (s *Store) markDurations(ctx context.Context, assets []*Asset) error {
	byID := make(map[int64]*Asset, len(assets))
	ids := make([]any, 0, len(assets))
	marks := make([]string, 0, len(assets))
	for _, a := range assets {
		if videoContainer[strings.ToLower(filepath.Ext(a.Path))] {
			byID[a.ID] = a
			ids = append(ids, a.ID)
			marks = append(marks, "?")
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.read.QueryContext(ctx, `SELECT d.asset_id,d.seconds FROM video_durations d JOIN assets a ON a.id=d.asset_id
		WHERE d.asset_id IN (`+strings.Join(marks, ",")+`) AND d.size_bytes=a.size_bytes AND d.seconds>0`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var seconds sql.NullFloat64
		if err = rows.Scan(&id, &seconds); err != nil {
			return err
		}
		if a := byID[id]; a != nil && seconds.Valid {
			a.Duration = seconds.Float64
		}
	}
	return rows.Err()
}
