package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

// Apple Photos works out a great deal about every picture for its Memories:
// an overall aesthetic score, whether the shot failed, whether its subject is
// sharp, how dark it is, which faces it found and how well each came out, what
// is in it, and a one-line caption. The archive was filled from the same
// pictures, so that work is worth having here, as hints beside each file on a
// day page. None of it is reachable through PhotoKit; a script run by
// osxphotos on the Mac reads it from the library's own database and sends it
// to /api/photos/agent/scores with the Cull Sync key. This file stores what
// arrives and matches it to the archive's files when a day is read.
//
// Hints are evidence, like fingerprints, never a decision. They are kept in
// their own table, versioned, apart from decisions and from the exact
// duplicate proof, and a day page reads the same whether they are there or
// not. Matching is by the name Photos knows a file by and the day it was
// taken, the rule photos.go uses to hand deletions across, with the same two
// cautions: a name found only a day off is taken only when it is the one
// bearer of that name within a day, and not when the archive holds a file of
// that name on the day Photos has, since the picture is then that file's.

// PhotosScoresVersion is the shape of what the script sends; a report in
// another version is refused rather than misread.
const PhotosScoresVersion = "apple-photos-v1"

const (
	photosScoresPageMax   = 2000
	photosScoresLabelsMax = 20
	photosScoresLabelLen  = 60
	photosScoresCaption   = 200
)

// PhotosScore is what Photos holds about one of its items. Every score is
// Apple's own number; one it has not worked out is left out, which is not
// the same as 0.
type PhotosScore struct {
	// PhotosID is the item's identifier in Photos, as osxphotos reports it.
	PhotosID string `json:"photosId"`
	// Name is the filename Photos imported the item under.
	Name string `json:"name"`
	// Day is the day it was taken in the Mac's time zone, as YYYY-MM-DD, or
	// empty when Photos has no date for it.
	Day string `json:"day"`
	// Kind is image or video.
	Kind string `json:"kind"`
	// Size is the original file's size in bytes, when Photos knows it.
	Size int64 `json:"size"`
	// Favourite is whether the item has a heart in Photos.
	Favourite bool `json:"favourite"`
	// Hidden is whether the item is hidden in Photos.
	Hidden bool `json:"hidden"`
	// Adjusted is whether the item was edited in Photos.
	Adjusted bool `json:"adjusted"`
	// Deleted means the item is in Recently Deleted: still scored, not yet gone.
	Deleted bool `json:"deleted"`
	// Overall is Apple's overall aesthetic score, 0 to 1.
	Overall *float64 `json:"overall,omitempty"`
	// Curation is how far Photos would pick it for Memories, 0 to 1.
	Curation *float64 `json:"curation,omitempty"`
	// Failure is below 0 when Photos thinks the shot failed.
	Failure *float64 `json:"failure,omitempty"`
	// Sharp is how sharply focused the subject is, 0 to 1.
	Sharp *float64 `json:"sharp,omitempty"`
	// Blur is tasteful blur above 0 and accidental blur below it.
	Blur *float64 `json:"blur,omitempty"`
	// LowLight is how dark the picture is, 0 to 1.
	LowLight *float64 `json:"lowLight,omitempty"`
	// Noise is below 0 for a noisy picture.
	Noise *float64 `json:"noise,omitempty"`
	// Intrusive is below 0 when something is in the way of the subject.
	Intrusive *float64 `json:"intrusive,omitempty"`
	// Composition is Apple's judgement of the composition, -1 to 1.
	Composition *float64 `json:"composition,omitempty"`
	// Framing is how well the subject is framed, -1 to 1.
	Framing *float64 `json:"framing,omitempty"`
	// Subject is how well chosen the subject is, -1 to 1.
	Subject *float64 `json:"subject,omitempty"`
	// Interesting is how interesting the subject is, -1 to 1.
	Interesting *float64 `json:"interesting,omitempty"`
	// Timing is how well timed the shot is, -1 to 1.
	Timing *float64 `json:"timing,omitempty"`
	// Lighting is how pleasant the light is, -1 to 1.
	Lighting *float64 `json:"lighting,omitempty"`
	// Faces counts the faces Photos found.
	Faces int `json:"faces"`
	// FaceMin is the worst face's quality, 0 to 1, when there are faces.
	FaceMin *float64 `json:"faceMin,omitempty"`
	// FaceMax is the best face's quality, 0 to 1, when there are faces.
	FaceMax *float64 `json:"faceMax,omitempty"`
	// Smiles counts the faces Photos saw smiling.
	Smiles int `json:"smiles"`
	// Labels are what Photos saw in the picture, such as people or water,
	// up to 20.
	Labels []string `json:"labels"`
	// Caption is Apple's one-line description, up to 200 characters, or empty.
	Caption string `json:"caption"`
}

