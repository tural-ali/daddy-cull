package catalog

import (
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
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"daddy-cull/next/internal/takeout"
)

// GooglePhotosPlan is a plan for copying one photo from a Takeout export into
// the archive. The export is only read.
type GooglePhotosPlan struct {
	// ID is the plan's id, 32 hexadecimal characters.
	ID string `json:"id"`
	// ItemID is the photo's id from GET /api/google-photos.
	ItemID int64 `json:"itemId"`
	// Archive is the export it is copied from, as the inbox names it.
	Archive string `json:"archive"`
	// ArchiveKind is zip or folder.
	ArchiveKind string `json:"archiveKind"`
	// Entry is where the photo is inside the export.
	Entry string `json:"entry"`
	// Destination is where it goes, relative to the archive root: the day it
	// was taken, as 2019/2019-08/2019-08-14/IMG_1234.JPG, or beside the
	// library's copy as IMG_1234 (Google Photos).JPG when the library holds a
	// different one. A number is added if the name is taken.
	Destination string `json:"destination"`
	// Size is the photo's size in bytes.
	Size int64 `json:"size"`
	// Hash is the SHA-256 of the photo, in hexadecimal. The copy is checked
	// against it.
	Hash string `json:"hash"`
	// Taken is when the photo was taken, in RFC 3339, which the copy is
	// dated with.
	Taken string `json:"taken"`
	// State is where the plan stands: planned (nothing copied yet), copying,
	// copied, or added (the copy is in the archive and recorded).
	State string `json:"state"`
	// Created is when the plan was made, in RFC 3339 UTC.
	Created string `json:"created"`
	// Error says why the last step failed, or is left out.
	Error string `json:"error,omitempty"`
}

// GooglePhotosChoice plans adding one photo from Google Photos.
type GooglePhotosChoice struct {
	// ItemID is the photo's id from GET /api/google-photos.
	ItemID int64 `json:"itemId"`
}

// GooglePhotosWriter copies photos from the Takeout inbox into the archive.
// It runs in the private writer, the only process that writes the archive.
type GooglePhotosWriter struct {
	s          *Store
	inbox      *takeout.Inbox
	archive    *os.Root
	mu         sync.Mutex
	checkpoint func(string) error
}

// NewGooglePhotosWriter reads exports from inboxRoot and writes into the
// archive at archiveRoot.
func NewGooglePhotosWriter(s *Store, inboxRoot, archiveRoot string) (*GooglePhotosWriter, error) {
	if _, err := openGuardedRoot(inboxRoot); err != nil {
		return nil, err
	}
	inbox, err := takeout.OpenInbox(inboxRoot)
	if err != nil {
		return nil, err
	}
	archive, err := openGuardedRoot(archiveRoot)
	if err != nil {
		inbox.Close()
		return nil, err
	}
	return &GooglePhotosWriter{s: s, inbox: inbox, archive: archive}, nil
}

// Close closes the inbox and the archive.
func (w *GooglePhotosWriter) Close() error {
	first := w.inbox.Close()
	second := w.archive.Close()
	if first != nil {
		return first
	}
	return second
}

func (w *GooglePhotosWriter) save(plan *GooglePhotosPlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = w.s.write.Exec("INSERT INTO takeout_plans(id,item_id,body) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", plan.ID, plan.ItemID, string(body))
	return err
}

func parseGooglePhotosPlan(id, body string) (*GooglePhotosPlan, error) {
	var plan GooglePhotosPlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil || plan.ID != id || plan.ItemID < 1 || plan.Size < 0 || len(plan.Hash) != 64 {
		return nil, ErrInvalid
	}
	if !takeout.SafePath(plan.Archive) || !takeout.SafePath(plan.Entry) || !safeRelative(plan.Destination) {
		return nil, ErrInvalid
	}
	if _, err := time.Parse(time.RFC3339, plan.Taken); err != nil {
		return nil, ErrInvalid
	}
	return &plan, nil
}

