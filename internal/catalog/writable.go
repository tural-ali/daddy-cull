package catalog

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

// accessWrite is W_OK for access(2).
const accessWrite = 2

// writableFolder reports whether this process may add and remove entries in
// the folder that holds rel, or, when that folder does not exist yet, in the
// nearest folder above it, which is where it would be created.
//
// A move is a link followed by an unlink, and each needs write permission on
// its folder. Checking every folder before the first file moves turns a folder
// the app may not change into a refusal up front, instead of a batch left half
// in the Bin and half where it was.
func writableFolder(root *os.Root, rel string) error {
	dir := path.Dir(rel)
	for {
		info, err := root.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("not a folder: %s", dir)
			}
			if syscall.Access(filepath.Join(root.Name(), filepath.FromSlash(dir)), accessWrite) != nil {
				return fmt.Errorf("the app is not allowed to change the folder %s; fix its permissions and try again", dir)
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) || dir == "." {
			return err
		}
		dir = path.Dir(dir)
	}
}
