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
	"syscall"
	"time"
)

// BinAsset is one photograph of a Bin plan, as the catalogue had it when the
// plan was made.
type BinAsset struct {
	// ID is the file's id in the catalogue.
	ID int64 `json:"id"`
	// Path is where the file is in the catalogue, such as
	// /archive/2019/2019-08/2019-08-14/IMG_1234.HEIC.
	Path string `json:"path"`
	// Revision is the file's decision revision when the plan was made. The
	// move is refused if the decision has changed since.
	Revision int64 `json:"revision"`
	// Size is the file's size in bytes, as the catalogue records it.
	Size int64 `json:"size"`
}

// BinFile is one file a Bin plan moves: a photograph or one of its sidecars.
type BinFile struct {
	// Original is where the file came from and where restoring puts it back,
	// relative to the archive root, without /archive/ in front.
	Original string `json:"original"`
	// Size is the file's size in bytes when the plan was made.
	Size int64 `json:"size"`
	// Mtime is when the file was last modified, as it was when the plan was
	// made, in Unix nanoseconds.
	Mtime int64 `json:"mtime"`
	// Hash is the SHA-256 of the file's contents, in hexadecimal. The file
	// is checked against it before every move and before it is deleted.
	Hash string `json:"hash"`
	// Phase is where the file stands: planned, moving (on its way into the
	// Bin), bin, restoring, restored, returned (given back on its own,
	// leaving the rest of the plan in the Bin), purging, purged, or
	// absent_after_intent (gone after its deletion started, without the
	// deletion being seen to finish).
	Phase string `json:"phase"`
	// Sidecar is true for a sidecar, which belongs to the nearest photograph
	// listed before it.
	Sidecar bool `json:"sidecar"`
}

// BinPlan is a plan for moving files from the archive into the Bin, and what
// has become of them since: restored, or deleted for good.
type BinPlan struct {
	// ID is the plan's id, 32 hexadecimal characters.
	ID string `json:"id"`
	// State is where the plan stands: planned (nothing moved yet),
	// quarantining, bin, restoring, restored, purging, purged, or
	// purged_recovered (deleted, with at least one file found already gone
	// after its deletion started).
	State string `json:"state"`
	// Created is when the plan was made, in RFC 3339 UTC.
	Created string `json:"created"`
	// Assets are the photographs the plan moves.
	Assets []BinAsset `json:"assets"`
	// Files are every file the plan moves, each photograph followed by its
	// sidecars.
	Files []BinFile `json:"files"`
	// Warnings are notes to show the reviewer, such as a sidecar shared with
	// another file and so left in the archive, or a file found gone during a
	// resumed deletion. It is an empty list when there are none.
	Warnings []string `json:"warnings"`
	// Error says why the last step failed. It is cleared when a step
	// finishes, and left out when there is none.
	Error string `json:"error,omitempty"`
}
type BinEngine struct {
	s          *Store
	root       *os.Root
	mu         sync.Mutex
	checkpoint func(string) error
	lock       *os.File
}

