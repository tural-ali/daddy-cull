package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The Import folder is where new photos arrive from outside the library: an
// SD card copied across, a phone's export, icloudpd's downloads. The writer
// files each one under the day it was taken, YYYY/YYYY-MM/YYYY-MM-DD, the
// layout the rest of Cull reads, and then catalogues the day so it waits for
// review. Nothing is ever deleted: a file the library already holds is moved
// aside into "Already in the library" for the person to look at, and a file
// Cull does not take stays where it is.

// IntakeDuplicates is the folder inside Import that files the library
// already holds are moved into.
const IntakeDuplicates = "Already in the library"

// intakeEvery is how often the Import folder is looked at. A file is taken
// only once it has stopped changing, so a copy still arriving is left alone.
const (
	intakeEvery  = time.Minute
	intakeRescan = time.Hour
	intakeSettle = 5 * time.Second
	intakeStatus = "intake_status"
	// exiftool is given this many files at a time.
	intakeDateBatch = 200
)

// Sidecars travel with the photo they describe, and are never taken alone.
var intakeSidecars = map[string]bool{"xmp": true, "aae": true}

// IntakeStatus is what the writer last found in the Import folder, kept in
// the settings table so the web process can show it.
type IntakeStatus struct {
	// Folder is the Import folder as the writer sees it.
	Folder string `json:"folder"`
	// LastRun is when the folder was last looked at, in RFC 3339.
	LastRun string `json:"lastRun,omitempty"`
	// Filed counts the files put in the library by the last look.
	Filed int `json:"filed"`
	// FiledTotal counts every file put in the library since the writer started.
	FiledTotal int `json:"filedTotal"`
	// Duplicates counts the files now in the "Already in the library" folder.
	Duplicates int `json:"duplicates"`
	// Waiting counts files still being copied in, left for the next look.
	Waiting int `json:"waiting"`
	// Unsupported counts files Cull does not take, such as documents, left in place.
	Unsupported int `json:"unsupported"`
	// Problem says what went wrong on the last look, if anything did.
	Problem string `json:"problem,omitempty"`
}

// Capture is what a photo or video says about itself.
type Capture struct {
	// Taken is when it was taken, as the clock where it was taken showed it,
	// or zero when the file does not say.
	Taken time.Time
	// LiveID is the identifier Apple gives both halves of a Live Photo, the
	// still and its video, in upper case; empty for anything else.
	LiveID string
}

// CaptureReader reads what each file says about itself, by absolute path. A
// file that says nothing is simply left out of the answer.
type CaptureReader func(ctx context.Context, files []string) (map[string]Capture, error)

// IntakeWriter files what arrives in the Import folder. It runs in the
// private writer, the only process that may change the library.
type IntakeWriter struct {
	s           *Store
	inbox       *intakeSource
	mirrors     []*intakeSource
	archive     *os.Root
	archivePath string
	capture     CaptureReader
	link        func(oldname, newname string) error
	settle      time.Duration
	wake        chan struct{}
	mu          sync.Mutex
	filedTotal  int
}

// NewIntakeWriter opens both folders. dateTool is exiftool's path; empty
// dates files by their names and modification times alone.
func NewIntakeWriter(s *Store, inboxRoot, archiveRoot, dateTool string) (*IntakeWriter, error) {
	inbox, err := openGuardedRoot(inboxRoot)
	if err != nil {
		return nil, fmt.Errorf("import folder %s: %w", inboxRoot, err)
	}
	archive, err := openGuardedRoot(archiveRoot)
	if err != nil {
		inbox.Close()
		return nil, fmt.Errorf("library folder %s: %w", archiveRoot, err)
	}
	inboxPath, _ := filepath.EvalSymlinks(filepath.Clean(inboxRoot))
	archivePath, _ := filepath.EvalSymlinks(filepath.Clean(archiveRoot))
	if within(inboxPath, archivePath) || within(archivePath, inboxPath) {
		inbox.Close()
		archive.Close()
		return nil, fmt.Errorf("the import folder and the library must not be inside each other")
	}
	w := &IntakeWriter{s: s, inbox: &intakeSource{root: inbox, path: inboxPath}, archive: archive, archivePath: archivePath,
		capture: exiftoolCapture(dateTool), link: os.Link, settle: intakeSettle, wake: make(chan struct{}, 1)}
	return w, nil
}

