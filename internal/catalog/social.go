package catalog

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Videos saved from Instagram and similar apps are identified from container headers
// and six greyscale thumbnails, never from a model. The report carries the findings;
// this file only stores and serves them, so the ranking lives in one place.
//
// The bands below are review queues, not verdicts. Scores at or above socialLikely were
// unambiguous Stories in every sample checked against the pixels; the band beneath it ran
// about half true, which is why it is shown separately rather than merged.
const (
	socialLikely   = 10
	socialPossible = 6
)

// SocialItem is a video in the archive that was probably saved from a social
// app, with what the detection scan found.
type SocialItem struct {
	Asset
	// Day is the day the video is filed under, as YYYY-MM-DD, or empty when
	// its path holds none.
	Day string `json:"day"`
	// Name is the file's name.
	Name string `json:"name"`
	// Score is how strongly the scan's evidence points to a social app; the
	// higher, the likelier.
	Score int `json:"score"`
	// Band is the score's band: likely for 10 or more, possible for 6 to 9,
	// and watch below 6.
	Band string `json:"band"`
	// Evidence lists what the scan found, in words separated by commas, such
	// as "uuid filename, low bpp".
	Evidence string `json:"evidence"`
	// Width is the picture's width in pixels as it is shown once Cull has read
	// the file, or else as the scan found it stored.
	Width int `json:"width"`
	// Height is the picture's height in pixels, read the same way as Width.
	Height int `json:"height"`
	// Duration is how long the video runs, in seconds, as the scan found it.
	Duration float64 `json:"duration"`
	// Letterbox is true when the scan found a black bar at the top of the
	// frame, as Stories have.
	Letterbox bool `json:"letterbox"`
	// Poster is true when the scan kept a frame of the video, served by GET
	// /api/social-poster/{id}.
	Poster bool `json:"poster"`
}

// SocialPage is one page of videos probably saved from social apps, with
// counts for each band. Every count leaves out videos already kept or removed,
// except Kept and Marked.
type SocialPage struct {
	// Items are this page's videos, highest score first.
	Items []SocialItem `json:"items"`
	// Total counts the candidates still awaiting a decision, in every band.
	Total int `json:"total"`
	// Shown counts those in the band asked for, so the pager measures the
	// filtered list rather than the set.
	Shown int `json:"shown"`
	// Bytes is the size of every candidate still awaiting a decision, in
	// bytes.
	Bytes int64 `json:"bytes"`
	// Likely counts the candidates scoring 10 or more.
	Likely int `json:"likely"`
	// Possible counts the candidates scoring 6 to 9.
	Possible int `json:"possible"`
	// Watch counts the candidates scoring below 6.
	Watch int `json:"watch"`
	// Letterboxed counts the candidates with a Story letterbox, whatever their
	// score.
	Letterboxed int `json:"letterboxed"`
	// Social counts the candidates the page calls likely from a social app:
	// strong evidence, scoring 10 or more, or a Story letterbox, which ran
	// about nine in ten true.
	Social int `json:"social"`
	// Unsure counts every other candidate.
	Unsure int `json:"unsure"`
	// Kept counts the videos the reviewer kept as not from a social app, kept
	// visible so the work shows.
	Kept int `json:"kept"`
	// Marked counts the videos the reviewer removed.
	Marked int `json:"marked"`
}

// A candidate leaves this page the moment it is decided. Keeping it says it is not
// a social video; marking it hands it to the Bin, which is the only thing in this
// programme that touches a file. Both are reversible, so nothing here is final.
const socialPending = " AND COALESCE(d.status,'unreviewed') NOT IN ('keep','cull')"

func socialBand(score int) string {
	switch {
	case score >= socialLikely:
		return "likely"
	case score >= socialPossible:
		return "possible"
	default:
		return "watch"
	}
}