func NewBinEngine(s *Store, root string) (*BinEngine, error) {
	if root == "" || root == "/" || !path.IsAbs(root) {
		return nil, ErrInvalid
	}
	resolved, e := filepath.EvalSymlinks(filepath.Clean(root))
	if e != nil {
		return nil, e
	}
	if resolved == string(filepath.Separator) {
		return nil, ErrInvalid
	}
	root = resolved
	r, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	if e = r.MkdirAll(".culled/next", 0700); e != nil {
		r.Close()
		return nil, e
	}
	for _, part := range []string{".culled", ".culled/next"} {
		st, e := r.Lstat(part)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			r.Close()
			return nil, fmt.Errorf("Bin path must be a real directory")
		}
	}
	for _, dir := range []string{".", ".culled", ".culled/next"} {
		f, e := r.Open(dir)
		if e != nil {
			r.Close()
			return nil, e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			r.Close()
			return nil, e
		}
	}
	lock, e := r.OpenFile(".culled/next/.writer.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		r.Close()
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		r.Close()
		return nil, fmt.Errorf("another archive writer is active: %w", e)
	}
	return &BinEngine{s: s, root: r, lock: lock}, nil
}
func (b *BinEngine) Close() error {
	if b.lock != nil {
		b.lock.Close()
	}
	return b.root.Close()
}
func safeRelative(p string) bool {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, "\\") {
		return false
	}
	for _, v := range strings.Split(p, "/") {
		if v == ".." || v == "." || strings.HasPrefix(v, ".") {
			return false
		}
	}
	return true
}
func (b *BinEngine) regular(p string) (os.FileInfo, error) {
	// No symlinks, including symlinked directories inside the permitted root.
	parts := strings.Split(p, "/")
	for i := range parts {
		st, e := b.root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink refused: %s", p)
		}
	}
	st, e := b.root.Stat(p)
	if e == nil && !st.Mode().IsRegular() {
		e = fmt.Errorf("not a regular file: %s", p)
	}
	return st, e
}
func (b *BinEngine) fingerprint(ctx context.Context, p string) (BinFile, error) {
	st, e := b.regular(p)
	if e != nil {
		return BinFile{}, e
	}
	f, e := b.root.Open(p)
	if e != nil {
		return BinFile{}, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(st, opened) {
		return BinFile{}, fmt.Errorf("file changed while opening: %s", p)
	}
	h := sha256.New()
	buf := make([]byte, 1024*1024)
	for {
		if e = ctx.Err(); e != nil {
			return BinFile{}, e
		}
		n, er := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return BinFile{}, er
		}
	}
	after, e := b.regular(p)
	if e != nil || !os.SameFile(st, after) || st.Size() != after.Size() || st.ModTime() != after.ModTime() {
		return BinFile{}, fmt.Errorf("file changed during verification: %s", p)
	}
	return BinFile{Original: p, Size: st.Size(), Mtime: st.ModTime().UnixNano(), Hash: hex.EncodeToString(h.Sum(nil)), Phase: "planned"}, nil
}

var sidecarExt = map[string]bool{"xmp": true, "json": true, "xml": true, "aae": true, "thm": true, "lrv": true}

func (b *BinEngine) sidecars(p string) ([]string, []string, error) {
	dir := path.Dir(p)
	f, e := b.root.Open(dir)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	entries, e := f.ReadDir(-1)
	if e != nil {
		return nil, nil, e
	}
	names := make([]string, len(entries))
	for i, ent := range entries {
		names[i] = ent.Name()
	}
	own, shared := ownSidecars(path.Base(p), names)
	var found, warnings []string
	for _, name := range own {
		found = append(found, path.Join(dir, name))
	}
	for _, name := range shared {
		warnings = append(warnings, "Shared sidecar stays in archive: "+path.Join(dir, name))
	}
	return found, warnings, nil
}

// ownSidecars picks, from the names in a file's folder, the sidecars that are
// the file's own and go where it goes: name.ext.xmp always, and stem.xmp
// unless another file shares the stem, as the photo and video of a Live Photo
// do. Those shared ones stay where they are, and are returned apart.
func ownSidecars(base string, names []string) (own, shared []string) {
	stem := strings.TrimSuffix(base, path.Ext(base))
	together := false
	for _, name := range names {
		if name != base && !sidecarExt[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))] && strings.EqualFold(strings.TrimSuffix(name, path.Ext(name)), stem) {
			together = true
		}
	}
	for _, name := range names {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		if !sidecarExt[ext] {
			continue
		}
		prefix := strings.TrimSuffix(name, path.Ext(name))
		if prefix == base {
			own = append(own, name)
		} else if strings.EqualFold(prefix, stem) {
			if together {
				shared = append(shared, name)
			} else {
				own = append(own, name)
			}
		}
	}
	return own, shared
}

