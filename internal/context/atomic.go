package neycontext

import (
	"io/fs"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path by creating a temp file in the same
// directory, writing and chmod'ing it, then renaming it over path — so
// readers never observe a partial write. It cleans up the temp file on any
// failure; on success there is nothing left behind but the final file.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}
