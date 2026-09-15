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
	"strings"
	"sync"
	"time"
)

type LegacyPlanItem struct {
	ID       int64  `json:"id"`
	Original string `json:"original"`
	Stored   string `json:"stored"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
}

type LegacyPlanFile struct {
	LegacyPlanItem
	Hash  string `json:"hash"`
	Mtime int64  `json:"mtime"`
	Phase string `json:"phase"`
}

type LegacyBinPlan struct {
	ID      string           `json:"id"`
	State   string           `json:"state"`
	Created string           `json:"created"`
	Files   []LegacyPlanFile `json:"files"`
	Error   string           `json:"error,omitempty"`
}

type LegacyBinEngine struct {
	s          *Store
	root       *os.Root
	mu         sync.Mutex
	checkpoint func(string) error
}

func NewLegacyBinEngine(s *Store, root string) (*LegacyBinEngine, error) {
	if root == "" || root == "/" || !path.IsAbs(root) {
		return nil, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil || resolved == string(filepath.Separator) {
		return nil, ErrInvalid
	}
	r, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, err
	}
	return &LegacyBinEngine{s: s, root: r}, nil
}

func (b *LegacyBinEngine) Close() error { return b.root.Close() }

func legacyRelative(value string) (string, bool) {
	if !strings.HasPrefix(value, "/disks/") {
		return "", false
	}
	rel := strings.TrimPrefix(value, "/disks/")
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || strings.Contains(rel, "\\") || !strings.Contains(rel, "/") {
		return "", false
	}
	for index, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || (strings.HasPrefix(part, ".") && !(index == 1 && part == ".culled")) {
			return "", false
		}
	}
	return rel, true
}

func (b *LegacyBinEngine) regular(rel string) (os.FileInfo, error) {
	if _, ok := legacyRelative("/disks/" + rel); !ok {
		return nil, ErrInvalid
	}
	parts := strings.Split(rel, "/")
	for index := range parts {
		info, err := b.root.Lstat(strings.Join(parts[:index+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink refused: %s", rel)
		}
	}
	info, err := b.root.Stat(rel)
	if err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", rel)
	}
	return info, err
}

func (b *LegacyBinEngine) fingerprint(ctx context.Context, rel string) (string, int64, int64, error) {
	before, err := b.regular(rel)
	if err != nil {
		return "", 0, 0, err
	}
	file, err := b.root.Open(rel)
	if err != nil {
		return "", 0, 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", 0, 0, fmt.Errorf("file changed while opening: %s", rel)
	}
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, 0, readErr
		}
	}
	after, err := b.regular(rel)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", 0, 0, fmt.Errorf("file changed during verification: %s", rel)
	}
	return hex.EncodeToString(hash.Sum(nil)), before.Size(), before.ModTime().UnixNano(), nil
}

func (b *LegacyBinEngine) verify(ctx context.Context, rel string, expected LegacyPlanFile) error {
	hash, size, _, err := b.fingerprint(ctx, rel)
	if err != nil {
		return err
	}
	if hash != expected.Hash || size != expected.Size {
		return fmt.Errorf("content changed: %s", rel)
	}
	return nil
}

func (b *LegacyBinEngine) save(plan *LegacyBinPlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = b.s.write.Exec("INSERT INTO legacy_file_plans(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", plan.ID, string(body))
	return err
}

func (b *LegacyBinEngine) load(id string) (*LegacyBinPlan, error) {
	if len(id) != 32 {
		return nil, ErrInvalid
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, ErrInvalid
	}
	var body string
	if err := b.s.read.QueryRow("SELECT body FROM legacy_file_plans WHERE id=?", id).Scan(&body); err != nil {
		return nil, err
	}
	var plan LegacyBinPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil || plan.ID != id || len(plan.Files) == 0 {
		return nil, ErrInvalid
	}
	for _, file := range plan.Files {
		stored, storedOK := legacyRelative(file.Stored)
		original, originalOK := legacyRelative(file.Original)
		if !storedOK || !originalOK || strings.Split(stored, "/")[0] != strings.Split(original, "/")[0] || !strings.Contains(stored, "/.culled/") || file.Size < 0 || len(file.Hash) != 64 {
			return nil, ErrInvalid
		}
		if _, err := hex.DecodeString(file.Hash); err != nil {
			return nil, ErrInvalid
		}
	}
	return &plan, nil
}

func (b *LegacyBinEngine) Preview(ctx context.Context, ids []int64) (*LegacyBinPlan, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(ids) < 1 || len(ids) > 20 {
		return nil, fmt.Errorf("select between 1 and 20 Bin items")
	}
	batches := make(map[string]bool)
	for _, id := range ids {
		var batch string
		if err := b.s.read.QueryRowContext(ctx, "SELECT batch FROM legacy_culled WHERE legacy_id=? AND restored_at IS NULL AND purged_at IS NULL", id).Scan(&batch); err != nil {
			return nil, err
		}
		batches[batch] = true
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	plan := &LegacyBinPlan{ID: hex.EncodeToString(random), State: "planned", Created: time.Now().UTC().Format(time.RFC3339), Files: make([]LegacyPlanFile, 0)}
	for batch := range batches {
		rows, err := b.s.read.QueryContext(ctx, "SELECT legacy_id,original_path,culled_path,kind,size_bytes FROM legacy_culled WHERE batch=? AND restored_at IS NULL AND purged_at IS NULL ORDER BY legacy_id", batch)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item LegacyPlanFile
			var catalogSize int64
			if err = rows.Scan(&item.ID, &item.Original, &item.Stored, &item.Kind, &catalogSize); err != nil {
				rows.Close()
				return nil, err
			}
			stored, ok := legacyRelative(item.Stored)
			original, originalOK := legacyRelative(item.Original)
			if !ok || !originalOK || strings.Split(stored, "/")[0] != strings.Split(original, "/")[0] || !strings.Contains(stored, "/.culled/") {
				rows.Close()
				return nil, fmt.Errorf("legacy path is outside its guarded disk")
			}
			item.Hash, item.Size, item.Mtime, err = b.fingerprint(ctx, stored)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if item.Size != catalogSize {
				rows.Close()
				return nil, fmt.Errorf("legacy catalogue size differs from current file: %s", item.Stored)
			}
			item.Phase = "bin"
			plan.Files = append(plan.Files, item)
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	if err := b.save(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (b *LegacyBinEngine) syncDir(rel string) error {
	dir, err := b.root.Open(path.Dir(rel))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (b *LegacyBinEngine) move(ctx context.Context, src, dst string, expected LegacyPlanFile) error {
	source, sourceErr := b.regular(src)
	destination, destinationErr := b.regular(dst)
	if errors.Is(sourceErr, os.ErrNotExist) && destinationErr == nil {
		return b.verify(ctx, dst, expected)
	}
	if sourceErr != nil {
		return sourceErr
	}
	if err := b.verify(ctx, src, expected); err != nil {
		return err
	}
	if destinationErr == nil {
		if !os.SameFile(source, destination) {
			return fmt.Errorf("restore destination exists; nothing overwritten: %s", dst)
		}
	} else if errors.Is(destinationErr, os.ErrNotExist) {
		if err := b.root.MkdirAll(path.Dir(dst), 0755); err != nil {
			return err
		}
		if err := b.root.Link(src, dst); err != nil {
			return fmt.Errorf("safe restore could not create link; Bin file retained: %w", err)
		}
		if err := b.syncDir(dst); err != nil {
			return err
		}
	} else {
		return destinationErr
	}
	if b.checkpoint != nil {
		if err := b.checkpoint("after-link"); err != nil {
			return err
		}
	}
	source, sourceErr = b.regular(src)
	destination, destinationErr = b.regular(dst)
	if sourceErr != nil || destinationErr != nil || !os.SameFile(source, destination) {
		return fmt.Errorf("restore paths changed during operation")
	}
	if err := b.root.Remove(src); err != nil {
		return err
	}
	return b.syncDir(src)
}

func (b *LegacyBinEngine) Run(ctx context.Context, id, action, confirmation string) (result *LegacyBinPlan, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	plan, err := b.load(id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			plan.Error = err.Error()
			_ = b.save(plan)
		}
		result = plan
	}()
	if action != "restore" && action != "purge" {
		return plan, ErrInvalid
	}
	if action == "purge" && confirmation != fmt.Sprintf("DELETE %d", len(plan.Files)) {
		return plan, fmt.Errorf("exact deletion confirmation required")
	}
	if plan.State == "restored" || plan.State == "purged" || plan.State == "purged_recovered" {
		return plan, nil
	}
	operationState := "restoring"
	if action == "purge" {
		operationState = "purging"
	}
	if plan.State != "planned" && plan.State != operationState {
		return plan, ErrInvalid
	}
	if plan.State == "planned" {
		for _, file := range plan.Files {
			stored, _ := legacyRelative(file.Stored)
			if err = b.verify(ctx, stored, file); err != nil {
				return plan, err
			}
			if action == "restore" {
				original, _ := legacyRelative(file.Original)
				if _, existsErr := b.regular(original); existsErr == nil {
					return plan, fmt.Errorf("restore destination exists; nothing overwritten: %s", file.Original)
				} else if !errors.Is(existsErr, os.ErrNotExist) {
					return plan, existsErr
				}
			}
		}
		plan.State = operationState
		if err = b.save(plan); err != nil {
			return plan, err
		}
	}
	recovered := false
	for index := range plan.Files {
		file := &plan.Files[index]
		stored, _ := legacyRelative(file.Stored)
		if action == "restore" {
			if file.Phase == "restored" {
				continue
			}
			original, _ := legacyRelative(file.Original)
			file.Phase = "restoring"
			if err = b.save(plan); err != nil {
				return plan, err
			}
			if err = b.move(ctx, stored, original, *file); err != nil {
				return plan, err
			}
			file.Phase = "restored"
		} else {
			if file.Phase == "purged" || file.Phase == "absent_after_intent" {
				continue
			}
			if file.Phase == "deleting" {
				if _, missingErr := b.regular(stored); errors.Is(missingErr, os.ErrNotExist) {
					file.Phase = "absent_after_intent"
					recovered = true
					continue
				}
			}
			if err = b.verify(ctx, stored, *file); err != nil {
				return plan, err
			}
			file.Phase = "deleting"
			if err = b.save(plan); err != nil {
				return plan, err
			}
			if err = b.root.Remove(stored); err != nil {
				return plan, err
			}
			if err = b.syncDir(stored); err != nil {
				return plan, err
			}
			file.Phase = "purged"
		}
		if err = b.save(plan); err != nil {
			return plan, err
		}
	}
	tx, err := b.s.write.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	timestamp := time.Now().UTC().Format(time.RFC3339)
	column := "restored_at"
	if action == "purge" {
		column = "purged_at"
	}
	for _, file := range plan.Files {
		if _, err = tx.ExecContext(ctx, "UPDATE legacy_culled SET "+column+"=? WHERE legacy_id=? AND restored_at IS NULL AND purged_at IS NULL", timestamp, file.ID); err != nil {
			return plan, err
		}
	}
	if action == "restore" {
		plan.State = "restored"
	} else if recovered {
		plan.State = "purged_recovered"
	} else {
		plan.State = "purged"
	}
	plan.Error = ""
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, "UPDATE legacy_file_plans SET body=? WHERE id=?", string(body), plan.ID); err != nil {
		return plan, err
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	return plan, nil
}