func (b *BinEngine) save(p *BinPlan) error {
	data, e := json.Marshal(p)
	if e != nil {
		return e
	}
	_, e = b.s.write.Exec("INSERT INTO file_plans(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", p.ID, string(data))
	return e
}
func (b *BinEngine) load(id string) (*BinPlan, error) {
	if len(id) != 32 {
		return nil, ErrInvalid
	}
	if _, e := hex.DecodeString(id); e != nil {
		return nil, ErrInvalid
	}
	var raw string
	if e := b.s.read.QueryRow("SELECT body FROM file_plans WHERE id=?", id).Scan(&raw); e != nil {
		return nil, e
	}
	var p BinPlan
	if e := json.Unmarshal([]byte(raw), &p); e != nil {
		return nil, e
	}
	if p.ID != id || len(p.Files) == 0 {
		return nil, ErrInvalid
	}
	for _, f := range p.Files {
		if !safeRelative(f.Original) || len(f.Hash) != 64 {
			return nil, ErrInvalid
		}
	}
	return &p, nil
}
func stored(p *BinPlan, i int) string {
	return fmt.Sprintf(".culled/next/%s/%04d-%s", p.ID, i, path.Base(p.Files[i].Original))
}
func (b *BinEngine) Preview(ctx context.Context, ids []int64) (*BinPlan, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(ids) < 1 || len(ids) > 20 {
		return nil, fmt.Errorf("select between 1 and 20 media files")
	}
	random := make([]byte, 16)
	if _, e := rand.Read(random); e != nil {
		return nil, e
	}
	p := &BinPlan{ID: hex.EncodeToString(random), State: "planned", Created: time.Now().UTC().Format(time.RFC3339), Warnings: []string{}}
	seenIDs := map[int64]bool{}
	seenFiles := map[string]bool{}
	for _, id := range ids {
		if seenIDs[id] {
			return nil, ErrInvalid
		}
		seenIDs[id] = true
		var a BinAsset
		var source, status string
		e := b.s.read.QueryRowContext(ctx, "SELECT a.id,a.relative_path,a.size_bytes,a.source_id,d.status,d.revision FROM assets a JOIN decisions d ON d.asset_id=a.id WHERE a.id=? AND NOT EXISTS(SELECT 1 FROM file_state f WHERE f.asset_id=a.id AND f.state!='restored')", id).Scan(&a.ID, &a.Path, &a.Size, &source, &status, &a.Revision)
		if e != nil {
			return nil, e
		}
		if source != "archive" || status != "cull" || !strings.HasPrefix(a.Path, "/archive/") {
			return nil, fmt.Errorf("only explicitly marked archive files can enter the Bin")
		}
		rel := strings.TrimPrefix(a.Path, "/archive/")
		if !safeRelative(rel) {
			return nil, ErrInvalid
		}
		files, warnings, e := b.sidecars(rel)
		if e != nil {
			return nil, e
		}
		p.Warnings = append(p.Warnings, warnings...)
		files = append([]string{rel}, files...)
		for i, file := range files {
			if seenFiles[file] {
				continue
			}
			seenFiles[file] = true
			fp, e := b.fingerprint(ctx, file)
			if e != nil {
				return nil, e
			}
			if i == 0 && fp.Size != a.Size {
				return nil, fmt.Errorf("catalogue size differs from current file: %s", rel)
			}
			fp.Sidecar = i > 0
			p.Files = append(p.Files, fp)
		}
		p.Assets = append(p.Assets, a)
	}
	if e := b.save(p); e != nil {
		return nil, e
	}
	return p, nil
}
func (b *BinEngine) verify(ctx context.Context, p string, expected BinFile) error {
	got, e := b.fingerprint(ctx, p)
	if e != nil {
		return e
	}
	if got.Hash != expected.Hash || got.Size != expected.Size {
		return fmt.Errorf("content changed: %s", p)
	}
	return nil
}
func (b *BinEngine) syncDir(p string) error {
	f, e := b.root.Open(path.Dir(p))
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (b *BinEngine) check(label string) error {
	if b.checkpoint != nil {
		return b.checkpoint(label)
	}
	return nil
}

// Hard-link then unlink gives no-overwrite semantics. Unsupported filesystems fail closed.
// A recorded intent plus same-file check makes either interruption point recoverable.
func (b *BinEngine) move(ctx context.Context, src, dst string, expected BinFile) error {
	si, se := b.regular(src)
	di, de := b.regular(dst)
	if errors.Is(se, os.ErrNotExist) && de == nil {
		return b.verify(ctx, dst, expected)
	}
	if se != nil {
		return se
	}
	if e := b.verify(ctx, src, expected); e != nil {
		return e
	}
	if de == nil {
		if !os.SameFile(si, di) {
			return fmt.Errorf("destination exists; nothing overwritten: %s", dst)
		}
	} else {
		if !errors.Is(de, os.ErrNotExist) {
			return de
		}
		if e := b.root.MkdirAll(path.Dir(dst), 0700); e != nil {
			return e
		}
		if e := b.root.Link(src, dst); e != nil {
			return fmt.Errorf("safe move could not create link; source retained: %w", e)
		}
		if e := b.syncDir(dst); e != nil {
			return e
		}
	}
	if e := b.check("after-link"); e != nil {
		return e
	}
	si, se = b.regular(src)
	di, de = b.regular(dst)
	if se != nil || de != nil || !os.SameFile(si, di) {
		return fmt.Errorf("source/destination changed during move")
	}
	if e := b.verify(ctx, src, expected); e != nil {
		return e
	}
	if e := b.root.Remove(src); e != nil {
		return e
	}
	if e := b.syncDir(src); e != nil {
		return e
	}
	return b.check("after-unlink")
}
func (b *BinEngine) reserve(p *BinPlan) (err error) {
	oldState := p.State
	defer func() {
		if err != nil {
			p.State = oldState
		}
	}()
	tx, e := b.s.write.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, a := range p.Assets {
		var status, source, actual string
		var revision int64
		if e = tx.QueryRow("SELECT d.status,d.revision,a.source_id,a.relative_path FROM decisions d JOIN assets a ON a.id=d.asset_id WHERE d.asset_id=?", a.ID).Scan(&status, &revision, &source, &actual); e != nil {
			return e
		}
		if status != "cull" || revision != a.Revision || source != "archive" || actual != a.Path {
			return fmt.Errorf("decision changed; create a new preview")
		}
		var n int
		if e = tx.QueryRow("SELECT count(*) FROM file_state WHERE asset_id=? AND state!='restored'", a.ID).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fmt.Errorf("file already belongs to a Bin operation")
		}
		if _, e = tx.Exec("INSERT INTO file_state VALUES(?,'quarantining',?) ON CONFLICT(asset_id) DO UPDATE SET state='quarantining',plan_id=excluded.plan_id", a.ID, p.ID); e != nil {
			return e
		}
	}
	p.State = "quarantining"
	raw, _ := json.Marshal(p)
	if _, e = tx.Exec("UPDATE file_plans SET body=? WHERE id=?", string(raw), p.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (b *BinEngine) finish(p *BinPlan, state string) (err error) {
	oldState := p.State
	defer func() {
		if err != nil {
			p.State = oldState
		}
	}()
	tx, e := b.s.write.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	p.State = state
	p.Error = ""
	raw, _ := json.Marshal(p)
	if _, e = tx.Exec("UPDATE file_plans SET body=? WHERE id=?", string(raw), p.ID); e != nil {
		return e
	}
	// A photograph given back on its own is the catalogue's again: finishing
	// the rest of its batch must not claim it back or reset its decision.
	if _, e = tx.Exec("UPDATE file_state SET state=? WHERE plan_id=? AND state!='restored'", state, p.ID); e != nil {
		return e
	}
	if state == "restored" {
		for _, a := range p.Assets {
			if returnedAsset(p, a) {
				continue
			}
			if _, e = tx.Exec("UPDATE decisions SET status='unreviewed',revision=revision+1 WHERE asset_id=?", a.ID); e != nil {
				return e
			}
		}
	}
	return tx.Commit()
}
func (b *BinEngine) checkSidecar(p *BinPlan, f BinFile) error {
	for i, m := range p.Files {
		if !m.Sidecar && (m.Phase == "bin" || m.Phase == "moving" || m.Phase == "purging" || m.Phase == "purged" || m.Phase == "absent_after_intent") {
			original, oe := b.regular(m.Original)
			if oe == nil {
				saved, se := b.regular(stored(p, i))
				if se != nil || !os.SameFile(original, saved) {
					return fmt.Errorf("media path was reused; sidecars retained: %s", m.Original)
				}
			} else if !errors.Is(oe, os.ErrNotExist) {
				return oe
			}
		}
	}
	for _, a := range p.Assets {
		media := strings.TrimPrefix(a.Path, "/archive/")
		if path.Dir(media) != path.Dir(f.Original) {
			continue
		}
		base := path.Base(media)
		stem := strings.TrimSuffix(base, path.Ext(base))
		sideStem := strings.TrimSuffix(path.Base(f.Original), path.Ext(f.Original))
		if sideStem == base {
			return nil
		}
		if !strings.EqualFold(sideStem, stem) {
			continue
		}
		dir, e := b.root.Open(path.Dir(media))
		if e != nil {
			return e
		}
		entries, e := dir.ReadDir(-1)
		dir.Close()
		if e != nil {
			return e
		}
		shared := false
		for _, ent := range entries {
			name := ent.Name()
			if name != base && !sidecarExt[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))] && strings.EqualFold(strings.TrimSuffix(name, path.Ext(name)), stem) {
				shared = true
				break
			}
		}
		if !shared {
			return nil
		}
	}

	return fmt.Errorf("sidecar ownership changed; restore this batch and create a new preview: %s", f.Original)
}
func (b *BinEngine) checkOwnership(p *BinPlan) error {
	for _, f := range p.Files {
		if f.Sidecar && f.Phase != "returned" {
			if e := b.checkSidecar(p, f); e != nil {
				return e
			}
		}
	}
	return nil
}
func (b *BinEngine) receipt(p *BinPlan) error {
	dir := ".culled/next/" + p.ID
	if e := b.root.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := b.syncDir(dir); e != nil {
		return e
	}
	if _, e := b.regular(dir + "/receipt.json"); e == nil {
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	data, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	tmp := dir + "/receipt-" + hex.EncodeToString(nonce) + ".tmp"
	f, e := b.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer b.root.Remove(tmp)
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = b.root.Link(tmp, dir+"/receipt.json"); e != nil {
		return e
	}
	return b.syncDir(dir + "/receipt.json")
}
func (b *BinEngine) Run(ctx context.Context, id, action, confirmation string) (result *BinPlan, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, e := b.load(id)
	if e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			p.Error = err.Error()
			_ = b.save(p)
		}
		result = p
	}()
	switch action {
	case "quarantine":
		if p.State == "bin" {
			return p, nil
		}
		if p.State != "planned" && p.State != "quarantining" {
			return p, ErrInvalid
		}
		for i, f := range p.Files {
			if f.Phase == "bin" {
				continue
			}
			if e = writableFolder(b.root, f.Original); e != nil {
				return p, e
			}
			if e = writableFolder(b.root, stored(p, i)); e != nil {
				return p, e
			}
		}
		if p.State == "planned" {
			for _, f := range p.Files {
				if e = b.verify(ctx, f.Original, f); e != nil {
					return p, e
				}
			}
			if e = b.checkOwnership(p); e != nil {
				return p, e
			}
			if e = b.reserve(p); e != nil {
				return p, e
			}
		}
		for i, f := range p.Files {
			if f.Phase == "bin" {
				if e = b.verify(ctx, stored(p, i), f); e != nil {
					return p, e
				}
			}
		}
		if e = b.receipt(p); e != nil {
			return p, e
		}

		for i := range p.Files {
			f := &p.Files[i]
			if f.Phase == "bin" {
				continue
			}
			if f.Sidecar {
				if e = b.checkSidecar(p, *f); e != nil {
					return p, e
				}
			}
			f.Phase = "moving"
			if e = b.save(p); e != nil {
				return p, e
			}
			if e = b.move(ctx, f.Original, stored(p, i), *f); e != nil {
				return p, e
			}
			f.Phase = "bin"
			if e = b.save(p); e != nil {
				return p, e
			}
		}
		return p, b.finish(p, "bin")
	case "restore":
		if p.State == "restored" {
			return p, nil
		}
		if p.State != "bin" && p.State != "restoring" && p.State != "quarantining" {
			return p, ErrInvalid
		}
		// Preflight every file before moving any member of the batch. A file
		// already given back on its own is the archive's again and is left be.
		for i, f := range p.Files {
			if f.Phase == "returned" {
				continue
			}
			if f.Phase != "restored" {
				if e = writableFolder(b.root, f.Original); e != nil {
					return p, e
				}
				if e = writableFolder(b.root, stored(p, i)); e != nil {
					return p, e
				}
			}
			if f.Phase == "restored" {
				if e = b.verify(ctx, f.Original, f); e != nil {
					return p, e
				}
				continue
			}
			si, se := b.regular(stored(p, i))
			di, de := b.regular(f.Original)
			if errors.Is(se, os.ErrNotExist) && de == nil && (f.Phase == "planned" || f.Phase == "moving" || f.Phase == "restoring") {
				if e = b.verify(ctx, f.Original, f); e != nil {
					return p, e
				}
				continue
			}
			if se != nil {
				return p, se
			}
			if e = b.verify(ctx, stored(p, i), f); e != nil {
				return p, e
			}
			if de == nil && !os.SameFile(si, di) {
				return p, fmt.Errorf("restore destination occupied: %s", f.Original)
			}
			if de != nil && !errors.Is(de, os.ErrNotExist) {
				return p, de
			}

		}
		p.State = "restoring"
		if e = b.save(p); e != nil {
			return p, e
		}
		for i := range p.Files {
			f := &p.Files[i]
			if f.Phase == "restored" || f.Phase == "returned" {
				continue
			}
			f.Phase = "restoring"
			if e = b.save(p); e != nil {
				return p, e
			}
			if e = b.move(ctx, stored(p, i), f.Original, *f); e != nil {
				return p, e
			}
			f.Phase = "restored"
			if e = b.save(p); e != nil {
				return p, e
			}
		}
		return p, b.finish(p, "restored")
	case "purge":
		if confirmation != fmt.Sprintf("DELETE %d", len(p.Files)) {
			return p, fmt.Errorf("type DELETE %d to delete precisely this batch", len(p.Files))
		}
		if p.State == "purged" || p.State == "purged_recovered" {
			return p, nil
		}
		if p.State != "bin" && p.State != "purging" {
			return p, ErrInvalid
		}
		// A single-file restore that stopped part-way has files on both sides;
		// deleting now could take the sidecar of a photograph already back.
		for _, f := range p.Files {
			if f.Phase == "restoring" {
				return p, fmt.Errorf("restoring %s stopped part-way; restore that file again before deleting the batch", f.Original)
			}
		}
		if e = b.checkOwnership(p); e != nil {
			return p, e
		}
		// Absence without durable deletion intent is always an error.
		for i, f := range p.Files {
			if f.Phase == "purged" || f.Phase == "absent_after_intent" || f.Phase == "returned" {
				continue
			}
			if e = writableFolder(b.root, stored(p, i)); e != nil {
				return p, e
			}
			if e = b.verify(ctx, stored(p, i), f); e != nil {
				if f.Phase == "purging" && errors.Is(e, os.ErrNotExist) {
					p.Files[i].Phase = "absent_after_intent"
					p.Warnings = append(p.Warnings, "Recovery: file absent after recorded deletion intent; deletion completion was not observed: "+f.Original)
				} else {
					return p, e
				}
			}
		}

		p.State = "purging"
		if e = b.save(p); e != nil {
			return p, e
		}
		for i := range p.Files {
			f := &p.Files[i]
			if f.Phase == "purged" || f.Phase == "absent_after_intent" || f.Phase == "returned" {
				continue
			}
			if f.Sidecar {
				if e = b.checkSidecar(p, *f); e != nil {
					return p, e
				}
			}
			f.Phase = "purging"
			if e = b.save(p); e != nil {
				return p, e
			}
			if e = b.verify(ctx, stored(p, i), *f); e != nil {
				return p, e
			}
			if e = b.root.Remove(stored(p, i)); e != nil {
				return p, e
			}
			if e = b.syncDir(stored(p, i)); e != nil {
				return p, e
			}
			if e = b.check("after-delete"); e != nil {
				return p, e
			}
			f.Phase = "purged"
			if e = b.save(p); e != nil {
				return p, e
			}
		}
		state := "purged"
		for _, f := range p.Files {
			if f.Phase == "absent_after_intent" {
				state = "purged_recovered"
			}
		}
		return p, b.finish(p, state)
	default:
		return p, ErrInvalid
	}
}