// ImportSocialReport replaces the derived candidate rows from a detection report.
// It reads the report only: no media is opened and no archive file is touched.
// A candidate already catalogued is linked to that asset so its decisions survive;
// one the catalogue has not seen yet is recorded from the report's own metadata.
func (s *Store) ImportSocialReport(ctx context.Context, input io.Reader, archivePrefix string) (int, error) {
	reader := csv.NewReader(input)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	header, err := reader.Read()
	if err != nil {
		return 0, err
	}
	if len(header) < 10 || header[0] != "score" {
		return 0, fmt.Errorf("social report is missing its header row")
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Rows are derived, so they are rebuilt wholesale rather than merged.
	if _, err = tx.ExecContext(ctx, "DELETE FROM social_items"); err != nil {
		return 0, err
	}
	count := 0
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
		if len(record) < 10 {
			continue
		}
		score, scoreErr := strconv.Atoi(record[0])
		if scoreErr != nil {
			return 0, fmt.Errorf("invalid social score %q", record[0])
		}
		logicalPath, ok := mappedPath(record[1], archivePrefix, "/archive")
		if !ok {
			return 0, fmt.Errorf("social candidate outside configured archive: %s", record[1])
		}
		// The detector walked the share, the Bin's .culled folder included. A
		// file there has already left the archive, and the Bin refuses any path
		// through a hidden folder, so it is not a candidate.
		if hiddenSegment(logicalPath) {
			continue
		}
		size, _ := strconv.ParseInt(record[3], 10, 64)
		width, _ := strconv.Atoi(record[4])
		height, _ := strconv.Atoi(record[5])
		duration, _ := strconv.ParseFloat(record[6], 64)
		top, _ := strconv.Atoi(record[7])
		bottom, _ := strconv.Atoi(record[8])
		poster := strings.TrimSpace(record[9])
		if strings.ContainsAny(poster, `/\`) || poster == "." || poster == ".." {
			return 0, fmt.Errorf("social poster name is not a plain filename: %q", poster)
		}

		day, _ := archiveDay(logicalPath, 0)
		captured := int64(0)
		if day != "" {
			if parsed, parseErr := time.Parse("2006-01-02", day); parseErr == nil {
				captured = parsed.Unix()
			}
		}
		var assetID int64
		switch scanErr := tx.QueryRowContext(ctx, "SELECT id FROM assets WHERE source_id='archive' AND relative_path=?", logicalPath).Scan(&assetID); {
		case scanErr == nil:
		case errors.Is(scanErr, sql.ErrNoRows):
			if assetID, err = upsertExternalAsset(ctx, tx, "archive", logicalPath, size, captured); err != nil {
				return 0, err
			}
		default:
			return 0, scanErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO social_items(asset_id,path,day,name,size_bytes,score,evidence,width,height,duration,letterbox_top,letterbox_bottom,poster,state)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'waiting')
			ON CONFLICT(asset_id) DO UPDATE SET path=excluded.path,day=excluded.day,name=excluded.name,size_bytes=excluded.size_bytes,score=excluded.score,evidence=excluded.evidence,width=excluded.width,height=excluded.height,duration=excluded.duration,letterbox_top=excluded.letterbox_top,letterbox_bottom=excluded.letterbox_bottom,poster=excluded.poster,state='waiting'`,
			assetID, logicalPath, nullable(day), path.Base(logicalPath), size, score, record[2], width, height, duration, top, bottom, poster); err != nil {
			return 0, err
		}
		count++
	}
	if count == 0 {
		return 0, fmt.Errorf("no social candidates imported")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('social_indexed_at',datetime('now')) ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

// SocialPoster returns the stored poster filename for a candidate, or "" when it has none.
// The name is a plain filename validated at import; the caller joins it to its own root.
func (s *Store) SocialPoster(ctx context.Context, assetID int64) (string, error) {
	var poster string
	err := s.read.QueryRowContext(ctx, "SELECT poster FROM social_items WHERE asset_id=? AND state='waiting'", assetID).Scan(&poster)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(poster, `/\`) || poster == "." || poster == ".." {
		return "", ErrInvalid
	}
	return poster, nil
}

func (s *Store) SocialCandidates(ctx context.Context, band string, from, limit int) (SocialPage, error) {
	page := SocialPage{Items: make([]SocialItem, 0)}
	if band != "" && band != "likely" && band != "possible" && band != "watch" && band != "letterboxed" && band != "social" && band != "unsure" {
		return page, ErrInvalid
	}
	if from < 0 || limit < 1 || limit > 200 {
		return page, ErrInvalid
	}
	if err := s.read.QueryRowContext(ctx, `WITH candidate AS (
		SELECT s.size_bytes,s.score,s.letterbox_top,
		       COALESCE(d.status,'unreviewed') NOT IN ('keep','cull') AS pending,
		       COALESCE(d.status,'unreviewed') AS status
		FROM social_items s LEFT JOIN decisions d ON d.asset_id=s.asset_id
		WHERE s.state='waiting')
		SELECT
		COALESCE(sum(pending),0),COALESCE(sum(CASE WHEN pending THEN size_bytes ELSE 0 END),0),
		COALESCE(sum(pending AND score>=?),0),COALESCE(sum(pending AND score>=? AND score<?),0),
		COALESCE(sum(pending AND score<?),0),COALESCE(sum(pending AND letterbox_top>0),0),
		COALESCE(sum(status='keep'),0),COALESCE(sum(status='cull'),0),
		COALESCE(sum(pending AND (score>=? OR letterbox_top>0)),0)
		FROM candidate`,
		socialLikely, socialPossible, socialLikely, socialPossible, socialLikely).
		Scan(&page.Total, &page.Bytes, &page.Likely, &page.Possible, &page.Watch, &page.Letterboxed,
			&page.Kept, &page.Marked, &page.Social); err != nil {
		return page, err
	}
	page.Unsure = page.Total - page.Social
	where, args := socialFilter(band)
	countArgs := append([]any(nil), args...)
	if err := s.read.QueryRowContext(ctx,
		`SELECT count(*) FROM social_items social LEFT JOIN decisions d ON d.asset_id=social.asset_id
		WHERE social.state='waiting'`+socialPending+where, countArgs...).
		Scan(&page.Shown); err != nil {
		return page, err
	}
	// The report's width and height are the stored frame, which a phone keeps
	// on its side; the catalogue's own reading is the shape as shown, so it
	// wins once the file has been read at its current size.
	query := `SELECT a.id,a.relative_path,a.captured_at,a.kind,a.size_bytes,COALESCE(d.status,'unreviewed'),COALESCE(d.favourite,0),COALESCE(d.revision,0),a.source_id,
		(SELECT count(*) FROM assets alt WHERE alt.anchor_id=a.id),` + relatedCount + `,
		COALESCE(social.day,''),social.name,social.score,social.evidence,COALESCE(m.width,social.width),COALESCE(m.height,social.height),social.duration,social.letterbox_top,social.poster
		FROM assets a LEFT JOIN decisions d ON d.asset_id=a.id JOIN social_items social ON social.asset_id=a.id
		LEFT JOIN media_shapes m ON m.asset_id=a.id AND m.size_bytes=a.size_bytes AND m.width>0 AND m.height>0
		WHERE social.state='waiting'` + socialPending + where + ` ORDER BY social.score DESC,COALESCE(social.day,''),social.name LIMIT ? OFFSET ?`
	args = append(args, limit, from)
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SocialItem
		var top int
		var poster string
		if err = rows.Scan(&item.Asset.ID, &item.Asset.Path, &item.Asset.CapturedAt, &item.Asset.Kind, &item.Asset.Size, &item.Asset.Status, &item.Asset.Favourite, &item.Asset.Revision, &item.Asset.Source, &item.Asset.AlternativeCount, &item.Asset.RelatedCount, &item.Day, &item.Name, &item.Score, &item.Evidence, &item.Width, &item.Height, &item.Duration, &top, &poster); err != nil {
			return page, err
		}
		item.Band = socialBand(item.Score)
		item.Letterbox = top > 0
		item.Poster = poster != ""
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

func socialFilter(band string) (string, []any) {
	switch band {
	case "likely":
		return " AND social.score>=?", []any{socialLikely}
	case "possible":
		return " AND social.score>=? AND social.score<?", []any{socialPossible, socialLikely}
	case "watch":
		return " AND social.score<?", []any{socialPossible}
	case "letterboxed":
		return " AND social.letterbox_top>0", nil
	case "social":
		return " AND (social.score>=? OR social.letterbox_top>0)", []any{socialLikely}
	case "unsure":
		return " AND social.score<? AND social.letterbox_top=0", []any{socialLikely}
	default:
		return "", nil
	}
}

// SocialPosterHandler serves the still frame captured for a candidate during detection.
// It is read-only and confined to posterRoot: the filename comes from the catalogue,
// is validated as a plain name, and no part of the request reaches the filesystem.
func (s *Store) SocialPosterHandler(posterRoot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "read only", 405)
			return
		}
		if posterRoot == "" {
			http.NotFound(w, r)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		poster, err := s.SocialPoster(ctx, id)
		cancel()
		if err != nil || poster == "" {
			http.NotFound(w, r)
			return
		}
		file, err := os.Open(filepath.Join(posterRoot, poster))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		http.ServeContent(w, r, poster, info.ModTime(), file)
	})
}
