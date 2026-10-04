//go:build unix

package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The lock is in the git directory, out of reach of what empties .orchestra/run/ during a run: a
// second run is still refused, RunHolder still sees the first run, from a linked worktree too, and the
// first run still writes its details where the others read them.
func TestRunLockOutlivesTheRunFolder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		empty func(t *testing.T, repo string, git func(dir string, args ...string) string)
	}{
		{"git clean -fdX", func(t *testing.T, repo string, git func(dir string, args ...string) string) {
			if err := EnsureRunExcluded(t.Context(), repo); err != nil {
				t.Fatal(err)
			}
			git(repo, "clean", "-q", "-fdX")
		}},
		{"git clean -fdx", func(_ *testing.T, repo string, git func(dir string, args ...string) string) {
			git(repo, "clean", "-q", "-fdx")
		}},
		{"rm -rf .orchestra/run", func(t *testing.T, repo string, _ func(dir string, args ...string) string) {
			if err := os.RemoveAll(filepath.Join(repo, Dir, RunName)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, git := gitRepo(t)
			first := Holder{PID: 44497, Branch: "main"}
			l := lockedBy(t, repo, first)
			root, err := OpenRun(repo)
			if err != nil {
				t.Fatal(err)
			}
			err = WriteRun(root, repo, RunPath(StateName), []byte("{}\n"), 0o644)
			_ = root.Close() // written or not, nothing more to do through it
			if err != nil {
				t.Fatal(err)
			}

			tc.empty(t, repo, git)
			if _, err := os.Stat(filepath.Join(repo, Dir, RunName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s is still there: %v", RunPath(""), err)
			}

			_, err = LockRun(t.Context(), repo, Holder{PID: 2})
			var held *HeldError
			if !errors.As(err, &held) || !sameHolder(held.Holder, first) {
				t.Errorf("second LockRun: %v, want a *HeldError naming %+v", err, first)
			}
			if h, ok, err := RunHolder(t.Context(), repo); err != nil || !ok || !sameHolder(h, first) {
				t.Errorf("RunHolder: %+v %v %v, want %+v", h, ok, err, first)
			}
			wt := filepath.Join(t.TempDir(), "wt")
			git(repo, "worktree", "add", "-q", "-b", "wt/x-1", wt)
			if h, ok, err := RunHolder(t.Context(), wt); err != nil || !ok || !sameHolder(h, first) {
				t.Errorf("RunHolder in a linked worktree: %+v %v %v, want %+v", h, ok, err, first)
			}
			feature := first
			feature.Feature = "Add a --json flag"
			if err := l.Rewrite(feature); err != nil {
				t.Fatal(err)
			}
			if h, ok, err := RunHolder(t.Context(), repo); err != nil || !ok || !sameHolder(h, feature) {
				t.Errorf("RunHolder after Rewrite: %+v %v %v, want %+v", h, ok, err, feature)
			}
		})
	}
}

// A run that opened the lock's file just before it was removed doesn't run beside the run that made
// and locked a new one: holding its flock, it finds that the path names another file, and its next try
// finds that run.
func TestRunLockHoldsTheFileItsPathNames(t *testing.T) {
	repo, _ := gitRepo(t)
	path, err := LockPath(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, ".git", LockName); !sameFile(t, filepath.Dir(path), filepath.Dir(want)) ||
		filepath.Base(path) != LockName {
		t.Fatalf("LockPath: %s, want %s", path, want)
	}

	// Opened, then removed before it is locked.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := lockOpened(repo, path, f); !errors.Is(err, errReplaced) {
		t.Fatalf("lockOpened on a removed file: %v, want errReplaced", err)
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the removed file was left open: %v", err)
	}

	// Opened, then replaced by another run's before it is locked.
	f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second := Holder{PID: 2, Branch: "main"}
	lockedBy(t, repo, second)
	if err := lockOpened(repo, path, f); !errors.Is(err, errReplaced) {
		t.Fatalf("lockOpened on a replaced file: %v, want errReplaced", err)
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the replaced file was left open: %v", err)
	}
	_, err = LockRun(t.Context(), repo, Holder{PID: 1})
	var held *HeldError
	if !errors.As(err, &held) || !sameHolder(held.Holder, second) {
		t.Errorf("LockRun: %v, want a *HeldError naming %+v", err, second)
	}
}

// sameFile reports whether a and b name the same file: a temporary folder may be reached through a
// symlink (/var and /private/var on macOS), and git names it by its real path.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}
