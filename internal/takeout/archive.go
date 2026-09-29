// Package takeout reads Google Photos exports from Google Takeout without
// unpacking them.
//
// Google stopped letting apps read a whole Google Photos library through its
// API on 31 March 2025, so a Takeout export is the only complete copy anyone
// can get. It arrives as .zip files of up to 50 GB each, holding every photo
// and video with a JSON file beside each one that says when it was taken,
// where, and what it was called. A photo and its JSON can land in different
// zips of the same export.
//
// This package lists what an export holds, reads one file out of it, and
// pairs each photo with its JSON. It never writes anything.
package takeout

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"unicode"
)

// Entry is one file in an export.
type Entry struct {
	// Path is where the file is inside the zip, or inside the folder the
	// export was unpacked into, with forward slashes.
	Path string
	// Size is the file's size in bytes, unpacked.
	Size int64
	// CRC32 is the file's checksum as the zip records it, which reading the
	// file checks it against. HasCRC is false for a file in a folder.
	CRC32  uint32
	HasCRC bool
}

// Archive is an export, or one zip of it.
type Archive interface {
	// Entries lists the files it holds, in path order. A file whose path is
	// not a plain relative path, such as one with .. in it, is left out.
	Entries() []Entry
	// Open reads one file. For a zip the reader fails at the end if the file
	// does not match its checksum.
	Open(entry string) (io.ReadCloser, error)
	Close() error
}

// ErrUnsafe is a path that could leave the archive or the folder.
var ErrUnsafe = errors.New("takeout: unsafe path")

// SafePath reports whether p is a plain relative path with forward slashes:
// no .., no leading slash, no backslash and no empty part.
func SafePath(p string) bool {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, "\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// SafeName reports whether a file's name can be used as it is for a file in
// the library: no slash, not hidden, and no control characters.
func SafeName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\") || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return false
		}
	}
	return true
}

// Zip is one .zip file of an export.
type Zip struct {
	file    *os.File
	reader  *zip.Reader
	entries []Entry
	byPath  map[string]*zip.File
}

// OpenZip opens a zip inside root, reading only its central directory.
func OpenZip(root *os.Root, name string) (*Zip, error) {
	if !SafePath(name) {
		return nil, ErrUnsafe
	}
	if err := noSymlinks(root, name); err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("takeout: %s is not a regular file", name)
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		file.Close()
		return nil, err
	}
	z := &Zip{file: file, reader: reader, byPath: map[string]*zip.File{}}
	for _, f := range reader.File {
		if f.FileInfo().IsDir() || !SafePath(f.Name) || f.UncompressedSize64 > 1<<40 {
			continue
		}
		if _, seen := z.byPath[f.Name]; seen {
			continue
		}
		z.byPath[f.Name] = f
		z.entries = append(z.entries, Entry{Path: f.Name, Size: int64(f.UncompressedSize64), CRC32: f.CRC32, HasCRC: true})
	}
	sort.Slice(z.entries, func(i, j int) bool { return z.entries[i].Path < z.entries[j].Path })
	return z, nil
}

func (z *Zip) Entries() []Entry { return z.entries }

func (z *Zip) Open(entry string) (io.ReadCloser, error) {
	f, ok := z.byPath[entry]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return f.Open()
}

func (z *Zip) Close() error { return z.file.Close() }

// Folder is an export someone unpacked into a folder.
type Folder struct {
	root    *os.Root
	entries []Entry
}

// OpenFolder lists the regular files under the folder name inside root,
// leaving out hidden files and folders and anything reached by a symlink.
func OpenFolder(root *os.Root, name string) (*Folder, error) {
	if !SafePath(name) {
		return nil, ErrUnsafe
	}
	if err := noSymlinks(root, name); err != nil {
		return nil, err
	}
	sub, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	folder := &Folder{root: sub}
	err = fs.WalkDir(sub.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
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
		folder.entries = append(folder.entries, Entry{Path: p, Size: info.Size()})
		return nil
	})
	if err != nil {
		sub.Close()
		return nil, err
	}
	return folder, nil
}

func (f *Folder) Entries() []Entry { return f.entries }

func (f *Folder) Open(entry string) (io.ReadCloser, error) {
	if !SafePath(entry) {
		return nil, ErrUnsafe
	}
	if err := noSymlinks(f.root, entry); err != nil {
		return nil, err
	}
	file, err := f.root.Open(entry)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err == nil {
			err = fmt.Errorf("takeout: %s is not a regular file", entry)
		}
		return nil, err
	}
	return file, nil
}

func (f *Folder) Close() error { return f.root.Close() }

// noSymlinks refuses a path any part of which is a symlink, so a link left
// in the inbox can never point Cull at something else.
func noSymlinks(root *os.Root, p string) error {
	parts := strings.Split(p, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("takeout: symlink refused: %s", p)
		}
	}
	return nil
}

// Inner is where a file is within the export, from its Takeout folder on,
// such as Takeout/Google Photos/Photos from 2019/IMG_1234.JPG. The zips of
// one export share it, which is how a photo in one zip finds its JSON in
// another. A path with no Takeout folder is its own inner path.
func Inner(entry string) string {
	parts := strings.Split(entry, "/")
	for i, part := range parts {
		if part == "Takeout" {
			return strings.Join(parts[i:], "/")
		}
	}
	return entry
}
