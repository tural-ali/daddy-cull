package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var screenshotDatedName = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})_(.+)$`)

// ScreenshotPlanFile is one file a screenshot plan moves: the screenshot or
// one of its sidecars.
type ScreenshotPlanFile struct {
	// Source is the file's name in the screenshots holding area, where it is
	// before the plan runs and where undoing a removal puts it back.
	Source string `json:"source"`
	// Destination is where the file goes. To keep, it is a path relative to
	// the archive root, filed under the date in the name, such as
	// 2024/2024-03/2024-03-05/IMG_0001.PNG for 2024-03-05_IMG_0001.PNG. To
	// remove, it is where the file waits in the Bin, relative to the holding
	// area: .culled/next/<plan id>/<place in the plan>-<name>.
	Destination string `json:"destination"`
	// Size is the file's size in bytes when the plan was made.
	Size int64 `json:"size"`
	// Hash is the SHA-256 of the file's contents when the plan was made, in
	// hexadecimal. The file is checked against it before every step.
	Hash string `json:"hash"`
	// Sidecar is true for a sidecar of the screenshot. The screenshot itself
	// is always the first file.
	Sidecar bool `json:"sidecar"`
	// Phase is where the file stands: waiting, moving, done, restoring,
	// restored, deleting, purged, or absent_after_intent (gone after its
	// deletion started, without the deletion being seen to finish).
	Phase string `json:"phase"`
}

// ScreenshotPlan is a plan for keeping one screenshot in the archive or
// removing it into the Bin, and what has become of it since.
type ScreenshotPlan struct {
	// ID is the plan's id, 32 hexadecimal characters.
	ID string `json:"id"`
	// AssetID is the screenshot's id in the catalogue.
	AssetID int64 `json:"assetId"`
	// Action is keep, which files it in the archive under the date in its
	// name, or remove, which moves it into the Bin.
	Action string `json:"action"`
	// State is where the plan stands: planned (nothing moved yet), running,
	// kept, bin, restoring, restored (a removal undone, so it waits for
	// review again), purging, purged, or purged_recovered (deleted, with at
	// least one file found already gone after its deletion started).
	State string `json:"state"`
	// Created is when the plan was made, in RFC 3339 UTC.
	Created string `json:"created"`
	// Files are the screenshot first and then its sidecars.
	Files []ScreenshotPlanFile `json:"files"`
	// Error says why the last step failed. It is cleared when a step
	// finishes, and left out when there is none.
	Error string `json:"error,omitempty"`
}

type ScreenshotWriter struct {
	s          *Store
	shots      *os.Root
	archive    *os.Root
	mu         sync.Mutex
	checkpoint func(string) error
}

func openGuardedRoot(root string) (*os.Root, error) {
	if root == "" || root == "/" || !path.IsAbs(root) {
		return nil, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil || resolved == string(filepath.Separator) {
		return nil, ErrInvalid
	}
	return os.OpenRoot(resolved)
}

func NewScreenshotWriter(s *Store, screenshotsRoot, archiveRoot string) (*ScreenshotWriter, error) {
	shots, err := openGuardedRoot(screenshotsRoot)
	if err != nil {
		return nil, err
	}
	archive, err := openGuardedRoot(archiveRoot)
	if err != nil {
		shots.Close()
		return nil, err
	}
	if err = shots.MkdirAll(".culled/next", 0700); err != nil {
		shots.Close()
		archive.Close()
		return nil, err
	}
	return &ScreenshotWriter{s: s, shots: shots, archive: archive}, nil
}

func (w *ScreenshotWriter) Close() error {
	first := w.shots.Close()
	second := w.archive.Close()
	if first != nil {
		return first
	}
	return second
}

func screenshotRelative(value string) (string, bool) {
	if !strings.HasPrefix(value, "/screenshots/") {
		return "", false
	}
	rel := strings.TrimPrefix(value, "/screenshots/")
	if strings.Contains(rel, "/") || strings.HasPrefix(rel, ".") || path.Clean(rel) != rel {
		return "", false
	}
	return rel, rel != ""
}

func guardedRegular(root *os.Root, rel string, allowBin bool) (os.FileInfo, error) {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || strings.Contains(rel, "\\") {
		return nil, ErrInvalid
	}
	for index, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || (strings.HasPrefix(part, ".") && !(allowBin && index == 0 && part == ".culled") && !strings.HasPrefix(part, ".daddy-cull-")) {
			return nil, ErrInvalid
		}
		info, err := root.Lstat(strings.Join(strings.Split(rel, "/")[:index+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink refused: %s", rel)
		}
	}
	info, err := root.Stat(rel)
	if err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", rel)
	}
	return info, err
}

func fingerprintIn(ctx context.Context, root *os.Root, rel string, allowBin bool) (string, int64, error) {
	before, err := guardedRegular(root, rel, allowBin)
	if err != nil {
		return "", 0, err
	}
	file, err := root.Open(rel)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", 0, fmt.Errorf("file changed while opening: %s", rel)
	}
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", 0, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, readErr
		}
	}
	after, err := guardedRegular(root, rel, allowBin)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", 0, fmt.Errorf("file changed during verification: %s", rel)
	}
	return hex.EncodeToString(hash.Sum(nil)), before.Size(), nil
}

func screenshotDestination(name string) (string, bool) {
	match := screenshotDatedName.FindStringSubmatch(name)
	if len(match) != 5 {
		return "", false
	}
	day := match[1] + "-" + match[2] + "-" + match[3]
	if _, err := time.Parse("2006-01-02", day); err != nil || strings.Contains(match[4], "/") || strings.HasPrefix(match[4], ".") {
		return "", false
	}
	return path.Join(match[1], match[1]+"-"+match[2], day, match[4]), true
}

func (w *ScreenshotWriter) sidecars(name string) ([]string, error) {
	directory, err := w.shots.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	result := make([]string, 0)
	for _, entry := range entries {
		candidate := entry.Name()
		if candidate == name || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(candidate), "."))
		if !sidecarExt[ext] {
			continue
		}
		prefix := strings.TrimSuffix(candidate, path.Ext(candidate))
		if prefix == name || strings.EqualFold(prefix, stem) {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func freeArchiveName(root *os.Root, target string) (string, error) {
	if _, err := guardedRegular(root, target, false); errors.Is(err, os.ErrNotExist) {
		return target, nil
	} else if err != nil {
		return "", err
	}
	dir, extension := path.Dir(target), path.Ext(target)
	stem := strings.TrimSuffix(path.Base(target), extension)
	for index := 2; index < 10000; index++ {
		candidate := path.Join(dir, fmt.Sprintf("%s (%d)%s", stem, index, extension))
		if _, err := guardedRegular(root, candidate, false); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no collision-free archive name available")
}

func (w *ScreenshotWriter) save(plan *ScreenshotPlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = w.s.write.Exec("INSERT INTO screenshot_plans(id,asset_id,body) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", plan.ID, plan.AssetID, string(body))
	return err
}

func (w *ScreenshotWriter) load(id string) (*ScreenshotPlan, error) {
	if len(id) != 32 {
		return nil, ErrInvalid
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, ErrInvalid
	}
	var body string
	if err := w.s.read.QueryRow("SELECT body FROM screenshot_plans WHERE id=?", id).Scan(&body); err != nil {
		return nil, err
	}
	var plan ScreenshotPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil || plan.ID != id || len(plan.Files) == 0 {
		return nil, ErrInvalid
	}
	if plan.AssetID < 1 || (plan.Action != "keep" && plan.Action != "remove") {
		return nil, ErrInvalid
	}
	for index, file := range plan.Files {
		if _, ok := screenshotRelative("/screenshots/" + file.Source); !ok || file.Size < 0 || len(file.Hash) != 64 {
			return nil, ErrInvalid
		}
		if _, err := hex.DecodeString(file.Hash); err != nil {
			return nil, ErrInvalid
		}
		if plan.Action == "keep" {
			if !safeRelative(file.Destination) {
				return nil, ErrInvalid
			}
		} else {
			expected := path.Join(".culled/next", plan.ID, fmt.Sprintf("%04d-%s", index, file.Source))
			if file.Destination != expected {
				return nil, ErrInvalid
			}
		}
	}
	return &plan, nil
}

func (w *ScreenshotWriter) Preview(ctx context.Context, assetID int64, action string) (*ScreenshotPlan, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if action != "keep" && action != "remove" {
		return nil, ErrInvalid
	}
	var assetPath, state string
	var catalogueSize int64
	if err := w.s.read.QueryRowContext(ctx, "SELECT a.relative_path,a.size_bytes,shots.state FROM assets a JOIN screenshot_items shots ON shots.asset_id=a.id WHERE a.id=? AND a.source_id='screenshots'", assetID).Scan(&assetPath, &catalogueSize, &state); err != nil {
		return nil, err
	}
	if state != "waiting" {
		return nil, fmt.Errorf("screenshot is no longer waiting")
	}
	name, ok := screenshotRelative(assetPath)
	if !ok {
		return nil, ErrInvalid
	}
	destination := ""
	if action == "keep" {
		var valid bool
		destination, valid = screenshotDestination(name)
		if !valid {
			return nil, fmt.Errorf("no valid date in filename; place this file manually")
		}
		var err error
		destination, err = freeArchiveName(w.archive, destination)
		if err != nil {
			return nil, err
		}
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	plan := &ScreenshotPlan{ID: hex.EncodeToString(random), AssetID: assetID, Action: action, State: "planned", Created: time.Now().UTC().Format(time.RFC3339), Files: make([]ScreenshotPlanFile, 0)}
	names := []string{name}
	sidecars, err := w.sidecars(name)
	if err != nil {
		return nil, err
	}
	names = append(names, sidecars...)
	mainStem := strings.TrimSuffix(path.Base(destination), path.Ext(destination))
	oldStem := strings.TrimSuffix(name, path.Ext(name))
	for index, source := range names {
		hash, size, err := fingerprintIn(ctx, w.shots, source, false)
		if err != nil {
			return nil, err
		}
		if index == 0 && size != catalogueSize {
			return nil, fmt.Errorf("catalogue size differs from current screenshot")
		}
		target := path.Join(".culled/next", plan.ID, fmt.Sprintf("%04d-%s", index, source))
		if action == "keep" {
			if index == 0 {
				target = destination
			} else if strings.HasPrefix(source, name) {
				target = destination + strings.TrimPrefix(source, name)
			} else {
				target = path.Join(path.Dir(destination), mainStem+strings.TrimPrefix(source, oldStem))
			}
		}
		plan.Files = append(plan.Files, ScreenshotPlanFile{Source: source, Destination: target, Size: size, Hash: hash, Sidecar: index > 0, Phase: "waiting"})
	}
	if err = w.save(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func syncRootDir(root *os.Root, rel string) error {
	directory, err := root.Open(path.Dir(rel))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func verifyPlanFile(ctx context.Context, root *os.Root, rel string, file ScreenshotPlanFile, allowBin bool) error {
	hash, size, err := fingerprintIn(ctx, root, rel, allowBin)
	if err != nil {
		return err
	}
	if hash != file.Hash || size != file.Size {
		return fmt.Errorf("content changed: %s", rel)
	}
	return nil
}

func (w *ScreenshotWriter) moveToBin(ctx context.Context, file ScreenshotPlanFile) error {
	return w.moveWithinShots(ctx, file.Source, file.Destination, file, false, true)
}

func (w *ScreenshotWriter) moveWithinShots(ctx context.Context, sourcePath, destinationPath string, file ScreenshotPlanFile, sourceInBin, destinationInBin bool) error {
	source, sourceErr := guardedRegular(w.shots, sourcePath, sourceInBin)
	destination, destinationErr := guardedRegular(w.shots, destinationPath, destinationInBin)
	if errors.Is(sourceErr, os.ErrNotExist) && destinationErr == nil {
		return verifyPlanFile(ctx, w.shots, destinationPath, file, destinationInBin)
	}
	if sourceErr != nil {
		return sourceErr
	}
	if err := verifyPlanFile(ctx, w.shots, sourcePath, file, sourceInBin); err != nil {
		return err
	}
	if destinationErr == nil {
		if !os.SameFile(source, destination) {
			return fmt.Errorf("destination exists; source retained: %s", destinationPath)
		}
	} else if errors.Is(destinationErr, os.ErrNotExist) {
		if err := w.shots.MkdirAll(path.Dir(destinationPath), 0700); err != nil {
			return err
		}
		if err := w.shots.Link(sourcePath, destinationPath); err != nil {
			return err
		}
		if err := syncRootDir(w.shots, destinationPath); err != nil {
			return err
		}
	} else {
		return destinationErr
	}
	if w.checkpoint != nil {
		if err := w.checkpoint("after-link"); err != nil {
			return err
		}
	}
	source, sourceErr = guardedRegular(w.shots, sourcePath, sourceInBin)
	destination, destinationErr = guardedRegular(w.shots, destinationPath, destinationInBin)
	if sourceErr != nil || destinationErr != nil || !os.SameFile(source, destination) {
		return fmt.Errorf("source and destination changed during move")
	}
	if err := w.shots.Remove(sourcePath); err != nil {
		return err
	}
	return syncRootDir(w.shots, sourcePath)
}

func (w *ScreenshotWriter) copyToArchive(ctx context.Context, plan *ScreenshotPlan, index int) error {
	file := plan.Files[index]
	if _, err := guardedRegular(w.archive, file.Destination, false); err == nil {
		if verifyErr := verifyPlanFile(ctx, w.archive, file.Destination, file, false); verifyErr != nil {
			return fmt.Errorf("archive destination exists; screenshot retained")
		}
		if _, sourceErr := guardedRegular(w.shots, file.Source, false); errors.Is(sourceErr, os.ErrNotExist) {
			return nil
		} else if sourceErr != nil {
			return sourceErr
		}
		if verifyErr := verifyPlanFile(ctx, w.shots, file.Source, file, false); verifyErr != nil {
			return verifyErr
		}
		if removeErr := w.shots.Remove(file.Source); removeErr != nil {
			return removeErr
		}
		return syncRootDir(w.shots, file.Source)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := verifyPlanFile(ctx, w.shots, file.Source, file, false); err != nil {
		return err
	}
	if err := w.archive.MkdirAll(path.Dir(file.Destination), 0755); err != nil {
		return err
	}
	temp := path.Join(path.Dir(file.Destination), ".daddy-cull-"+plan.ID+fmt.Sprintf("-%04d.tmp", index))
	_ = w.archive.Remove(temp)
	source, err := w.shots.Open(file.Source)
	if err != nil {
		return err
	}
	target, err := w.archive.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0660)
	if err != nil {
		source.Close()
		return err
	}
	_, copyErr := io.CopyBuffer(target, source, make([]byte, 1024*1024))
	if copyErr == nil {
		copyErr = target.Sync()
	}
	closeTargetErr := target.Close()
	closeSourceErr := source.Close()
	if copyErr != nil {
		w.archive.Remove(temp)
		return copyErr
	}
	if closeTargetErr != nil || closeSourceErr != nil {
		w.archive.Remove(temp)
		if closeTargetErr != nil {
			return closeTargetErr
		}
		return closeSourceErr
	}
	defer w.archive.Remove(temp)
	if err = verifyPlanFile(ctx, w.archive, temp, file, false); err != nil {
		return err
	}
	if err = w.archive.Link(temp, file.Destination); err != nil {
		return fmt.Errorf("archive destination became occupied; screenshot retained: %w", err)
	}
	if err = syncRootDir(w.archive, file.Destination); err != nil {
		return err
	}
	if w.checkpoint != nil {
		if err = w.checkpoint("after-copy"); err != nil {
			return err
		}
	}
	if err = verifyPlanFile(ctx, w.archive, file.Destination, file, false); err != nil {
		return err
	}
	if err = w.shots.Remove(file.Source); err != nil {
		return err
	}
	return syncRootDir(w.shots, file.Source)
}

func (w *ScreenshotWriter) Run(ctx context.Context, id string) (result *ScreenshotPlan, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	plan, err := w.load(id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			plan.Error = err.Error()
			_ = w.save(plan)
		}
		result = plan
	}()
	if plan.State == "kept" || plan.State == "bin" {
		return plan, nil
	}
	if plan.State != "planned" && plan.State != "running" {
		return plan, ErrInvalid
	}
	if plan.State == "planned" {
		var state string
		if err = w.s.read.QueryRowContext(ctx, "SELECT state FROM screenshot_items WHERE asset_id=?", plan.AssetID).Scan(&state); err != nil || state != "waiting" {
			return plan, fmt.Errorf("screenshot is no longer waiting")
		}
		for _, file := range plan.Files {
			if err = verifyPlanFile(ctx, w.shots, file.Source, file, false); err != nil {
				return plan, err
			}
			if _, existsErr := guardedRegular(map[bool]*os.Root{true: w.archive, false: w.shots}[plan.Action == "keep"], file.Destination, plan.Action == "remove"); existsErr == nil {
				return plan, fmt.Errorf("destination exists; screenshot retained")
			} else if !errors.Is(existsErr, os.ErrNotExist) {
				return plan, existsErr
			}
		}
		plan.State = "running"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	for index := range plan.Files {
		file := &plan.Files[index]
		if file.Phase == "done" {
			continue
		}
		file.Phase = "moving"
		if err = w.save(plan); err != nil {
			return plan, err
		}
		if plan.Action == "keep" {
			err = w.copyToArchive(ctx, plan, index)
		} else {
			err = w.moveToBin(ctx, *file)
		}
		if err != nil {
			return plan, err
		}
		file.Phase = "done"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	state := "bin"
	if plan.Action == "keep" {
		state = "kept"
	}
	tx, err := w.s.write.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_items SET state=? WHERE asset_id=? AND state='waiting'", state, plan.AssetID); err != nil {
		return plan, err
	}
	plan.State = state
	plan.Error = ""
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_plans SET body=? WHERE id=?", string(body), plan.ID); err != nil {
		return plan, err
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	return plan, nil
}

func (w *ScreenshotWriter) UndoRemove(ctx context.Context, id string) (result *ScreenshotPlan, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	plan, err := w.load(id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			plan.Error = err.Error()
			_ = w.save(plan)
		}
		result = plan
	}()
	if plan.Action != "remove" || (plan.State != "bin" && plan.State != "restoring") {
		return plan, ErrInvalid
	}
	if plan.State == "bin" {
		for _, file := range plan.Files {
			if _, existsErr := guardedRegular(w.shots, file.Source, false); existsErr == nil {
				return plan, fmt.Errorf("restore destination exists; Bin file retained: %s", file.Source)
			} else if !errors.Is(existsErr, os.ErrNotExist) {
				return plan, existsErr
			}
			if err = verifyPlanFile(ctx, w.shots, file.Destination, file, true); err != nil {
				return plan, err
			}
		}
		plan.State = "restoring"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	for index := len(plan.Files) - 1; index >= 0; index-- {
		file := &plan.Files[index]
		if file.Phase == "restored" {
			continue
		}
		file.Phase = "restoring"
		if err = w.save(plan); err != nil {
			return plan, err
		}
		if err = w.moveWithinShots(ctx, file.Destination, file.Source, *file, true, false); err != nil {
			return plan, err
		}
		file.Phase = "restored"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	tx, err := w.s.write.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_items SET state='waiting' WHERE asset_id=? AND state='bin'", plan.AssetID); err != nil {
		return plan, err
	}
	plan.State = "restored"
	plan.Error = ""
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_plans SET body=? WHERE id=?", string(body), plan.ID); err != nil {
		return plan, err
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	return plan, nil
}

func (w *ScreenshotWriter) PurgeRemove(ctx context.Context, id, confirmation string) (result *ScreenshotPlan, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	plan, err := w.load(id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			plan.Error = err.Error()
			_ = w.save(plan)
		}
		result = plan
	}()
	if plan.Action != "remove" || confirmation != fmt.Sprintf("DELETE %d", len(plan.Files)) {
		return plan, fmt.Errorf("exact deletion confirmation required")
	}
	if plan.State == "purged" || plan.State == "purged_recovered" {
		return plan, nil
	}
	if plan.State != "bin" && plan.State != "purging" {
		return plan, ErrInvalid
	}
	if plan.State == "bin" {
		for _, file := range plan.Files {
			if err = verifyPlanFile(ctx, w.shots, file.Destination, file, true); err != nil {
				return plan, err
			}
		}
		plan.State = "purging"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	recovered := false
	for index := range plan.Files {
		file := &plan.Files[index]
		if file.Phase == "purged" || file.Phase == "absent_after_intent" {
			continue
		}
		if file.Phase == "deleting" {
			if _, missingErr := guardedRegular(w.shots, file.Destination, true); errors.Is(missingErr, os.ErrNotExist) {
				file.Phase = "absent_after_intent"
				recovered = true
				continue
			}
		}
		if err = verifyPlanFile(ctx, w.shots, file.Destination, *file, true); err != nil {
			return plan, err
		}
		file.Phase = "deleting"
		if err = w.save(plan); err != nil {
			return plan, err
		}
		if err = w.shots.Remove(file.Destination); err != nil {
			return plan, err
		}
		if err = syncRootDir(w.shots, file.Destination); err != nil {
			return plan, err
		}
		if w.checkpoint != nil {
			if err = w.checkpoint("after-delete"); err != nil {
				return plan, err
			}
		}
		file.Phase = "purged"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	tx, err := w.s.write.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_items SET state='missing' WHERE asset_id=? AND state='bin'", plan.AssetID); err != nil {
		return plan, err
	}
	if recovered {
		plan.State = "purged_recovered"
	} else {
		plan.State = "purged"
	}
	plan.Error = ""
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, "UPDATE screenshot_plans SET body=? WHERE id=?", string(body), plan.ID); err != nil {
		return plan, err
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	return plan, nil
}

func (s *Store) ScreenshotBin(ctx context.Context, limit int) ([]ScreenshotPlan, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := s.read.QueryContext(ctx, "SELECT body FROM screenshot_plans ORDER BY rowid DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make([]ScreenshotPlan, 0)
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		var plan ScreenshotPlan
		if err = json.Unmarshal([]byte(body), &plan); err != nil {
			return nil, err
		}
		if plan.Action == "remove" && (plan.State == "bin" || plan.State == "restoring") {
			plans = append(plans, plan)
		}
	}
	return plans, rows.Err()
}