// assetFiles returns where one photograph's files sit in its batch: its media
// file and the sidecars Preview recorded straight after it.
func assetFiles(p *BinPlan, a BinAsset) []int {
	media := strings.TrimPrefix(a.Path, "/archive/")
	for i, f := range p.Files {
		if f.Sidecar || f.Original != media {
			continue
		}
		indexes := []int{i}
		for j := i + 1; j < len(p.Files) && p.Files[j].Sidecar; j++ {
			indexes = append(indexes, j)
		}
		return indexes
	}
	return nil
}

// returnedAsset reports whether a photograph was given back on its own.
func returnedAsset(p *BinPlan, a BinAsset) bool {
	indexes := assetFiles(p, a)
	return len(indexes) > 0 && p.Files[indexes[0]].Phase == "returned"
}

// Return puts one photograph of a batch back where it came from, sidecars
// included, and leaves the rest of the batch in the Bin. It moves files with
// the same checks as a whole restore, and every file is verified before any is
// moved. Interrupted, it is resumed by asking again; until then the batch's
// own restore and deletion refuse to guess, because the half-moved files are
// in neither place they expect.
func (b *BinEngine) Return(ctx context.Context, id string, assetID int64) (result *BinPlan, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, e := b.load(id)
	if e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			p.Error = err.Error()
			_ = b.save(p)
		}
		result = p
	}()
	var asset *BinAsset
	for i := range p.Assets {
		if p.Assets[i].ID == assetID {
			asset = &p.Assets[i]
		}
	}
	if asset == nil {
		return p, fmt.Errorf("this file is not part of the batch")
	}
	indexes := assetFiles(p, *asset)
	if len(indexes) == 0 {
		return p, ErrInvalid
	}
	if p.Files[indexes[0]].Phase == "returned" {
		return p, nil
	}
	if p.State != "bin" {
		return p, fmt.Errorf("this batch is part-way through another action; restore or delete it as a whole")
	}
	for _, i := range indexes {
		f := p.Files[i]
		if f.Phase != "bin" && f.Phase != "restoring" {
			return p, fmt.Errorf("file is not in the Bin: %s", f.Original)
		}
		if e = writableFolder(b.root, f.Original); e != nil {
			return p, e
		}
		if e = writableFolder(b.root, stored(p, i)); e != nil {
			return p, e
		}
		si, se := b.regular(stored(p, i))
		di, de := b.regular(f.Original)
		if f.Phase == "restoring" && errors.Is(se, os.ErrNotExist) && de == nil {
			if e = b.verify(ctx, f.Original, f); e != nil {
				return p, e
			}
			continue
		}
		if se != nil {
			return p, se
		}
		if e = b.verify(ctx, stored(p, i), f); e != nil {
			return p, e
		}
		if de == nil && !os.SameFile(si, di) {
			return p, fmt.Errorf("restore destination occupied: %s", f.Original)
		}
		if de != nil && !errors.Is(de, os.ErrNotExist) {
			return p, de
		}
	}
	for _, i := range indexes {
		f := &p.Files[i]
		f.Phase = "restoring"
		if e = b.save(p); e != nil {
			return p, e
		}
		if e = b.move(ctx, stored(p, i), f.Original, *f); e != nil {
			return p, e
		}
	}
	return p, b.returned(p, *asset, indexes)
}

