package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// A worktree's .orchestra/run/ is the worker's to change: a worker is an agent reading ticket text
// orchestra can't fully trust, and the worktree is its own. Had it replaced .orchestra/run, or a file
// in it, with a symlink, orchestra would write, remove or read wherever that pointed: the MCP
// servers' secrets could land in another repository and be committed there. So every file orchestra
// touches there (and in the probe's .orchestra/run/ in the main checkout) is reached through an
// os.Root opened at the checkout, which refuses any path that leads out of it, symlinks included,
// with no gap between checking a path and using it.
//
// The files in folders the user controls (the settings, the worker prompt, the log, the reports,
// git's info/exclude) stay on the plain os calls: no worker changes those, and the user may well
// keep them behind a symlink of their own.

// RunPath is the path of the file name in .orchestra/run/, relative to the checkout: the name to
// give an os.Root opened by OpenRun.
func RunPath(name string) string {
	return filepath.Join(Dir, RunName, name)
}

// OpenRun opens an os.Root at the checkout dir and makes .orchestra/run/ in it, for the caller to
// reach the files there by their RunPath, and to close. A .orchestra/run that leads out of dir is
// an *EscapeError.
func OpenRun(dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := root.MkdirAll(filepath.Join(Dir, RunName), 0o755); err != nil {
		_ = root.Close() // read-only so far: nothing to lose
		return nil, RunError(dir, filepath.Join(Dir, RunName), err)
	}
	return root, nil
}

// RemoveRun removes the file rel from root, an os.Root at the checkout dir, if it is there. Removing a
// symlink would not touch what it points to, but one that leads out of dir was put there to send
// orchestra's file elsewhere: it is refused as an *EscapeError, like a write through it.
func RemoveRun(root *os.Root, dir, rel string) error {
	if _, err := root.Stat(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RunError(dir, rel, err)
	}
	if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RunError(dir, rel, err)
	}
	return nil
}

// EscapeError is a path in a checkout that an os.Root opened there refused because it leads out
// of the checkout, through a symlink.
type EscapeError struct {
	Dir  string // the checkout
	Path string // relative to Dir: the part of the path that leads out, e.g. .orchestra/run
	Err  error  // what the os.Root said
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%s in %s points outside it (%v)", e.Path, e.Dir, e.Err)
}

func (e *EscapeError) Unwrap() error { return e.Err }

// RunError is err, which an os.Root at the checkout dir returned for rel, as an *EscapeError when
// part of rel is a symlink that leads out of dir, and as it is otherwise. os doesn't export the
// error for an escape, so the path is looked at again; that only names the cause: the os.Root had
// already refused it, and nothing is done through what this finds.
func RunError(dir, rel string, err error) error {
	if err == nil {
		return nil
	}
	if out := escapes(dir, rel); out != "" {
		return &EscapeError{Dir: dir, Path: out, Err: err}
	}
	return err
}

// escapes returns the first part of rel, a path in dir, that is a symlink leading out of dir, or
// that can't be followed; "" when there is none.
func escapes(dir, rel string) string {
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	var parts []string
	for p := filepath.Clean(rel); p != "."; p = filepath.Dir(p) {
		parts = append([]string{p}, parts...)
	}
	for _, p := range parts {
		fi, err := os.Lstat(filepath.Join(dir, p))
		if errors.Is(err, fs.ErrNotExist) {
			return ""
		}
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		to, err := filepath.EvalSymlinks(filepath.Join(dir, p))
		if err != nil {
			return p // it leads nowhere, which an os.Root can't follow either
		}
		if r, err := filepath.Rel(base, to); err != nil || !filepath.IsLocal(r) {
			return p
		}
	}
	return ""
}
