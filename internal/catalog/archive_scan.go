package catalog

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The catalogue began as a one-off import, and the old app's indexer, which
// kept that import current, has been retired. Files graduating from iCloud
// every night therefore reached the archive without ever reaching review.
// ScanArchive is the replacement: it walks the archive's day folders and adds
// what the catalogue does not hold yet.

// The media the catalogue holds: what the import held, plus PNG. The old app
// left PNGs to its Screenshots page, which no longer looks at the archive, so
// one that gets past graduation is catalogued like any other file and shows
// on its day for review.
var archiveMediaExtensions = map[string]bool{
	"heic": true, "heif": true, "jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "avif": true, "tif": true, "tiff": true,
	"mov": true, "mp4": true, "m4v": true, "avi": true, "mkv": true, "3gp": true, "mpg": true, "mpeg": true,
	"arw": true, "dng": true, "cr2": true, "nef": true, "raf": true, "orf": true,
}

var (
	archiveYearDir  = regexp.MustCompile(`^\d{4}$`)
	archiveMonthDir = regexp.MustCompile(`^\d{4}-\d{2}$`)
	archiveDayDir   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

type ArchiveScanResult struct {
	Files       int      `json:"files"`
	Added       int      `json:"added"`
	AddedBytes  int64    `json:"addedBytes"`
	Screenshots []string `json:"screenshots"`
	// Catalogued archive files no longer on disk. Reported, never removed:
	// their decisions and history stay.
	Missing int `json:"missing"`
}

type archiveScanFile struct {
	path     string
	size     int64
	captured int64
}

// ScanArchive adds media under root's YYYY/YYYY-MM/YYYY-MM-DD folders that the
// catalogue does not hold, as archive assets under "/archive/...". It reads
// directory entries and stat metadata only, never file content, changes no
// existing row, and skips hidden files and folders such as .live-photos.
func (s *Store) ScanArchive(ctx context.Context, root string) (ArchiveScanResult, error) {
	result := ArchiveScanResult{Screenshots: make([]string, 0)}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return result, err
	}
	// Known paths, and whether each should be on disk: one Cull has moved to
	// the Bin is expected to be gone.
	known := map[string]bool{}
	rows, err := s.read.QueryContext(ctx, "SELECT a.relative_path,fs.asset_id IS NULL FROM assets a LEFT JOIN file_state fs ON fs.asset_id=a.id WHERE a.source_id='archive'")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var p string
		var onDisk bool
		if err = rows.Scan(&p, &onDisk); err != nil {
			rows.Close()
			return result, err
		}
		known[p] = onDisk
	}
	if err = rows.Close(); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	found := make([]archiveScanFile, 0)
	err = walkArchiveDays(resolved, func(dayDir, day string) error {
		start, parseErr := time.Parse("2006-01-02", day)
		if parseErr != nil {
			return nil
		}
		return filepath.WalkDir(dayDir, func(full string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if !archiveMediaExtensions[strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), "."))] {
				return nil
			}
			info, statErr := entry.Info()
			if statErr != nil {
				if os.IsNotExist(statErr) {
					return nil
				}
				return statErr
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, relErr := filepath.Rel(resolved, full)
			if relErr != nil {
				return relErr
			}
			logical := "/archive/" + filepath.ToSlash(rel)
			result.Files++
			seen[logical] = true
			if _, ok := known[logical]; ok {
				return nil
			}
			// The folder is the file's day. Within it the file's own time orders
			// the day, where it agrees; otherwise the day's start, as the import
			// recorded files it had no time for.
			captured := start.Unix()
			if mtime := info.ModTime().Unix(); mtime >= captured && mtime < captured+86400 {
				captured = mtime
			}
			found = append(found, archiveScanFile{path: logical, size: info.Size(), captured: captured})
			return nil
		})
	})
	if err != nil {
		return result, err
	}
	for p, onDisk := range known {
		if onDisk && !seen[p] {
			result.Missing++
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO assets(relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,?,'archive') ON CONFLICT(source_id,relative_path) DO NOTHING")
	if err != nil {
		return result, err
	}
	defer stmt.Close()
	for _, file := range found {
		r, insertErr := stmt.ExecContext(ctx, file.path, file.captured, assetKind(file.path), file.size)
		if insertErr != nil {
			return result, insertErr
		}
		if n, _ := r.RowsAffected(); n == 0 {
			continue
		}
		result.Added++
		result.AddedBytes += file.size
		if ClassifyScreenshot(file.path) != "" {
			result.Screenshots = append(result.Screenshots, file.path)
		}
	}
	if result.Added > 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE stats SET total=total+? WHERE id=1", result.Added); err != nil {
			return result, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('archive_scanned_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if result.Added == 0 {
		return result, nil
	}
	// New files need their days and their related groups, exactly as a
	// reindex from Settings builds them.
	if err = s.IndexRelated(ctx); err != nil {
		return result, err
	}
	if err = s.IndexCalendar(ctx); err != nil {
		return result, err
	}
	_, err = s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('snapshot_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value")
	return result, err
}

// walkArchiveDays calls visit for each YYYY/YYYY-MM/YYYY-MM-DD folder whose
// names agree with each other, so nothing outside the dated tree is read.
func walkArchiveDays(root string, visit func(dir, day string) error) error {
	years, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, year := range years {
		if !year.IsDir() || !archiveYearDir.MatchString(year.Name()) {
			continue
		}
		months, err := os.ReadDir(filepath.Join(root, year.Name()))
		if err != nil {
			return err
		}
		for _, month := range months {
			if !month.IsDir() || !archiveMonthDir.MatchString(month.Name()) || !strings.HasPrefix(month.Name(), year.Name()+"-") {
				continue
			}
			days, err := os.ReadDir(filepath.Join(root, year.Name(), month.Name()))
			if err != nil {
				return err
			}
			for _, day := range days {
				if !day.IsDir() || !archiveDayDir.MatchString(day.Name()) || !strings.HasPrefix(day.Name(), month.Name()+"-") {
					continue
				}
				if err = visit(filepath.Join(root, year.Name(), month.Name(), day.Name()), day.Name()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
