package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A grid lays photos out in rows at their own shapes, as Google Photos does,
// so each file's width and height, as it is shown, are read once with the
// metadata reader and kept in the catalogue. A photo turned by its
// orientation tag, or a clip turned by its rotation, is kept turned. The size
// it was read at is kept with it: a file replaced by a different one is read
// again.

// shapeWorkers is how many files are read at once. The reader looks at a
// file's header only, so a few at a time is plenty and leaves the preview
// workers to the tiles.
const shapeWorkers = 4

// shapeTimeout bounds one read.
const shapeTimeout = 30 * time.Second

// shapeEvery is the longest a new file waits for its shape; a changed
// catalogue starts a pass sooner.
const shapeEvery = 15 * time.Minute

// KeepShapes fills in shapes now and then again every so often, until ctx
// ends. It is quiet when there is nothing new.
func (s *Store) KeepShapes(ctx context.Context, roots MediaRoots) {
	if roots.RawTool == "" {
		log.Print("photo shapes off: no metadata reader")
		return
	}
	for {
		started := time.Now()
		seen, _ := s.CatalogueGeneration(ctx)
		read, err := s.FillShapes(ctx, roots)
		if err != nil && ctx.Err() == nil {
			log.Printf("photo shapes: %v", err)
		} else if read > 0 {
			log.Printf("photo shapes: read %d in %s", read, time.Since(started).Round(time.Second))
		}
		if !s.waitForChange(ctx, seen, shapeEvery) {
			return
		}
	}
}

type shapeTarget struct {
	id       int64
	relative string
	size     int64
}

// FillShapes reads the shape of every file that has none yet, or whose size
// has changed since it was read, and returns how many it recorded. A file
// that cannot be opened is left for a later pass; one the reader cannot make
// sense of is recorded as 0 by 0, so it is not tried again until it changes,
// and its tile measures the picture it is shown instead.
func (s *Store) FillShapes(ctx context.Context, roots MediaRoots) (int, error) {
	targets, err := s.shapeTargets(ctx)
	if err != nil || len(targets) == 0 {
		return 0, err
	}
	work := make(chan shapeTarget)
	var mu sync.Mutex
	var recorded int
	var firstErr error
	var wg sync.WaitGroup
	for range shapeWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range work {
				width, height, ok := readShape(ctx, roots, target)
				if !ok {
					continue
				}
				_, err := s.write.ExecContext(ctx, `INSERT INTO media_shapes(asset_id,size_bytes,width,height) VALUES(?,?,?,?)
					ON CONFLICT(asset_id) DO UPDATE SET size_bytes=excluded.size_bytes,width=excluded.width,height=excluded.height`, target.id, target.size, width, height)
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

// shapeTargets lists the files still to be read, newest first so the days
// being reviewed now fill in before the old library does. Files in the Bin or
// deleted from it are left alone.
func (s *Store) shapeTargets(ctx context.Context) ([]shapeTarget, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT a.id,a.relative_path,a.size_bytes FROM assets a
		LEFT JOIN media_shapes m ON m.asset_id=a.id
		WHERE (m.asset_id IS NULL OR m.size_bytes<>a.size_bytes)
		AND NOT EXISTS (SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state IN ('bin','purged'))
		ORDER BY a.captured_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []shapeTarget
	for rows.Next() {
		var target shapeTarget
		if err = rows.Scan(&target.id, &target.relative, &target.size); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// readShape opens one file through its read-only mount, as the media handler
// does, and asks the metadata reader for its size and turn. ok is false when
// the file could not be reached, which says nothing about the file itself.
func readShape(ctx context.Context, roots MediaRoots, target shapeTarget) (width, height int, ok bool) {
	mountRoot, inMount, known := roots.root(target.relative, target.id)
	if !known {
		return 0, 0, false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return 0, 0, false
	}
	defer root.Close()
	file, err := root.Open(inMount)
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, shapeTimeout)
	defer cancel()
	// -fast stops before the end of the file, which is where a phone video's
	// index sometimes sits, but after the track headers that hold its size.
	command := exec.CommandContext(ctx, roots.RawTool, "-j", "-n", "-fast", "-ImageWidth", "-ImageHeight", "-Orientation", "-Rotation", "/dev/fd/3")
	command.ExtraFiles = []*os.File{file}
	out, err := command.Output()
	if ctx.Err() != nil {
		// Cut short, not unreadable: try again on a later pass.
		return 0, 0, false
	}
	if err != nil {
		return 0, 0, true
	}
	width, height = parseShape(out)
	return width, height, true
}

// parseShape reads the metadata reader's answer: the stored size, swapped
// when an orientation tag or a rotation turns the picture a quarter turn.
func parseShape(out []byte) (width, height int) {
	var answers []struct {
		ImageWidth  json.Number
		ImageHeight json.Number
		Orientation json.Number
		Rotation    json.Number
	}
	if json.Unmarshal(out, &answers) != nil || len(answers) != 1 {
		return 0, 0
	}
	answer := answers[0]
	w, errW := answer.ImageWidth.Float64()
	h, errH := answer.ImageHeight.Float64()
	if errW != nil || errH != nil || w < 1 || h < 1 || w > 1e6 || h > 1e6 {
		return 0, 0
	}
	width, height = int(w), int(h)
	orientation, _ := answer.Orientation.Int64()
	rotation, _ := answer.Rotation.Float64()
	quarter := int(rotation) % 180
	if orientation >= 5 && orientation <= 8 || quarter == 90 || quarter == -90 {
		width, height = height, width
	}
	return width, height
}

// markShapes sets Width and Height on the given files that have been read,
// and Turn on those the reviewer turned.
func (s *Store) markShapes(ctx context.Context, assets []*Asset) error {
	if len(assets) == 0 {
		return nil
	}
	byID := make(map[int64]*Asset, len(assets))
	ids := make([]any, 0, len(assets))
	marks := make([]string, 0, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
		ids = append(ids, a.ID)
		marks = append(marks, "?")
	}
	rows, err := s.read.QueryContext(ctx, `SELECT m.asset_id,m.width,m.height FROM media_shapes m JOIN assets a ON a.id=m.asset_id
		WHERE m.asset_id IN (`+strings.Join(marks, ",")+`) AND m.size_bytes=a.size_bytes AND m.width>0 AND m.height>0`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var width, height sql.NullInt64
		if err = rows.Scan(&id, &width, &height); err != nil {
			return err
		}
		if a := byID[id]; a != nil && width.Valid && height.Valid {
			a.Width, a.Height = int(width.Int64), int(height.Int64)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return s.markTurns(ctx, byID, ids, marks)
}
