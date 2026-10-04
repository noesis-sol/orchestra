package project

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The files orchestra rewrites (the settings, git's info/exclude, .gitattributes, the run state)
// are written to a temporary file beside them first, then renamed over them. os.WriteFile
// truncates a file before it writes: a full disk, an I/O error or a crash in between would leave
// it empty or half written, and the user's entries in info/exclude, which git doesn't keep, lost
// for good.

// writeFile writes b to the file path as os.WriteFile does, but through writeRoot: a reader finds
// the old file or the new one, never half of either, and a write that fails leaves the file as it
// was. A symlink at path is followed, as os.WriteFile follows it: the file it leads to is
// replaced, and the link kept.
func writeFile(path string, b []byte, perm fs.FileMode) error {
	if to, err := filepath.EvalSymlinks(path); err == nil {
		path = to
	} // else there is no file there yet, nor one a link leads to: writeRoot makes it
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // the file is written and renamed, or not, by then
	if err := writeRoot(root, filepath.Base(path), b, perm); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// writeRoot writes b to the file name in root: to a temporary file in the same folder first,
// synced to disk, then renamed over it, so the file is the old one or the new one, never half of
// either, and a write that fails leaves it as it was and removes the temporary file. The file
// keeps its mode; a new one gets perm, less the umask. A symlink at name is replaced rather than
// written through, and every step stays inside root.
func writeRoot(root *os.Root, name string, b []byte, perm fs.FileMode) error {
	var r [8]byte
	_, _ = rand.Read(r[:]) // crypto/rand's Read never fails
	// A name of its own, so that two writers side by side (runs in two worktrees share info/exclude)
	// never write into each other's file, and one a crash left behind is never in the way.
	tmp := name + "." + hex.EncodeToString(r[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	// The old file's mode, as os.WriteFile keeps it. A file that isn't there, or can't be looked at,
	// has none to keep: the new one gets perm, and the rename says what is wrong.
	if fi, lerr := root.Lstat(name); lerr == nil && fi.Mode().IsRegular() {
		err = f.Chmod(fi.Mode().Perm())
	}
	if err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync() // on disk before the rename makes it the file
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		_ = root.Remove(tmp) // the file stays as it was; err says why
		return err
	}
	return nil
}
