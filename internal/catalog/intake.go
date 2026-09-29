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

// CaptureDater reads when each file was taken, by absolute path. A file it
// has no date for is simply left out of the answer.
type CaptureDater func(ctx context.Context, files []string) (map[string]time.Time, error)

// IntakeWriter files what arrives in the Import folder. It runs in the
// private writer, the only process that may change the library.
type IntakeWriter struct {
	s           *Store
	inbox       *os.Root
	archive     *os.Root
	inboxPath   string
	archivePath string
	date        CaptureDater
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
	w := &IntakeWriter{s: s, inbox: inbox, archive: archive, inboxPath: inboxPath, archivePath: archivePath,
		date: exiftoolDater(dateTool), link: os.Link, settle: intakeSettle, wake: make(chan struct{}, 1)}
	return w, nil
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Close releases both folders.
func (w *IntakeWriter) Close() error {
	first := w.inbox.Close()
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
// ctx ends.
func (w *IntakeWriter) Keep(ctx context.Context) {
	for {
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
	rel   string
	size  int64
	mod   time.Time
	ext   string
	still bool
}

type intakeGroup struct {
	dir   string
	stem  string
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

var stillExtensions = map[string]bool{"heic": true, "heif": true, "jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "avif": true, "tif": true, "tiff": true,
	"arw": true, "dng": true, "cr2": true, "nef": true, "raf": true, "orf": true}

// look lists the Import folder: the media and sidecars in it, and how many
// other files it holds.
func (w *IntakeWriter) look(ctx context.Context) (map[string]intakeFile, int, int, error) {
	files := map[string]intakeFile{}
	unsupported, duplicates := 0, 0
	err := fs.WalkDir(w.inbox.FS(), ".", func(rel string, entry fs.DirEntry, err error) error {
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
			if rel == IntakeDuplicates {
				duplicates += countFiles(w.inbox, rel)
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(entry.Name()), "."))
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
		files[rel] = intakeFile{rel: rel, size: info.Size(), mod: info.ModTime(), ext: ext, still: stillExtensions[ext]}
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
	status := IntakeStatus{Folder: w.inboxPath}
	status, err := w.pass(ctx, status)
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

func (w *IntakeWriter) pass(ctx context.Context, status IntakeStatus) (IntakeStatus, error) {
	first, unsupported, _, err := w.look(ctx)
	if err != nil {
		return status, err
	}
	status.Unsupported = unsupported
	if len(first) == 0 {
		_, _, status.Duplicates, _ = w.look(ctx)
		return status, nil
	}
	// A file still being copied in changes between two looks.
	select {
	case <-ctx.Done():
		return status, ctx.Err()
	case <-time.After(w.settle):
	}
	second, _, _, err := w.look(ctx)
	if err != nil {
		return status, err
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
	// Photo is never split across two days.
	for key, group := range groups {
		for rel := range second {
			if _, ok := ready[rel]; !ok && path.Dir(rel) == group.dir && intakeStem(path.Base(rel)) == group.stem {
				status.Waiting += len(group.files)
				delete(groups, key)
				break
			}
		}
	}
	taken, err := w.dates(ctx, groups)
	if err != nil {
		return status, err
	}
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
				return status, ctx.Err()
			}
			problems = append(problems, fmt.Sprintf("%s: %v", group.files[0].rel, err))
			continue
		}
		emptied[group.dir] = true
	}
	w.tidy(emptied)
	_, _, status.Duplicates, _ = w.look(ctx)
	if status.Filed > 0 {
		if _, err := w.s.ScanArchive(ctx, w.archivePath); err != nil {
			problems = append(problems, "cataloguing the library: "+err.Error())
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		more := ""
		if len(problems) > 3 {
			more = fmt.Sprintf(", and %d more", len(problems)-3)
			problems = problems[:3]
		}
		return status, errors.New(strings.Join(problems, "; ") + more)
	}
	return status, nil
}

func groupIntake(files map[string]intakeFile) map[string]*intakeGroup {
	groups := map[string]*intakeGroup{}
	for rel, file := range files {
		dir := path.Dir(rel)
		stem := intakeStem(path.Base(rel))
		key := dir + "\x00" + stem
		group := groups[key]
		if group == nil {
			group = &intakeGroup{dir: dir, stem: stem}
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

// dates finds when each group was taken: from the files themselves where
// they say, then from the name, then from the file's modification time.
func (w *IntakeWriter) dates(ctx context.Context, groups map[string]*intakeGroup) (map[string]time.Time, error) {
	var asked []string
	for _, group := range groups {
		for _, file := range group.files {
			if archiveMediaExtensions[file.ext] {
				asked = append(asked, filepath.Join(w.inboxPath, filepath.FromSlash(file.rel)))
			}
		}
	}
	sort.Strings(asked)
	read := map[string]time.Time{}
	for start := 0; start < len(asked); start += intakeDateBatch {
		end := min(start+intakeDateBatch, len(asked))
		found, err := w.date(ctx, asked[start:end])
		if err != nil {
			return nil, err
		}
		for k, v := range found {
			read[k] = v
		}
	}
	taken := map[string]time.Time{}
	for key, group := range groups {
		var when time.Time
		for _, file := range group.files {
			if t, ok := read[filepath.Join(w.inboxPath, filepath.FromSlash(file.rel))]; ok && plausibleTaken(t) {
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
	return taken, nil
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

// exiftoolDater reads the time a photo or video was taken, as the clock
// where it was taken showed it. Videos record UTC, which exiftool turns into
// this computer's time; Apple's own videos also carry the local time, which
// is preferred.
func exiftoolDater(tool string) CaptureDater {
	if tool == "" {
		return func(context.Context, []string) (map[string]time.Time, error) { return map[string]time.Time{}, nil }
	}
	return func(ctx context.Context, files []string) (map[string]time.Time, error) {
		args := []string{"-json", "-q", "-q", "-api", "QuickTimeUTC=1", "-d", "%Y-%m-%d %H:%M:%S",
			"-DateTimeOriginal", "-ContentCreateDate", "-CreationDate", "-CreateDate", "-MediaCreateDate", "--"}
		cmd := exec.CommandContext(ctx, tool, append(args, files...)...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		// exiftool exits 1 when some file has no metadata, and still answers.
		if err := cmd.Run(); err != nil && out.Len() == 0 {
			if errors.Is(err, exec.ErrNotFound) {
				return nil, fmt.Errorf("%s is not installed, so files cannot be dated by their contents", tool)
			}
			return map[string]time.Time{}, nil
		}
		var rows []map[string]any
		if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
			return nil, fmt.Errorf("%s answered with something other than JSON", tool)
		}
		found := map[string]time.Time{}
		for _, row := range rows {
			source, _ := row["SourceFile"].(string)
			for _, key := range []string{"DateTimeOriginal", "ContentCreateDate", "CreationDate", "CreateDate", "MediaCreateDate"} {
				value, _ := row[key].(string)
				if len(value) < 19 {
					continue
				}
				if t, err := time.ParseInLocation("2006-01-02 15:04:05", value[:19], time.Local); err == nil && plausibleTaken(t) {
					found[source] = t
					break
				}
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
	}
	members := make([]member, 0, len(group.files))
	for _, file := range group.files {
		hash, size, err := fingerprintIn(ctx, w.inbox, file.rel, false)
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
		members = append(members, member{file: file, hash: hash, same: same})
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
			if m.same != "" {
				continue
			}
			if _, err := w.archive.Lstat(path.Join(day, withSuffix(path.Base(m.file.rel), suffix))); err == nil {
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
			if err := w.setAside(m.file.rel); err != nil {
				return filed, err
			}
			continue
		}
		if err := mkdirShared(w.archive, day); err != nil {
			return filed, err
		}
		target := path.Join(day, withSuffix(path.Base(m.file.rel), suffix))
		if err := w.move(ctx, m.file, target, m.hash); err != nil {
			return filed, err
		}
		filed++
	}
	return filed, nil
}

// withSuffix puts suffix before the extension, or before the photo's own
// extension in a sidecar named like IMG_0001.HEIC.xmp.
func withSuffix(name, suffix string) string {
	if suffix == "" {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
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
	source := filepath.Join(w.inboxPath, filepath.FromSlash(file.rel))
	destination := filepath.Join(w.archivePath, filepath.FromSlash(target))
	if _, err := guardedRegular(w.inbox, file.rel, false); err != nil {
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
	if err := w.inbox.Remove(file.rel); err != nil {
		return err
	}
	return syncRootDir(w.inbox, file.rel)
}

func (w *IntakeWriter) copyAcross(ctx context.Context, file intakeFile, target, hash string) error {
	temp := path.Join(path.Dir(target), ".daddy-cull-import-"+shortHash(hash)+".tmp")
	_ = w.archive.Remove(temp)
	in, err := w.inbox.Open(file.rel)
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

// setAside moves a file the library already holds into "Already in the
// library", keeping the folders it was in, for the person to look at.
func (w *IntakeWriter) setAside(rel string) error {
	target := path.Join(IntakeDuplicates, rel)
	if err := w.inbox.MkdirAll(path.Dir(target), 0o755); err != nil {
		return err
	}
	free, err := freeArchiveName(w.inbox, target)
	if err != nil {
		return err
	}
	if err := w.inbox.Rename(rel, free); err != nil {
		return err
	}
	return syncRootDir(w.inbox, free)
}

// tidy removes the folders in Import that filing left empty. A folder that
// still holds anything is left, and so is Import itself.
func (w *IntakeWriter) tidy(dirs map[string]bool) {
	list := make([]string, 0, len(dirs))
	for dir := range dirs {
		for d := dir; d != "." && d != "/" && d != IntakeDuplicates; d = path.Dir(d) {
			list = append(list, d)
		}
	}
	// Deepest first, so a parent is only tried once its children are gone.
	sort.Slice(list, func(i, j int) bool { return strings.Count(list[i], "/") > strings.Count(list[j], "/") })
	for _, dir := range list {
		entries, err := readRootDir(w.inbox, dir)
		if err != nil || len(entries) > 0 {
			continue
		}
		_ = w.inbox.Remove(dir)
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