// intakeSource is a folder photos are filed from.
type intakeSource struct {
	root *os.Root
	path string
	// placeholders marks a folder a downloader fills, such as icloudpd's: each
	// file filed from it leaves an empty file of the same name, so the
	// downloader, which only asks whether a file of that name is there, does
	// not fetch it again.
	placeholders bool
}

// Mirror adds a folder a downloader fills, such as icloudpd's, whose photos
// are filed like the Import folder's. Each one filed leaves an empty
// placeholder of its name, and one the library already holds, byte for byte,
// is replaced by its placeholder, since the library's copy is the same file.
func (w *IntakeWriter) Mirror(root string) error {
	mirror, err := openGuardedRoot(root)
	if err != nil {
		return fmt.Errorf("download folder %s: %w", root, err)
	}
	resolved, _ := filepath.EvalSymlinks(filepath.Clean(root))
	others := []string{w.inbox.path, w.archivePath}
	for _, m := range w.mirrors {
		others = append(others, m.path)
	}
	for _, other := range others {
		if within(resolved, other) || within(other, resolved) {
			mirror.Close()
			return fmt.Errorf("the download folder %s must not be inside the import folder, the library or another download folder, nor they inside it", root)
		}
	}
	w.mirrors = append(w.mirrors, &intakeSource{root: mirror, path: resolved, placeholders: true})
	return nil
}

func (w *IntakeWriter) sources() []*intakeSource {
	return append([]*intakeSource{w.inbox}, w.mirrors...)
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Close releases both folders.
func (w *IntakeWriter) Close() error {
	first := w.inbox.root.Close()
	for _, m := range w.mirrors {
		m.root.Close()
	}
	if second := w.archive.Close(); first == nil {
		return second
	}
	return first
}

// Wake asks for a look at the Import folder now.
func (w *IntakeWriter) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Keep looks at the Import folder every minute, and whenever woken, until
// ctx ends. It also catalogues the library when it starts and every hour
// after, so photos put straight into a day folder, or a library that was
// there before Cull, wait for review too.
func (w *IntakeWriter) Keep(ctx context.Context) {
	var scanned time.Time
	for {
		if time.Since(scanned) > intakeRescan {
			if result, err := w.s.ScanArchive(ctx, w.archivePath); err != nil && ctx.Err() == nil {
				log.Printf("cataloguing the library: %v", err)
			} else if result.Added > 0 {
				log.Printf("catalogued %d files found in the library", result.Added)
			}
			scanned = time.Now()
		}
		if _, err := w.Pass(ctx); err != nil && ctx.Err() == nil {
			log.Printf("import folder: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-time.After(intakeEvery):
		}
	}
}

type intakeFile struct {
	src   *intakeSource
	rel   string
	size  int64
	mod   time.Time
	ext   string
	still bool
	// live is the file's Live Photo identifier, and of, for a Live Photo's
	// video, the still in its group it is the video of.
	live string
	of   string
}

type intakeGroup struct {
	dir   string
	stem  string
	loose string
	files []intakeFile
}

// intakeStem is the name shared by the files of one photo: a Live Photo's
// still and video, a RAW and its JPEG, and their sidecars.
func intakeStem(name string) string {
	stem := strings.ToLower(name)
	if ext := path.Ext(stem); intakeSidecars[strings.TrimPrefix(ext, ".")] {
		stem = strings.TrimSuffix(stem, ext)
		if inner := path.Ext(stem); archiveMediaExtensions[strings.TrimPrefix(inner, ".")] {
			stem = strings.TrimSuffix(stem, inner)
		}
	} else {
		stem = strings.TrimSuffix(stem, ext)
	}
	// icloudpd names a Live Photo's video after its still with _HEVC added.
	return strings.TrimSuffix(stem, "_hevc")
}

// looseTail is what may follow the name the two halves of a Live Photo share:
// the sizes icloudpd adds to tell photos of one name apart, a copy's " (2)",
// and the video's _HEVC. FullSizeRender-1198557.HEIC and
// FullSizeRender_HEVC-5510754.MOV are both fullsizerender.
var looseTail = regexp.MustCompile(`(?:_hevc|-[0-9]+| \([0-9]+\))+$`)

// looseStem is the name a Live Photo's still and video share however they
// were told apart, for finding the halves Apple's identifier may pair.
func looseStem(name string) string {
	stem := intakeStem(name)
	if loose := looseTail.ReplaceAllString(stem, ""); loose != "" {
		return loose
	}
	return stem
}

// isLiveClip reports whether a file may be the video of a Live Photo.
func isLiveClip(file intakeFile) bool {
	return liveClipExt["."+file.ext]
}

var stillExtensions = map[string]bool{"heic": true, "heif": true, "jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "avif": true, "tif": true, "tiff": true,
	"arw": true, "dng": true, "cr2": true, "nef": true, "raf": true, "orf": true}

// look lists one folder: the media and sidecars in it, and how many other
// files it holds.
func (w *IntakeWriter) look(ctx context.Context, src *intakeSource) (map[string]intakeFile, int, int, error) {
	files := map[string]intakeFile{}
	unsupported, duplicates := 0, 0
	err := fs.WalkDir(src.root.FS(), ".", func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if rel == IntakeDuplicates && !src.placeholders {
				duplicates += countFiles(src.root, rel)
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(entry.Name()), "."))
		// icloudpd downloads into a .part file and renames it when done.
		if src.placeholders && ext == "part" {
			return nil
		}
		if !archiveMediaExtensions[ext] && !intakeSidecars[ext] {
			unsupported++
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if src.placeholders && info.Size() == 0 {
			return nil
		}
		files[rel] = intakeFile{src: src, rel: rel, size: info.Size(), mod: info.ModTime(), ext: ext, still: stillExtensions[ext]}
		return nil
	})
	return files, unsupported, duplicates, err
}

func countFiles(root *os.Root, dir string) int {
	count := 0
	_ = fs.WalkDir(root.FS(), dir, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() && !strings.HasPrefix(entry.Name(), ".") {
			count++
		}
		return nil
	})
	return count
}