// PhotosScoresReport is one page of what the script read from Photos.
type PhotosScoresReport struct {
	// Version is PhotosScoresVersion.
	Version string `json:"version"`
	// Run names this reading of the library, the same on every page; once
	// the last page is in, items from earlier runs are dropped, so an item
	// deleted from Photos stops being a hint.
	Run string `json:"run"`
	// Items are up to 2,000 items.
	Items []PhotosScore `json:"items"`
	// Done marks the last page of the run.
	Done bool `json:"done"`
}

// PhotosScoresStored is the answer to a page.
type PhotosScoresStored struct {
	// Stored counts the page's items written.
	Stored int `json:"stored"`
	// Total counts the items held after this page, across runs.
	Total int `json:"total"`
}

// PhotosScoresState says what the server holds, for the Apple Photos page.
type PhotosScoresState struct {
	// Items counts the Photos items held.
	Items int `json:"items"`
	// SyncedAt is when the last page arrived, in RFC 3339 UTC; empty before
	// the first.
	SyncedAt string `json:"syncedAt,omitempty"`
}

func validScore(v *float64) bool {
	return v == nil || (!math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= -1 && *v <= 1)
}

func validPhotosScore(item PhotosScore) bool {
	if item.PhotosID == "" || len(item.PhotosID) > 200 || item.Name == "" || len(item.Name) > 255 {
		return false
	}
	if item.Day != "" && !photosDay.MatchString(item.Day) {
		return false
	}
	if item.Kind != "image" && item.Kind != "video" {
		return false
	}
	if item.Size < 0 || item.Faces < 0 || item.Smiles < 0 || item.Faces > 10000 || item.Smiles > item.Faces {
		return false
	}
	for _, v := range []*float64{item.Overall, item.Curation, item.Failure, item.Sharp, item.Blur, item.LowLight, item.Noise, item.Intrusive, item.Composition, item.Framing, item.Subject, item.Interesting, item.Timing, item.Lighting, item.FaceMin, item.FaceMax} {
		if !validScore(v) {
			return false
		}
	}
	if len(item.Labels) > photosScoresLabelsMax || len(item.Caption) > photosScoresCaption {
		return false
	}
	for _, label := range item.Labels {
		if label == "" || len(label) > photosScoresLabelLen {
			return false
		}
	}
	return true
}

