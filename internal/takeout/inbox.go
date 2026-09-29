package takeout

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Inbox is the folder Takeout exports are dropped into. Cull only ever reads
// it: an export is left where it is, for whoever dropped it to delete once
// its photos are in the library.
//
// A .zip at the top of the inbox is one archive. A folder at the top is an
// export someone unpacked, and any .zip inside it is an archive of its own.
type Inbox struct {
	root *os.Root

	mu    sync.Mutex
	open  map[string]*held
	order []string
}

// Found is one archive in the inbox.
type Found struct {
	// Name is where it is in the inbox, such as takeout-001.zip or
	// Takeout 2024/takeout-002.zip, or the folder's name.
	Name string
	// Kind is zip, folder, or unsupported for an export Cull cannot read
	// without unpacking it first, such as a .tgz.
	Kind string
	// Size is the zip's size, or the total size of the folder's files.
	Size int64
	// Modified is when the zip last changed, or the newest file in the
	// folder, which is how a changed archive is told from one already read.
	Modified time.Time
}

// Folder and zip kinds, and the one Cull cannot read.
const (
	KindZip         = "zip"
	KindFolder      = "folder"
	KindUnsupported = "unsupported"
)

// keepOpen is how many archives stay open between reads. Opening a 50 GB
// zip reads its whole directory, so a task working through one zip would
// otherwise read it again for every file.
const keepOpen = 4

type held struct {
	archive  Archive
	size     int64
	modified time.Time
	users    int
	evicted  bool
}

// OpenInbox opens the inbox at dir.
func OpenInbox(dir string) (*Inbox, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Inbox{root: root, open: map[string]*held{}}, nil
}

// Close closes the inbox and every archive it holds open.
func (i *Inbox) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, h := range i.open {
		h.archive.Close()
	}
	i.open = map[string]*held{}
	i.order = nil
	return i.root.Close()
}

func unsupported(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tar")
}

func isZip(name string) bool { return strings.EqualFold(path.Ext(name), ".zip") }

// List finds every archive in the inbox, sorted by name. Hidden files and
// folders, symlinks and anything else are left out.
func (i *Inbox) List() ([]Found, error) {
	top, err := readDir(i.root, ".")
	if err != nil {
		return nil, err
	}
	found := []Found{}
	for _, entry := range top {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			folder, zips, err := i.walkFolder(name)
			if err != nil {
				return nil, err
			}
			if folder.Size > 0 {
				found = append(found, folder)
			}
			found = append(found, zips...)
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		switch {
		case isZip(name):
			found = append(found, Found{Name: name, Kind: KindZip, Size: info.Size(), Modified: info.ModTime()})
		case unsupported(name):
			found = append(found, Found{Name: name, Kind: KindUnsupported, Size: info.Size(), Modified: info.ModTime()})
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].Name < found[b].Name })
	return found, nil
}

// walkFolder adds up the photos and JSON files in a folder at the top of the
// inbox, and lists the zips in it.
func (i *Inbox) walkFolder(name string) (Found, []Found, error) {
	folder := Found{Name: name, Kind: KindFolder}
	var zips []Found
	sub, err := i.root.OpenRoot(name)
	if err != nil {
		return folder, nil, err
	}
	defer sub.Close()
	err = fs.WalkDir(sub.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		switch {
		case isZip(p):
			zips = append(zips, Found{Name: name + "/" + p, Kind: KindZip, Size: info.Size(), Modified: info.ModTime()})
		case unsupported(p):
			zips = append(zips, Found{Name: name + "/" + p, Kind: KindUnsupported, Size: info.Size(), Modified: info.ModTime()})
		case IsMedia(p) || IsSidecar(p):
			folder.Size += info.Size()
			if info.ModTime().After(folder.Modified) {
				folder.Modified = info.ModTime()
			}
		}
		return nil
	})
	return folder, zips, err
}

func readDir(root *os.Root, name string) ([]fs.DirEntry, error) {
	dir, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

// Archive opens an archive the inbox listed, for as long as release is not
// called. An archive that changed on disk since it was last opened is opened
// afresh.
func (i *Inbox) Archive(name, kind string) (archive Archive, release func(), err error) {
	if !SafePath(name) {
		return nil, nil, ErrUnsafe
	}
	info, err := i.root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	key := kind + ":" + name
	if h, ok := i.open[key]; ok && !h.evicted && (kind == KindFolder || (h.size == info.Size() && h.modified.Equal(info.ModTime()))) {
		h.users++
		i.touch(key)
		return h.archive, i.releaser(h), nil
	} else if ok {
		i.evict(key)
	}
	switch kind {
	case KindZip:
		archive, err = OpenZip(i.root, name)
	case KindFolder:
		archive, err = OpenFolder(i.root, name)
	default:
		err = errors.New("takeout: this kind of archive cannot be read")
	}
	if err != nil {
		return nil, nil, err
	}
	h := &held{archive: archive, size: info.Size(), modified: info.ModTime(), users: 1}
	// A folder is walked when it is opened, so it is not kept: it would miss
	// files added to it afterwards.
	if kind == KindZip {
		i.open[key] = h
		i.order = append(i.order, key)
		for len(i.order) > keepOpen {
			i.evict(i.order[0])
		}
	} else {
		h.evicted = true
	}
	return archive, i.releaser(h), nil
}

func (i *Inbox) touch(key string) {
	for n, k := range i.order {
		if k == key {
			i.order = append(append(i.order[:n:n], i.order[n+1:]...), key)
			return
		}
	}
}

// evict forgets an archive, closing it once nobody is reading it.
func (i *Inbox) evict(key string) {
	h, ok := i.open[key]
	if !ok {
		return
	}
	delete(i.open, key)
	for n, k := range i.order {
		if k == key {
			i.order = append(i.order[:n:n], i.order[n+1:]...)
			break
		}
	}
	h.evicted = true
	if h.users == 0 {
		h.archive.Close()
	}
}

func (i *Inbox) releaser(h *held) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			i.mu.Lock()
			defer i.mu.Unlock()
			h.users--
			if h.users == 0 && h.evicted {
				h.archive.Close()
			}
		})
	}
}

// ReadEntry opens one file of an archive in the inbox. Closing the reader
// lets the archive go.
func (i *Inbox) ReadEntry(name, kind, entry string) (io.ReadCloser, error) {
	archive, release, err := i.Archive(name, kind)
	if err != nil {
		return nil, err
	}
	r, err := archive.Open(entry)
	if err != nil {
		release()
		return nil, err
	}
	return &entryReader{ReadCloser: r, release: release}, nil
}

type entryReader struct {
	io.ReadCloser
	release func()
}

func (r *entryReader) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
}

// photoFolders are the names Google gives the Google Photos folder of an
// export, which follows the account's language.
var photoFolders = map[string]bool{
	"Google Photos": true, "Google Fotos": true, "Google Foto": true,
	"Google Zdjęcia": true, "Google Фото": true,
}

// FromPhotos reports whether a file in an export came from Google Photos. An
// export can hold other Google products too, such as Drive, whose pictures
// are not Google Photos'. A file with no Takeout folder above it was
// unpacked by hand and is taken to be a photo.
func FromPhotos(entry string) bool {
	inner := Inner(entry)
	if !strings.HasPrefix(inner, "Takeout/") {
		return true
	}
	parts := strings.SplitN(inner, "/", 3)
	return len(parts) == 3 && photoFolders[parts[1]]
}