// Pass looks at the Import folder once and files every photo that has
// stopped changing, then catalogues what it filed.
func (w *IntakeWriter) Pass(ctx context.Context) (IntakeStatus, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	status := IntakeStatus{Folder: w.inbox.path}
	var problems []string
	for _, src := range w.sources() {
		if err := w.pass(ctx, src, &status); err != nil {
			if ctx.Err() != nil {
				problems = []string{ctx.Err().Error()}
				break
			}
			problems = append(problems, err.Error())
		}
	}
	if _, _, duplicates, err := w.look(ctx, w.inbox); err == nil {
		status.Duplicates = duplicates
	}
	if status.Filed > 0 && ctx.Err() == nil {
		if _, err := w.s.ScanArchive(ctx, w.archivePath); err != nil {
			problems = append(problems, "cataloguing the library: "+err.Error())
		}
	}
	var err error
	if len(problems) > 0 {
		err = errors.New(strings.Join(problems, "; "))
	}
	status.LastRun = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		status.Problem = err.Error()
	}
	w.filedTotal += status.Filed
	status.FiledTotal = w.filedTotal
	if saveErr := w.save(ctx, status); saveErr != nil && err == nil {
		err = saveErr
	}
	return status, err
}

func (w *IntakeWriter) pass(ctx context.Context, src *intakeSource, status *IntakeStatus) error {
	first, unsupported, _, err := w.look(ctx, src)
	if err != nil {
		return err
	}
	status.Unsupported += unsupported
	if len(first) == 0 {
		return nil
	}
	// A file still being copied in changes between two looks.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(w.settle):
	}
	second, _, _, err := w.look(ctx, src)
	if err != nil {
		return err
	}
	ready := map[string]intakeFile{}
	for rel, file := range second {
		if before, ok := first[rel]; ok && before.size == file.size && before.mod.Equal(file.mod) && file.size > 0 {
			ready[rel] = file
		} else {
			status.Waiting++
		}
	}
	groups := groupIntake(ready)
	// A group any of whose files is still arriving waits whole, so a Live
	// Photo is never split across two days, nor from a half named apart.
	for key, group := range groups {
		for rel := range second {
			if _, ok := ready[rel]; !ok && path.Dir(rel) == group.dir && looseStem(path.Base(rel)) == group.loose {
				status.Waiting += len(group.files)
				delete(groups, key)
				break
			}
		}
	}
	read, err := w.captures(ctx, src, groups)
	if err != nil {
		return err
	}
	pairLive(src, groups, read)
	taken := dates(src, groups, read)
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	emptied := map[string]bool{}
	var problems []string
	for _, key := range keys {
		group := groups[key]
		filed, err := w.file(ctx, group, taken[key])
		status.Filed += filed
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			problems = append(problems, fmt.Sprintf("%s: %v", group.files[0].rel, err))
			continue
		}
		emptied[group.dir] = true
	}
	if !src.placeholders {
		w.tidy(src, emptied)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		more := ""
		if len(problems) > 3 {
			more = fmt.Sprintf(", and %d more", len(problems)-3)
			problems = problems[:3]
		}
		return errors.New(strings.Join(problems, "; ") + more)
	}
	return nil
}