// RecordPhotosScores writes one page of a run. Every item is checked before
// anything is written, so a page with one bad item changes nothing.
func (s *Store) RecordPhotosScores(ctx context.Context, report PhotosScoresReport, now time.Time) (PhotosScoresStored, error) {
	if report.Version != PhotosScoresVersion || report.Run == "" || len(report.Run) > 80 || len(report.Items) > photosScoresPageMax {
		return PhotosScoresStored{}, ErrInvalid
	}
	for _, item := range report.Items {
		if !validPhotosScore(item) {
			return PhotosScoresStored{}, ErrInvalid
		}
	}
	stamp := now.UTC().Format(time.RFC3339)
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return PhotosScoresStored{}, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO photos_scores(photos_id,stem,ext,day,name,kind,size_bytes,favourite,hidden,adjusted,deleted,
		overall,curation,failure,sharp,blur,low_light,noise,intrusive,composition,framing,subject,interesting,timing,lighting,
		faces,face_min,face_max,smiles,labels,caption,version,run,synced_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(photos_id) DO UPDATE SET stem=excluded.stem,ext=excluded.ext,day=excluded.day,name=excluded.name,kind=excluded.kind,size_bytes=excluded.size_bytes,
		favourite=excluded.favourite,hidden=excluded.hidden,adjusted=excluded.adjusted,deleted=excluded.deleted,
		overall=excluded.overall,curation=excluded.curation,failure=excluded.failure,sharp=excluded.sharp,blur=excluded.blur,low_light=excluded.low_light,noise=excluded.noise,intrusive=excluded.intrusive,
		composition=excluded.composition,framing=excluded.framing,subject=excluded.subject,interesting=excluded.interesting,timing=excluded.timing,lighting=excluded.lighting,
		faces=excluded.faces,face_min=excluded.face_min,face_max=excluded.face_max,smiles=excluded.smiles,labels=excluded.labels,caption=excluded.caption,version=excluded.version,run=excluded.run,synced_at=excluded.synced_at`)
	if err != nil {
		return PhotosScoresStored{}, err
	}
	defer stmt.Close()
	for _, item := range report.Items {
		stem, ext := photosSplit(item.Name)
		labels := item.Labels
		if labels == nil {
			labels = []string{}
		}
		encoded, _ := json.Marshal(labels)
		if _, err = stmt.ExecContext(ctx, item.PhotosID, PhotosNormalise(stem), ext, item.Day, item.Name, item.Kind, item.Size, item.Favourite, item.Hidden, item.Adjusted, item.Deleted,
			item.Overall, item.Curation, item.Failure, item.Sharp, item.Blur, item.LowLight, item.Noise, item.Intrusive, item.Composition, item.Framing, item.Subject, item.Interesting, item.Timing, item.Lighting,
			item.Faces, item.FaceMin, item.FaceMax, item.Smiles, string(encoded), item.Caption, report.Version, report.Run, stamp); err != nil {
			return PhotosScoresStored{}, err
		}
	}
	if report.Done {
		if _, err = tx.ExecContext(ctx, "DELETE FROM photos_scores WHERE run!=?", report.Run); err != nil {
			return PhotosScoresStored{}, err
		}
	}
	var total int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM photos_scores").Scan(&total); err != nil {
		return PhotosScoresStored{}, err
	}
	return PhotosScoresStored{Stored: len(report.Items), Total: total}, tx.Commit()
}

// PhotosScoresState counts what is held.
func (s *Store) PhotosScoresState(ctx context.Context) (PhotosScoresState, error) {
	var state PhotosScoresState
	var last sql.NullString
	err := s.read.QueryRowContext(ctx, "SELECT count(*),max(synced_at) FROM photos_scores").Scan(&state.Items, &last)
	state.SyncedAt = last.String
	return state, err
}

// AssetHint is what Apple Photos thought of a file, matched by name and day.
// It is a suggestion to look, never a decision.
type AssetHint struct {
	// Source is apple-photos.
	Source string `json:"source"`
	// How is exact when Photos has the name on the same day, and near when it
	// has it only a day either side, uniquely.
	How string `json:"how"`
	// Overall is Apple's aesthetic score, 0 to 1; left out when Photos has
	// not scored the file.
	Overall float64 `json:"overall,omitempty"`
	// Keep means Photos would pick the picture for Memories, which the
	// reviewer here mostly kept. Never set for a file Photos has not scored.
	Keep bool `json:"keep,omitempty"`
	// Cull means Photos marks the shot as failed, which the reviewer here
	// mostly removed. Never set for a file Photos has not scored.
	Cull bool `json:"cull,omitempty"`
	// Reasons say why, in words: "edited in Photos", "a face came out
	// poorly", "failed shot".
	Reasons []string `json:"reasons,omitempty"`
	// Faces counts the faces Photos found.
	Faces int `json:"faces,omitempty"`
	// Labels are what Photos saw in the picture.
	Labels []string `json:"labels,omitempty"`
	// Caption is Apple's one-line description of the picture, when it has one.
	Caption string `json:"caption,omitempty"`
}

// hintBands turns Apple's numbers into the two bands and their reasons.
// The thresholds were checked against 779 files this archive's reviewer had
// already decided, 388 of them culled (audit/verification/2026-10-10/
// calibration-by-kind.txt). Apple's overall score hardly separates them:
// kept and culled images both sit around 0.5, and every cut of it lands
// within a few points of the base rate. Two things did stand out. An image
// Photos rates 0.8 or more for Memories was kept 86% of the time against 67%
// for images overall, and a shot Photos marks as failed was nearly always
// culled, though that mark is rare. Those are the only two bands. Videos get
// no keep band: Photos' Memories pick ran the other way for them. The other
// reasons describe the picture, dark or blurred or a poor face, for the
// reviewer to weigh; none of them predicted the decision on its own.
func hintBands(row PhotosScore) (keep, cull bool, reasons []string) {
	val := func(v *float64) (float64, bool) {
		if v == nil {
			return 0, false
		}
		return *v, true
	}
	if row.Adjusted {
		reasons = append(reasons, "edited in Photos")
	}
	if row.Favourite {
		reasons = append(reasons, "favourite in Photos")
	}
	if _, scored := val(row.Overall); !scored {
		return false, false, reasons
	}
	if v, ok := val(row.Failure); ok && v < -0.05 {
		cull = true
		reasons = append(reasons, "Photos marks it a failed shot")
	}
	if sharp, ok := val(row.Sharp); ok && sharp < 0.005 {
		if blur, ok := val(row.Blur); ok && blur < -0.2 {
			reasons = append(reasons, "nothing in focus")
		}
	}
	if v, ok := val(row.FaceMin); ok && v < 0.1 {
		reasons = append(reasons, "a face came out poorly")
	}
	if v, ok := val(row.LowLight); ok && v > 0.5 {
		reasons = append(reasons, "dark")
	}
	if v, ok := val(row.Intrusive); ok && v < -0.3 {
		reasons = append(reasons, "something in the way")
	}
	if v, ok := val(row.Curation); ok && v >= 0.8 && row.Kind == "image" && !cull {
		keep = true
		reasons = append(reasons, "Photos would pick it for Memories")
	}
	return keep, cull, reasons
}

func hintFor(row PhotosScore, how string) *AssetHint {
	keep, cull, reasons := hintBands(row)
	hint := &AssetHint{Source: "apple-photos", How: how, Keep: keep, Cull: cull, Reasons: reasons, Faces: row.Faces, Labels: row.Labels, Caption: row.Caption}
	if row.Overall != nil {
		hint.Overall = *row.Overall
	}
	return hint
}

// photosScoreRows reads the held items dated within the given days.
func (s *Store) photosScoreRows(ctx context.Context, days []string) (map[string][]PhotosScore, error) {
	rows := map[string][]PhotosScore{}
	for start := 0; start < len(days); start += 500 {
		chunk := days[start:min(start+500, len(days))]
		args := make([]any, len(chunk))
		for i, day := range chunk {
			args[i] = day
		}
		result, err := s.read.QueryContext(ctx, `SELECT photos_id,stem,ext,day,name,kind,size_bytes,favourite,hidden,adjusted,deleted,
			overall,curation,failure,sharp,blur,low_light,noise,intrusive,composition,framing,subject,interesting,timing,lighting,
			faces,face_min,face_max,smiles,labels,caption FROM photos_scores WHERE day IN (?`+strings.Repeat(",?", len(chunk)-1)+") ORDER BY photos_id", args...)
		if err != nil {
			return nil, err
		}
		for result.Next() {
			var row PhotosScore
			var stem, ext, labels string
			if err = result.Scan(&row.PhotosID, &stem, &ext, &row.Day, &row.Name, &row.Kind, &row.Size, &row.Favourite, &row.Hidden, &row.Adjusted, &row.Deleted,
				&row.Overall, &row.Curation, &row.Failure, &row.Sharp, &row.Blur, &row.LowLight, &row.Noise, &row.Intrusive, &row.Composition, &row.Framing, &row.Subject, &row.Interesting, &row.Timing, &row.Lighting,
				&row.Faces, &row.FaceMin, &row.FaceMax, &row.Smiles, &labels, &row.Caption); err != nil {
				result.Close()
				return nil, err
			}
			if json.Unmarshal([]byte(labels), &row.Labels) != nil {
				row.Labels = nil
			}
			key := stem + "\x00" + ext + "\x00" + row.Day
			rows[key] = append(rows[key], row)
		}
		if err = result.Close(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// photosNamesOn lists the normalised stem and extension of every live
// archive file filed under a day, to tell whether a name Photos has on that
// day belongs to a file there.
func (s *Store) photosNamesOn(ctx context.Context, day string) (map[string]bool, error) {
	names := map[string]bool{}
	rows, err := s.read.QueryContext(ctx, `SELECT a.relative_path FROM assets a JOIN asset_days ad ON ad.asset_id=a.id WHERE ad.day=?
		AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
		AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)`, day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var file string
		if err = rows.Scan(&file); err != nil {
			rows.Close()
			return nil, err
		}
		name, _ := photosName(file, day)
		stem, ext := photosSplit(name)
		names[PhotosNormalise(stem)+"\x00"+ext] = true
	}
	return names, rows.Close()
}

func shiftDay(day string, days int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

// markHints attaches what Photos holds to the files it can be matched to.
// Nothing is attached when there is nothing held, so a library without the
// script costs one query per day read.
func (s *Store) markHints(ctx context.Context, assets []*Asset) error {
	if len(assets) == 0 {
		return nil
	}
	type want struct {
		asset    *Asset
		key, day string
	}
	wants := make([]want, 0, len(assets))
	daySet := map[string]bool{}
	for _, a := range assets {
		if a.Kind != "image" && a.Kind != "raw" && a.Kind != "video" {
			continue
		}
		day, ok := archiveDay(a.Path, a.CapturedAt)
		if !ok {
			continue
		}
		name, day := photosName(a.Path, day)
		if !photosDay.MatchString(day) {
			continue
		}
		stem, ext := photosSplit(name)
		wants = append(wants, want{asset: a, key: PhotosNormalise(stem) + "\x00" + ext, day: day})
		for _, d := range []string{shiftDay(day, -1), day, shiftDay(day, 1)} {
			if d != "" {
				daySet[d] = true
			}
		}
	}
	if len(wants) == 0 {
		return nil
	}
	days := make([]string, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Strings(days)
	rows, err := s.photosScoreRows(ctx, days)
	if err != nil || len(rows) == 0 {
		return err
	}
	namesOn := map[string]map[string]bool{}
	for _, w := range wants {
		if exact := rows[w.key+"\x00"+w.day]; len(exact) > 0 {
			w.asset.Hint = hintFor(exact[0], "exact")
			continue
		}
		var near []PhotosScore
		for _, d := range []string{shiftDay(w.day, -1), shiftDay(w.day, 1)} {
			near = append(near, rows[w.key+"\x00"+d]...)
		}
		if len(near) != 1 {
			continue
		}
		// Photos has the name only a day off. That is this file's picture
		// only when the archive holds no file of the name on Photos' own day.
		names, ok := namesOn[near[0].Day]
		if !ok {
			if names, err = s.photosNamesOn(ctx, near[0].Day); err != nil {
				return err
			}
			namesOn[near[0].Day] = names
		}
		if names[w.key] {
			continue
		}
		w.asset.Hint = hintFor(near[0], "near")
	}
	return nil
}