func (w *GooglePhotosWriter) load(id string) (*GooglePhotosPlan, error) {
	if len(id) != 32 {
		return nil, ErrInvalid
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, ErrInvalid
	}
	var body string
	if err := w.s.read.QueryRow("SELECT body FROM takeout_plans WHERE id=?", id).Scan(&body); err != nil {
		return nil, err
	}
	return parseGooglePhotosPlan(id, body)
}

// fingerprintEntry reads a photo out of its export, answering its SHA-256
// and size. A zip checks its own checksum as the last byte is read.
func (w *GooglePhotosWriter) fingerprintEntry(ctx context.Context, archive, kind, entry string) (string, int64, error) {
	r, err := w.inbox.ReadEntry(archive, kind, entry)
	if err != nil {
		return "", 0, err
	}
	defer r.Close()
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, &contextReader{ctx, r}, make([]byte, 1<<20))
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

// Preview plans adding one photo, checking it can be read and that the
// library does not already hold the same bytes where it would go.
func (w *GooglePhotosWriter) Preview(ctx context.Context, itemID int64) (*GooglePhotosPlan, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// A plan cut short is carried on with rather than made again, so a
	// retry never copies a photo twice.
	var lastID, lastBody string
	err := w.s.read.QueryRowContext(ctx, "SELECT id,body FROM takeout_plans WHERE item_id=? ORDER BY rowid DESC LIMIT 1", itemID).Scan(&lastID, &lastBody)
	if err == nil {
		if last, parseErr := parseGooglePhotosPlan(lastID, lastBody); parseErr == nil && (last.State == "copying" || last.State == "copied") {
			return last, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var name, outcome, state string
	var size int64
	var takenAt, localAt sql.NullInt64
	var matchPath string
	err = w.s.read.QueryRowContext(ctx, `SELECT i.name,i.size_bytes,i.outcome,i.state,i.taken_at,i.local_at,COALESCE(m.relative_path,'')
		FROM takeout_items i LEFT JOIN assets m ON m.id=i.match_asset_id WHERE i.id=?`, itemID).Scan(&name, &size, &outcome, &state, &takenAt, &localAt, &matchPath)
	if err != nil {
		return nil, err
	}
	switch {
	case state == "added":
		return nil, fmt.Errorf("this photo was already added to the library")
	case state != "waiting":
		return nil, fmt.Errorf("this photo was set aside, so it is not added")
	case outcome != TakeoutMissing && outcome != TakeoutAlternative:
		return nil, fmt.Errorf("the library already has this photo, or Cull cannot tell where it belongs")
	case !takenAt.Valid || !localAt.Valid:
		return nil, fmt.Errorf("nobody knows when this photo was taken, so it has no day to go under")
	case !takeout.SafeName(name):
		return nil, fmt.Errorf("the photo's name cannot be used for a file in the library")
	}
	var archive, kind, entry string
	err = w.s.read.QueryRowContext(ctx, `SELECT a.name,a.kind,e.path FROM takeout_entries e JOIN takeout_archives a ON a.id=e.archive_id
		WHERE e.item_id=? AND a.present=1 ORDER BY a.kind='folder' DESC,a.id LIMIT 1`, itemID).Scan(&archive, &kind, &entry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no export in the inbox holds this photo any more")
	}
	if err != nil {
		return nil, err
	}
	hash, read, err := w.fingerprintEntry(ctx, archive, kind, entry)
	if err != nil {
		return nil, fmt.Errorf("the photo could not be read from %s: %w", archive, err)
	}
	if read != size {
		return nil, fmt.Errorf("the photo in %s is not the size it was listed with", archive)
	}
	day := time.Unix(localAt.Int64, 0).UTC()
	target := path.Join(day.Format("2006"), day.Format("2006-01"), day.Format("2006-01-02"), name)
	if outcome == TakeoutAlternative {
		rel, ok := archiveRelative(matchPath)
		if !ok {
			return nil, fmt.Errorf("the library's copy of this photo has moved; read the page again")
		}
		stem := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		target = path.Join(path.Dir(rel), stem+" (Google Photos)"+path.Ext(name))
	}
	if existing, err := w.sameBytesIn(ctx, path.Dir(target), hash, size); err != nil {
		return nil, err
	} else if existing != "" {
		return nil, fmt.Errorf("the library already holds this photo as %s", existing)
	}
	destination, err := freeArchiveName(w.archive, target)
	if err != nil {
		return nil, err
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	plan := &GooglePhotosPlan{ID: id, ItemID: itemID, Archive: archive, ArchiveKind: kind, Entry: entry, Destination: destination,
		Size: size, Hash: hash, Taken: time.Unix(takenAt.Int64, 0).UTC().Format(time.RFC3339), State: "planned", Created: nowUTC()}
	if err = w.save(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// sameBytesIn finds a file in dir with exactly these bytes, which a photo
// already added, or held by the library under another name, would be.
func (w *GooglePhotosWriter) sameBytesIn(ctx context.Context, dir, hash string, size int64) (string, error) {
	entries, err := readRootDir(w.archive, dir)
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
		other, _, err := fingerprintIn(ctx, w.archive, rel, false)
		if err != nil {
			return "", err
		}
		if other == hash {
			return rel, nil
		}
	}
	return "", nil
}

func readRootDir(root *os.Root, dir string) ([]fs.DirEntry, error) {
	if _, err := guardedDir(root, dir); err != nil {
		return nil, err
	}
	f, err := root.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// guardedDir checks that dir is a folder reached without a symlink.
func guardedDir(root *os.Root, dir string) (os.FileInfo, error) {
	if !safeRelative(dir) {
		return nil, ErrInvalid
	}
	parts := strings.Split(dir, "/")
	var info os.FileInfo
	for i := range parts {
		var err error
		if info, err = root.Lstat(strings.Join(parts[:i+1], "/")); err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("not a plain folder: %s", dir)
		}
	}
	return info, nil
}

// mkdirShared makes the folders of dir that are missing, open to everyone
// who uses the archive, as its other folders are: the writer's own umask
// would otherwise leave them writable only by the writer.
func mkdirShared(root *os.Root, dir string) error {
	if !safeRelative(dir) {
		return ErrInvalid
	}
	parts := strings.Split(dir, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		err := root.Mkdir(p, 0o777)
		if err == nil {
			if err = root.Chmod(p, 0o777); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := root.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("not a plain folder: %s", p)
		}
	}
	return nil
}

func (w *GooglePhotosWriter) verify(ctx context.Context, rel string, plan *GooglePhotosPlan) error {
	hash, size, err := fingerprintIn(ctx, w.archive, rel, false)
	if err != nil {
		return err
	}
	if hash != plan.Hash || size != plan.Size {
		return fmt.Errorf("the copy in the archive does not match the photo")
	}
	return nil
}

// Run carries out a plan: the photo is copied beside where it goes, checked,
// dated with when it was taken, and linked into place without replacing
// anything; then it is recorded as added. A plan cut short is carried on
// with by running it again.
func (w *GooglePhotosWriter) Run(ctx context.Context, id string) (result *GooglePhotosPlan, err error) {
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
	if plan.State == "added" {
		return plan, nil
	}
	if plan.State != "planned" && plan.State != "copying" && plan.State != "copied" {
		return plan, ErrInvalid
	}
	var state string
	if err = w.s.read.QueryRowContext(ctx, "SELECT state FROM takeout_items WHERE id=?", plan.ItemID).Scan(&state); err != nil {
		return plan, err
	}
	if state != "waiting" && plan.State == "planned" {
		return plan, fmt.Errorf("this photo is no longer waiting to be added")
	}
	if plan.State == "planned" {
		if _, destinationErr := guardedRegular(w.archive, plan.Destination, false); destinationErr == nil {
			return plan, fmt.Errorf("a file appeared where the photo was going; read the page again")
		} else if !errors.Is(destinationErr, os.ErrNotExist) {
			return plan, destinationErr
		}
		plan.State = "copying"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	if plan.State == "copying" {
		if err = w.copyIn(ctx, plan); err != nil {
			return plan, err
		}
		plan.State = "copied"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	tx, err := w.s.write.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE takeout_items SET state='added',added_as=?,added_at=? WHERE id=?", "/archive/"+plan.Destination, nowUTC(), plan.ItemID); err != nil {
		return plan, err
	}
	plan.State, plan.Error = "added", ""
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, "UPDATE takeout_plans SET body=? WHERE id=?", string(body), plan.ID); err != nil {
		return plan, err
	}
	return plan, tx.Commit()
}

func (w *GooglePhotosWriter) copyIn(ctx context.Context, plan *GooglePhotosPlan) error {
	if _, err := guardedRegular(w.archive, plan.Destination, false); err == nil {
		// Copied before a restart cut the plan short, or something else
		// put a file there: only the photo's own bytes are taken as done.
		if err := w.verify(ctx, plan.Destination, plan); err != nil {
			return fmt.Errorf("a different file is where the photo was going; nothing was replaced")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := path.Dir(plan.Destination)
	if err := mkdirShared(w.archive, dir); err != nil {
		return err
	}
	temp := path.Join(dir, ".daddy-cull-"+plan.ID+".tmp")
	_ = w.archive.Remove(temp)
	source, err := w.inbox.ReadEntry(plan.Archive, plan.ArchiveKind, plan.Entry)
	if err != nil {
		return fmt.Errorf("the photo could not be read from %s: %w", plan.Archive, err)
	}
	target, err := w.archive.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
	if err != nil {
		source.Close()
		return err
	}
	hash := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(target, hash), &contextReader{ctx, source}, make([]byte, 1<<20))
	if copyErr == nil {
		copyErr = target.Sync()
	}
	targetErr := target.Close()
	sourceErr := source.Close()
	if copyErr == nil && targetErr != nil {
		copyErr = targetErr
	}
	if copyErr == nil && sourceErr != nil {
		copyErr = sourceErr
	}
	if copyErr == nil && (n != plan.Size || hex.EncodeToString(hash.Sum(nil)) != plan.Hash) {
		copyErr = fmt.Errorf("the photo in %s changed since it was planned", plan.Archive)
	}
	if copyErr != nil {
		w.archive.Remove(temp)
		return copyErr
	}
	defer w.archive.Remove(temp)
	taken, _ := time.Parse(time.RFC3339, plan.Taken)
	if err := w.archive.Chtimes(temp, taken, taken); err != nil {
		return err
	}
	if err := w.verify(ctx, temp, plan); err != nil {
		return err
	}
	if err := w.archive.Link(temp, plan.Destination); err != nil {
		return fmt.Errorf("a file appeared where the photo was going; nothing was replaced: %w", err)
	}
	if err := syncRootDir(w.archive, plan.Destination); err != nil {
		return err
	}
	if w.checkpoint != nil {
		if err := w.checkpoint("after-copy"); err != nil {
			return err
		}
	}
	return w.verify(ctx, plan.Destination, plan)
}

// Handler serves the writer's routes to the web process, which holds the
// same secret.
func (w *GooglePhotosWriter) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	respond := func(writer http.ResponseWriter, value any, err error) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		if err != nil {
			writer.WriteHeader(409)
			json.NewEncoder(writer).Encode(map[string]any{"error": err.Error(), "plan": value})
			return
		}
		json.NewEncoder(writer).Encode(value)
	}
	decode := func(writer http.ResponseWriter, request *http.Request, value any) error {
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(value); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("one request required")
		}
		return nil
	}
	mux.HandleFunc("POST /google-photos/preview", func(writer http.ResponseWriter, request *http.Request) {
		var input GooglePhotosChoice
		if err := decode(writer, request, &input); err != nil {
			respond(writer, nil, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		plan, err := w.Preview(ctx, input.ItemID)
		respond(writer, plan, err)
	})
	mux.HandleFunc("POST /google-photos/execute", func(writer http.ResponseWriter, request *http.Request) {
		var input PlanRef
		if err := decode(writer, request, &input); err != nil {
			respond(writer, nil, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		plan, err := w.Run(ctx, input.ID)
		respond(writer, plan, err)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(request.Header.Get("X-Bin-Key"))) != 1 {
			http.Error(writer, "forbidden", 403)
			return
		}
		mux.ServeHTTP(writer, request)
	})
}