func groupIntake(files map[string]intakeFile) map[string]*intakeGroup {
	groups := map[string]*intakeGroup{}
	for rel, file := range files {
		dir := path.Dir(rel)
		stem := intakeStem(path.Base(rel))
		key := dir + "\x00" + stem
		group := groups[key]
		if group == nil {
			group = &intakeGroup{dir: dir, stem: stem, loose: looseStem(path.Base(rel))}
			groups[key] = group
		}
		group.files = append(group.files, file)
	}
	for key, group := range groups {
		media := false
		for _, file := range group.files {
			media = media || archiveMediaExtensions[file.ext]
		}
		// A sidecar whose photo is not here is left for the person.
		if !media {
			delete(groups, key)
			continue
		}
		// The still first: it is the one dated, and it names the day.
		sort.Slice(group.files, func(i, j int) bool {
			a, b := group.files[i], group.files[j]
			if a.still != b.still {
				return a.still
			}
			if intakeSidecars[a.ext] != intakeSidecars[b.ext] {
				return !intakeSidecars[a.ext]
			}
			return a.rel < b.rel
		})
	}
	return groups
}

// captures reads what every photo and video in the groups says about itself,
// by absolute path.
func (w *IntakeWriter) captures(ctx context.Context, src *intakeSource, groups map[string]*intakeGroup) (map[string]Capture, error) {
	var asked []string
	for _, group := range groups {
		for _, file := range group.files {
			if archiveMediaExtensions[file.ext] {
				asked = append(asked, src.abs(file.rel))
			}
		}
	}
	return w.readCaptures(ctx, asked)
}

func (w *IntakeWriter) readCaptures(ctx context.Context, files []string) (map[string]Capture, error) {
	sort.Strings(files)
	read := map[string]Capture{}
	for start := 0; start < len(files); start += intakeDateBatch {
		end := min(start+intakeDateBatch, len(files))
		found, err := w.capture(ctx, files[start:end])
		if err != nil {
			return nil, err
		}
		for k, v := range found {
			read[k] = v
		}
	}
	return read, nil
}

func (src *intakeSource) abs(rel string) string {
	return filepath.Join(src.path, filepath.FromSlash(rel))
}

// pairLive keeps each Live Photo's video with its still. icloudpd names the
// two apart when it tells photos of one name apart by size, as
// FullSizeRender-1198557.HEIC and FullSizeRender_HEVC-5510754.MOV, and a
// phone's export may name the video IMG_0001.MOV, as it would a film of its
// own; Apple's identifier, which both halves carry, says which are one photo.
// A video named apart joins its still's group, and every video known to be a
// still's is marked with it, to be filed under the still's name with _HEVC,
// the name Cull pairs.
func pairLive(src *intakeSource, groups map[string]*intakeGroup, read map[string]Capture) {
	keys := make([]string, 0, len(groups))
	for key, group := range groups {
		keys = append(keys, key)
		for i := range group.files {
			group.files[i].live = read[src.abs(group.files[i].rel)].LiveID
		}
	}
	sort.Strings(keys)
	stills := map[string]string{}
	for _, key := range keys {
		group := groups[key]
		for _, file := range group.files {
			if id := group.dir + "\x00" + file.live; file.still && file.live != "" && stills[id] == "" {
				stills[id] = key
			}
		}
	}
	for _, key := range keys {
		group := groups[key]
		if group.files[0].still {
			continue
		}
		var kept []intakeFile
		media := false
		for _, file := range group.files {
			if target := stills[group.dir+"\x00"+file.live]; file.live != "" && target != "" && isLiveClip(file) {
				groups[target].files = append(groups[target].files, file)
				continue
			}
			kept = append(kept, file)
			media = media || archiveMediaExtensions[file.ext]
		}
		if !media {
			delete(groups, key)
		} else {
			group.files = kept
		}
	}
	for _, group := range groups {
		for i, clip := range group.files {
			if !isLiveClip(clip) {
				continue
			}
			named := strings.HasSuffix(strings.ToLower(strings.TrimSuffix(path.Base(clip.rel), path.Ext(clip.rel))), "_hevc")
			for _, still := range group.files {
				if !still.still {
					continue
				}
				if clip.live != "" && clip.live == still.live || clip.live == "" && named {
					group.files[i].of = still.rel
					break
				}
			}
		}
	}
}