// returned records a photograph given back, in one transaction with its
// catalogue state, so it is never back on disk yet still claimed by the Bin.
// The batch is finished as restored once nothing of it is left in the Bin.
func (b *BinEngine) returned(p *BinPlan, a BinAsset, indexes []int) (err error) {
	oldState, oldPhases := p.State, make([]string, len(indexes))
	for n, i := range indexes {
		oldPhases[n] = p.Files[i].Phase
		p.Files[i].Phase = "returned"
	}
	defer func() {
		if err != nil {
			p.State = oldState
			for n, i := range indexes {
				p.Files[i].Phase = oldPhases[n]
			}
		}
	}()
	left := false
	for _, f := range p.Files {
		if f.Phase != "returned" {
			left = true
		}
	}
	if !left {
		p.State = "restored"
	}
	p.Error = ""
	tx, e := b.s.write.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(p)
	if _, e = tx.Exec("UPDATE file_plans SET body=? WHERE id=?", string(raw), p.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE file_state SET state='restored' WHERE asset_id=? AND plan_id=?", a.ID, p.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE decisions SET status='unreviewed',revision=revision+1 WHERE asset_id=?", a.ID); e != nil {
		return e
	}
	return tx.Commit()
}

func (b *BinEngine) List() ([]*BinPlan, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rows, e := b.s.read.Query("SELECT body FROM file_plans WHERE json_extract(body,'$.state') NOT IN ('planned','restored','purged','purged_recovered') OR rowid IN (SELECT rowid FROM file_plans WHERE json_extract(body,'$.state') IN ('restored','purged','purged_recovered') ORDER BY rowid DESC LIMIT 100) ORDER BY rowid DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	plans := []*BinPlan{}
	for rows.Next() {
		var raw string
		var p BinPlan
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &p); e != nil {
			return nil, e
		}
		// A plan stored with a list left null is still sent with an empty
		// one, as the reference says.
		if p.Assets == nil {
			p.Assets = []BinAsset{}
		}
		if p.Files == nil {
			p.Files = []BinFile{}
		}
		if p.Warnings == nil {
			p.Warnings = []string{}
		}
		plans = append(plans, &p)
	}
	return plans, rows.Err()
}
