package project

import (
	"errors"
	"fmt"
	"io"
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
// A symlink in place of .orchestra or .orchestra/run is refused too, wherever it points: git's
// ignore rules for the folder (/.orchestra/run/ in info/exclude, run/ in .orchestra/.gitignore) match
// a directory only, not a link, so files written through one inside the worktree would show as
// untracked where it points, and a worker's git add -A would commit them, secrets and all. For the
// same reason a run file that is a symlink inside the worktree is not written through: WriteRun
// removes the link and writes the file in its place.
//
// The files in folders the user controls (the settings, the worker prompt, the log, the reports,
// git's info/exclude) are reached by their paths, through any symlink (writeFile follows one too):
// no worker changes those, and the user may well keep them behind a symlink of their own.

// RunPath is the path of the file name in .orchestra/run/, relative to the checkout: the name to
// give an os.Root opened by OpenRun.
func RunPath(name string) string {
	return filepath.Join(Dir, RunName, name)
}

// The folders in the main checkout's .orchestra/run/ where a Claude session with no ticket, and so no
// worktree of its own, has its hooks write their records, apart from a ticket's and each other's.
const (
	ProbeRun     = "probe-session"     // the environment probe's worker
	InterviewRun = "interview-session" // the claude of a breakdown with the user: a feature's, or a spec's refinement
)

// OpenRun opens an os.Root at the checkout dir and makes .orchestra/run/ in it, for the caller to
// reach the files there by their RunPath, and to close. A .orchestra or .orchestra/run that is a
// symlink, wherever it points, is an *EscapeError whose Err is ErrRunLink. Orchestra writes there
// before the worker starts, so nothing changes the folders between this check and those writes.
func OpenRun(dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	for _, rel := range []string{Dir, filepath.Join(Dir, RunName)} {
		// A path that isn't there is made below, as a folder; one that can't be looked at fails there too.
		if fi, err := root.Lstat(rel); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close() // read-only so far: nothing to lose
			return nil, &EscapeError{Dir: dir, Path: rel, Err: ErrRunLink}
		}
	}
	if err := root.MkdirAll(filepath.Join(Dir, RunName), 0o755); err != nil {
		_ = root.Close() // read-only so far: nothing to lose
		return nil, RunError(dir, filepath.Join(Dir, RunName), err)
	}
	return root, nil
}

// WriteRun writes b to the file rel in root, an os.Root at the checkout dir, with mode perm. An earlier
// file is removed first (see RemoveRun): WriteFile would keep its mode, and would write through it if
// it were a symlink. One that stays inside dir would get orchestra's file where it points, as an
// untracked file that git's ignore rules for .orchestra/run/ don't cover, for a worker's git add -A to
// commit; WriteRun puts the file in place of the link instead.
func WriteRun(root *os.Root, dir, rel string, b []byte, perm fs.FileMode) error {
	if err := RemoveRun(root, dir, rel); err != nil {
		return err
	}
	if err := root.WriteFile(rel, b, perm); err != nil {
		return RunError(dir, rel, err)
	}
	return nil
}

// WriteRunFrom writes what r reads to the file rel in root, as WriteRun writes b: for a file too long
// to hold in memory first, such as a check's whole output.
func WriteRunFrom(root *os.Root, dir, rel string, r io.Reader, perm fs.FileMode) error {
	f, err := CreateRun(root, dir, rel, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return RunError(dir, rel, err)
	}
	return nil
}

// CreateRun creates the file rel in root, an os.Root at the checkout dir, with mode perm, for the caller
// to write and close: an earlier file is removed first, as WriteRun removes it.
func CreateRun(root *os.Root, dir, rel string, perm fs.FileMode) (*os.File, error) {
	if err := RemoveRun(root, dir, rel); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return nil, RunError(dir, rel, err)
	}
	return f, nil
}

// RemoveRun removes the file rel from root, an os.Root at the checkout dir, if it is there. Removing a
// symlink would not touch what it points to, but one that leads out of dir was put there to send
// orchestra's file elsewhere: it is refused as an *EscapeError, like a write through it. One that
// leads to a file inside dir is removed, even by a way the os.Root won't follow (an absolute target,
// or one that climbs out of dir and back in): the os.Root refuses those as it refuses an escape.
func RemoveRun(root *os.Root, dir, rel string) error {
	if _, err := root.Stat(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fi, lerr := root.Lstat(rel)
		if lerr != nil || fi.Mode()&fs.ModeSymlink == 0 {
			return RunError(dir, rel, err)
		}
		if out := escapes(dir, rel); out != "" {
			return &EscapeError{Dir: dir, Path: out, Err: err}
		}
	}
	if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RunError(dir, rel, err)
	}
	return nil
}

// EscapeError is a symlink in a checkout that orchestra won't follow to the run files: one that an
// os.Root opened there refused because it leads out of the checkout, or one in place of .orchestra
// or .orchestra/run, wherever it points (see OpenRun).
type EscapeError struct {
	Dir  string // the checkout
	Path string // relative to Dir: the symlink, e.g. .orchestra/run
	Err  error  // what the os.Root said, or ErrRunLink
}

// ErrRunLink is the Err of an *EscapeError for a symlink in place of .orchestra or .orchestra/run.
var ErrRunLink = errors.New("git's ignore rules for .orchestra/run/ don't follow a symlink")

func (e *EscapeError) Error() string {
	if errors.Is(e.Err, ErrRunLink) {
		return fmt.Sprintf("%s in %s is a symlink (%v)", e.Path, e.Dir, e.Err)
	}
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