// dates finds when each group was taken: from the files themselves where
// they say, then from the name, then from the file's modification time.
func dates(src *intakeSource, groups map[string]*intakeGroup, read map[string]Capture) map[string]time.Time {
	taken := map[string]time.Time{}
	for key, group := range groups {
		var when time.Time
		for _, file := range group.files {
			if t := read[src.abs(file.rel)].Taken; plausibleTaken(t) {
				when = t
				break
			}
		}
		if when.IsZero() {
			for _, file := range group.files {
				if t, ok := dateFromName(path.Base(file.rel)); ok {
					when = t
					break
				}
			}
		}
		if when.IsZero() {
			when = group.files[0].mod.Local()
		}
		taken[key] = when
	}
	return taken
}

func plausibleTaken(t time.Time) bool {
	return t.Year() >= 1971 && t.Before(time.Now().Add(48*time.Hour))
}

// Names such as IMG_20190814_120000, PXL_20190814_120000123,
// IMG-20190814-WA0001, 2019-08-14 12.00.00 and Screenshot 2019-08-14.
var nameDate = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})[-_.]?(0[1-9]|1[0-2])[-_.]?(0[1-9]|[12]\d|3[01])(?:[^0-9]|$)`)

func dateFromName(name string) (time.Time, bool) {
	match := nameDate.FindStringSubmatch(name)
	if match == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", match[1]+"-"+match[2]+"-"+match[3], time.Local)
	if err != nil || !plausibleTaken(t) {
		return time.Time{}, false
	}
	return t, true
}

// liveIDPattern is the form of Apple's Live Photo identifier, a UUID.
var liveIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// exiftoolCapture reads the time a photo or video was taken, as the clock
// where it was taken showed it, and Apple's Live Photo identifier, which the
// still and the video both carry. Videos record UTC, which exiftool turns
// into this computer's time; Apple's own videos also carry the local time,
// which is preferred.
func exiftoolCapture(tool string) CaptureReader {
	if tool == "" {
		return func(context.Context, []string) (map[string]Capture, error) { return map[string]Capture{}, nil }
	}
	return func(ctx context.Context, files []string) (map[string]Capture, error) {
		args := []string{"-json", "-q", "-q", "-api", "QuickTimeUTC=1", "-d", "%Y-%m-%d %H:%M:%S",
			"-DateTimeOriginal", "-ContentCreateDate", "-CreationDate", "-CreateDate", "-MediaCreateDate", "-ContentIdentifier", "--"}
		cmd := exec.CommandContext(ctx, tool, append(args, files...)...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		// exiftool exits 1 when some file has no metadata, and still answers.
		if err := cmd.Run(); err != nil && out.Len() == 0 {
			if errors.Is(err, exec.ErrNotFound) {
				return nil, fmt.Errorf("%s is not installed, so files cannot be dated by their contents", tool)
			}
			return map[string]Capture{}, nil
		}
		var rows []map[string]any
		if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
			return nil, fmt.Errorf("%s answered with something other than JSON", tool)
		}
		found := map[string]Capture{}
		for _, row := range rows {
			source, _ := row["SourceFile"].(string)
			var capture Capture
			for _, key := range []string{"DateTimeOriginal", "ContentCreateDate", "CreationDate", "CreateDate", "MediaCreateDate"} {
				value, _ := row[key].(string)
				if len(value) < 19 {
					continue
				}
				if t, err := time.ParseInLocation("2006-01-02 15:04:05", value[:19], time.Local); err == nil && plausibleTaken(t) {
					capture.Taken = t
					break
				}
			}
			if id, _ := row["ContentIdentifier"].(string); liveIDPattern.MatchString(id) {
				capture.LiveID = strings.ToUpper(id)
			}
			if !capture.Taken.IsZero() || capture.LiveID != "" {
				found[source] = capture
			}
		}
		return found, nil
	}
}

// file puts one photo's files in the library under the day it was taken,
// and returns how many it put there.
func (w *IntakeWriter) file(ctx context.Context, group *intakeGroup, taken time.Time) (int, error) {
	day := taken.Format("2006/2006-01/2006-01-02")
	type member struct {
		file intakeFile
		hash string
		same string
		// name is what the file is filed as, before any suffix; fixed when it
		// is a Live Photo video named after a still already in the library,
		// so it takes no suffix of its own.
		name  string
		fixed bool
	}
	members := make([]member, 0, len(group.files))
	for _, file := range group.files {
		hash, size, err := fingerprintIn(ctx, file.src.root, file.rel, false)
		if err != nil {
			return 0, err
		}
		if size != file.size {
			return 0, fmt.Errorf("changed while it was being read")
		}
		same, err := sameBytesInRoot(ctx, w.archive, day, hash, size)
		if err != nil {
			return 0, err
		}
		members = append(members, member{file: file, hash: hash, same: same, name: path.Base(file.rel)})
	}
	// A Live Photo's video takes its still's name with _HEVC, the name Cull
	// pairs: the still's here, the library's copy's when the still is one the
	// library holds, or, when the video arrives after its still was filed,
	// the name of the still in the library that has its identifier.
	var owned map[string]bool
	for i, m := range members {
		if !isLiveClip(m.file) || m.same != "" {
			continue
		}
		still, fixed := "", true
		switch {
		case m.file.of != "":
			for _, s := range members {
				if s.file.rel == m.file.of {
					still, fixed = s.name, s.same != ""
					if fixed {
						still = path.Base(s.same)
					}
				}
			}
		case m.file.live == "" || group.files[0].still:
			continue
		}
		if fixed && owned == nil {
			var err error
			if owned, err = w.liveOwners(day); err != nil {
				return 0, err
			}
		}
		if still == "" {
			found, err := w.livePhotoIn(ctx, day, m.file, owned)
			if err != nil {
				return 0, err
			}
			still = found
		}
		stem := strings.TrimSuffix(still, path.Ext(still))
		if still == "" || fixed && owned[strings.ToLower(stem)] {
			continue
		}
		name := stem + "_HEVC" + path.Ext(m.name)
		if fixed {
			if _, err := w.archive.Lstat(path.Join(day, name)); !errors.Is(err, fs.ErrNotExist) {
				continue
			}
		}
		members[i].name, members[i].fixed = name, fixed
	}
	// One suffix for the whole photo, so a Live Photo's still and video, or a
	// RAW and its JPEG, keep one name between them.
	suffix := ""
	for index := 1; ; index++ {
		if index > 1 {
			suffix = fmt.Sprintf(" (%d)", index)
		}
		if index > 9999 {
			return 0, fmt.Errorf("no free name in %s", day)
		}
		free := true
		for _, m := range members {
			if m.same != "" || m.fixed {
				continue
			}
			if _, err := w.archive.Lstat(path.Join(day, withSuffix(m.name, suffix))); err == nil {
				free = false
				break
			} else if !errors.Is(err, fs.ErrNotExist) {
				return 0, err
			}
		}
		if free {
			break
		}
	}
	filed := 0
	for _, m := range members {
		if m.same != "" {
			if err := w.setAside(m.file); err != nil {
				return filed, err
			}
			continue
		}
		if err := mkdirShared(w.archive, day); err != nil {
			return filed, err
		}
		target := path.Join(day, withSuffix(m.name, suffix))
		if m.fixed {
			target = path.Join(day, m.name)
		}
		if err := w.move(ctx, m.file, target, m.hash); err != nil {
			return filed, err
		}
		filed++
	}
	return filed, nil
}

// liveOwners is the photos on day that already have a Live Photo video,
// beside them or in .live-photos, by their name before the extension in
// lower case.
func (w *IntakeWriter) liveOwners(day string) (map[string]bool, error) {
	owned := map[string]bool{}
	for _, dir := range []string{day, path.Join(liveFolder, day)} {
		// .live-photos is hidden, so it is listed as the Bin lists it, only
		// ever read.
		entries, err := fs.ReadDir(w.archive.FS(), dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if _, stem, ok := livePhotoOf(path.Join(dir, e.Name())); ok && e.Type().IsRegular() {
				owned[stem] = true
			}
		}
	}
	return owned, nil
}

// livePhotoIn finds the still on day that a Live Photo's video, arriving
// after it, is the video of: one of a like name with the video's identifier
// and no video yet. It answers the still's file name, or "".
func (w *IntakeWriter) livePhotoIn(ctx context.Context, day string, clip intakeFile, owned map[string]bool) (string, error) {
	entries, err := readRootDir(w.archive, day)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	loose := looseStem(path.Base(clip.rel))
	var asked []string
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		if strings.HasPrefix(name, ".") || !e.Type().IsRegular() || !stillExtensions[ext] || looseStem(name) != loose ||
			owned[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))] {
			continue
		}
		asked = append(asked, filepath.Join(w.archivePath, filepath.FromSlash(day), name))
	}
	read, err := w.readCaptures(ctx, asked)
	if err != nil {
		return "", err
	}
	for _, still := range asked {
		if read[still].LiveID == clip.live {
			return filepath.Base(still), nil
		}
	}
	return "", nil
}

// withSuffix puts suffix before the extension, or before the photo's own
// extension in a sidecar named like IMG_0001.HEIC.xmp, or before the _HEVC
// of a Live Photo's video, so IMG_0001 (2)_HEVC.MOV still pairs with
// IMG_0001 (2).HEIC.
func withSuffix(name, suffix string) string {
	if suffix == "" {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if liveClipExt[strings.ToLower(ext)] && len(stem) > len("_hevc") && strings.EqualFold(stem[len(stem)-len("_hevc"):], "_hevc") {
		cut := len(stem) - len("_hevc")
		return stem[:cut] + suffix + stem[cut:] + ext
	}
	if intakeSidecars[strings.ToLower(strings.TrimPrefix(ext, "."))] {
		if inner := path.Ext(stem); archiveMediaExtensions[strings.ToLower(strings.TrimPrefix(inner, "."))] {
			return strings.TrimSuffix(stem, inner) + suffix + inner + ext
		}
	}
	return stem + suffix + ext
}

func sameBytesInRoot(ctx context.Context, root *os.Root, dir, hash string, size int64) (string, error) {
	entries, err := readRootDir(root, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() != size {
			continue
		}
		rel := path.Join(dir, e.Name())
		other, _, err := fingerprintIn(ctx, root, rel, false)
		if err != nil {
			return "", err
		}
		if other == hash {
			return rel, nil
		}
	}
	return "", nil
}

// move puts one file at target in the library and takes it out of Import.
// On one disk it is linked across, which is instant and never overwrites;
// otherwise it is copied, checked byte for byte, dated as it was, and only
// then taken out of Import.
func (w *IntakeWriter) move(ctx context.Context, file intakeFile, target, hash string) error {
	source := filepath.Join(file.src.path, filepath.FromSlash(file.rel))
	destination := filepath.Join(w.archivePath, filepath.FromSlash(target))
	if _, err := guardedRegular(file.src.root, file.rel, false); err != nil {
		return err
	}
	if _, err := guardedDir(w.archive, path.Dir(target)); err != nil {
		return err
	}
	err := w.link(source, destination)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s appeared in the library meanwhile", target)
	}
	if err != nil {
		var link *os.LinkError
		if !errors.As(err, &link) || !(errors.Is(link.Err, syscall.EXDEV) || errors.Is(link.Err, syscall.EPERM) || errors.Is(link.Err, syscall.ENOTSUP)) {
			return err
		}
		if err = w.copyAcross(ctx, file, target, hash); err != nil {
			return err
		}
	}
	if err := syncRootDir(w.archive, target); err != nil {
		return err
	}
	got, size, err := fingerprintIn(ctx, w.archive, target, false)
	if err != nil {
		return err
	}
	if got != hash || size != file.size {
		return fmt.Errorf("%s did not arrive intact; the file is still in Import", target)
	}
	return takeOut(file)
}

func (w *IntakeWriter) copyAcross(ctx context.Context, file intakeFile, target, hash string) error {
	temp := path.Join(path.Dir(target), ".daddy-cull-import-"+shortHash(hash)+".tmp")
	_ = w.archive.Remove(temp)
	in, err := file.src.root.Open(file.rel)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := w.archive.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, copyErr := io.CopyBuffer(io.MultiWriter(out, digest), readerWithContext{ctx, in}, make([]byte, 1<<20))
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	defer w.archive.Remove(temp)
	if copyErr != nil {
		return copyErr
	}
	if hex.EncodeToString(digest.Sum(nil)) != hash {
		return fmt.Errorf("changed while it was being copied")
	}
	if err := w.archive.Chtimes(temp, file.mod, file.mod); err != nil {
		return err
	}
	if err := w.archive.Link(temp, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s appeared in the library meanwhile", target)
		}
		return err
	}
	return nil
}

func shortHash(hash string) string {
	if len(hash) > 16 {
		return hash[:16]
	}
	return hash
}

type readerWithContext struct {
	ctx context.Context
	r   io.Reader
}

func (r readerWithContext) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// takeOut removes a filed file from where it came, or, in a folder a
// downloader fills, leaves an empty placeholder of its name.
func takeOut(file intakeFile) error {
	root := file.src.root
	if !file.src.placeholders {
		if err := root.Remove(file.rel); err != nil {
			return err
		}
		return syncRootDir(root, file.rel)
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	temp := path.Join(path.Dir(file.rel), ".daddy-cull-placeholder-"+id+".tmp")
	placeholder, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := placeholder.Close(); err != nil {
		root.Remove(temp)
		return err
	}
	if err := root.Rename(temp, file.rel); err != nil {
		root.Remove(temp)
		return err
	}
	return syncRootDir(root, file.rel)
}

// setAside moves a file the library already holds into "Already in the
// library", keeping the folders it was in, for the person to look at. In a
// folder a downloader fills it is replaced by its placeholder instead: the
// library holds the same bytes, and the downloader would fetch it again.
func (w *IntakeWriter) setAside(file intakeFile) error {
	if file.src.placeholders {
		return takeOut(file)
	}
	root, rel := file.src.root, file.rel
	target := path.Join(IntakeDuplicates, rel)
	if err := root.MkdirAll(path.Dir(target), 0o755); err != nil {
		return err
	}
	free, err := freeArchiveName(root, target)
	if err != nil {
		return err
	}
	if err := root.Rename(rel, free); err != nil {
		return err
	}
	return syncRootDir(root, free)
}

// tidy removes the folders in Import that filing left empty. A folder that
// still holds anything is left, and so is Import itself.
func (w *IntakeWriter) tidy(src *intakeSource, dirs map[string]bool) {
	list := make([]string, 0, len(dirs))
	for dir := range dirs {
		for d := dir; d != "." && d != "/" && d != IntakeDuplicates; d = path.Dir(d) {
			list = append(list, d)
		}
	}
	// Deepest first, so a parent is only tried once its children are gone.
	sort.Slice(list, func(i, j int) bool { return strings.Count(list[i], "/") > strings.Count(list[j], "/") })
	for _, dir := range list {
		entries, err := readRootDir(src.root, dir)
		if err != nil || len(entries) > 0 {
			continue
		}
		_ = src.root.Remove(dir)
	}
}

func (w *IntakeWriter) save(ctx context.Context, status IntakeStatus) error {
	body, err := json.Marshal(status)
	if err != nil {
		return err
	}
	_, err = w.s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", intakeStatus, string(body))
	return err
}

// ForgetIntake clears what an earlier writer recorded about an Import
// folder, when the writer now runs without one.
func (s *Store) ForgetIntake(ctx context.Context) error {
	_, err := s.write.ExecContext(ctx, "DELETE FROM settings WHERE key=?", intakeStatus)
	return err
}

// Intake is what the writer last found in the Import folder, or nil when the
// writer has none.
func (s *Store) Intake(ctx context.Context) (*IntakeStatus, error) {
	var body string
	err := s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", intakeStatus).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var status IntakeStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// Handler serves the writer's one intake route: POST /intake/run looks at the
// Import folder now and answers with what it found.
func (w *IntakeWriter) Handler(secret string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(request.Header.Get("X-Bin-Key"))) != 1 {
			http.Error(writer, "forbidden", 403)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/intake/run" {
			http.NotFound(writer, request)
			return
		}
		// A look that has begun finishes even if the page asking goes away,
		// so no file is left half moved.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		status, _ := w.Pass(ctx)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(writer).Encode(status)
	})
}
