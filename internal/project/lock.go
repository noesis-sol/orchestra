package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/git"
)

// A run's safeguards (the merge queue, the single git writer, the tickets in flight) live in its own
// process, so a second run in the same repository would race the first for the same tickets,
// worktrees and branch. Each run therefore holds a lock on orchestra.lock in the repository's git
// directory, the one its worktrees share (the main checkout's .git), for as long as it runs: an
// advisory flock, which the kernel releases when the process ends, however it ends, so a crash leaves
// nothing stale to clean up. The file stays when the run ends (removing it would race a run taking
// it), and says which run held it last.
//
// An flock holds the file that was opened, not its path: had the file been removed during a run, a
// second run would make another at the path and lock that. So the lock is kept where nothing removes
// it: not in .orchestra/run/, which git ignores and git clean -fdX or -fdx removes, but in the git
// directory, which git clean never touches.

// LockName is the run lock's file in the git directory a repository's worktrees share.
const LockName = "orchestra.lock"

// Holder is what a run writes into the lock it holds: for a second run refused the lock, and for
// people and agents finding out whether a run is going and what it runs.
type Holder struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started,omitzero"`
	Version string    `json:"version,omitempty"`
	Branch  string    `json:"branch,omitempty"`  // the main checkout's, where finished tickets land
	Ticket  string    `json:"ticket,omitempty"`  // --ticket: the run is scoped to it
	Feature string    `json:"feature,omitempty"` // --feature: the request
	Pane    string    `json:"pane,omitempty"`    // the Herdr pane it runs in (HERDR_PANE_ID)
}

// RunLock is a run's hold on the lock of its repository (see LockRun).
type RunLock struct {
	f    *os.File
	path string // the lock's file, for errors
}

// HeldError is LockRun's error when another run holds the lock.
type HeldError struct {
	Repo   string // the main checkout
	Holder Holder // what the holder wrote in the lock; zero when it says nothing readable
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("orchestra is already running in %s (%s)", e.Repo, e.Holder.describe(time.Now()))
}

// describe is h for a message: its PID, start time, branch, scope and pane, those it has.
func (h Holder) describe(now time.Time) string {
	var parts []string
	if h.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", h.PID))
	}
	if !h.Started.IsZero() {
		at := h.Started.Local()
		if y, m, d := at.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
			parts = append(parts, "since "+at.Format("15:04"))
		} else {
			parts = append(parts, "since "+at.Format("Jan 2 15:04"))
		}
	}
	if h.Branch != "" {
		parts = append(parts, h.Branch)
	}
	switch {
	case h.Ticket != "":
		parts = append(parts, "--ticket "+h.Ticket)
	case h.Feature != "":
		request := []rune(strings.Join(strings.Fields(h.Feature), " "))
		if len(request) > 40 {
			request = append(request[:39], '…')
		}
		parts = append(parts, fmt.Sprintf("--feature %q", string(request)))
	}
	if h.Pane != "" {
		parts = append(parts, "pane "+h.Pane)
	}
	if len(parts) == 0 {
		return LockName + " doesn't say which"
	}
	return strings.Join(parts, ", ")
}

// errLocked is lockFile's error when another open file holds the lock.
var errLocked = errors.New("locked by another")

// errReplaced is lockOpened's error when the lock's path no longer names the file it locked.
var errReplaced = errors.New("removed or replaced as it was locked")

// LockPath returns the path of the run lock of the repository whose main checkout is repo: LockName
// in the git directory its worktrees share (git rev-parse --git-common-dir).
func LockPath(ctx context.Context, repo string) (string, error) {
	common, err := git.Git{}.CommonDir(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("cannot find the git directory of %s: %w", repo, err)
	}
	return filepath.Join(common, LockName), nil
}

// LockRun takes the run lock of the main checkout repo for the run h describes, and writes h into
// it. Another run holding it is a *HeldError naming that run. The lock is held until Close, or until
// the process ends; keep the RunLock reachable until then, as an *os.File that isn't is closed. On a
// system without flock (Windows) it holds nothing and returns an error wrapping
// errors.ErrUnsupported.
func LockRun(ctx context.Context, repo string, h Holder) (*RunLock, error) {
	path, err := LockPath(ctx, repo)
	if err != nil {
		return nil, err
	}
	// The file is removed or replaced between the open and the lock only by hand, so a few tries
	// are plenty.
	for try := 1; ; try++ {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644) // no O_TRUNC: the holder's details are in it
		if err != nil {
			return nil, err
		}
		switch err := lockOpened(repo, path, f); {
		case errors.Is(err, errReplaced) && try < 3:
			continue
		case errors.Is(err, errReplaced):
			return nil, fmt.Errorf("cannot lock %s: %w, %d times", path, err, try)
		case err != nil:
			return nil, err
		}
		l := &RunLock{f: f, path: path}
		if err := l.Rewrite(h); err != nil {
			_ = f.Close() // releases the lock: the run doesn't start
			return nil, err
		}
		return l, nil
	}
}

// lockOpened takes the lock's file f, opened at path, for a run, and closes f when it can't: a
// *HeldError for the main checkout repo when another run holds it, and errReplaced when path names
// another file once f is locked. An flock holds the file opened: a run that opens path after the
// file was removed or replaced, as this one waited for the lock, locks another file, and both would
// run.
func lockOpened(repo, path string, f *os.File) error {
	// RunHolder holds the lock for a moment, shared; a few tries over a tenth of a second get past
	// it, where a run holds it for hours.
	var err error
	for try := 1; ; try++ {
		if err = lockFile(f, true); !errors.Is(err, errLocked) || try == 5 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		held := readHolder(f)
		_ = f.Close() // only read
		if errors.Is(err, errLocked) {
			return &HeldError{Repo: repo, Holder: held}
		}
		return fmt.Errorf("cannot lock %s: %w", path, err)
	}
	at, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		_ = f.Close() // releases a lock no other run would see
		return errReplaced
	}
	var locked fs.FileInfo
	if err == nil {
		locked, err = f.Stat()
	}
	if err != nil {
		_ = f.Close() // releases the lock: the run doesn't start
		return fmt.Errorf("cannot lock %s: %w", path, err)
	}
	if !os.SameFile(at, locked) {
		_ = f.Close() // releases a lock no other run would see
		return errReplaced
	}
	return nil
}

// Rewrite replaces what the lock says of its run with h, as when the run learns its feature after
// taking the lock, at the question of what to work on. A nil RunLock holds nothing to write in.
func (l *RunLock) Rewrite(h Holder) error {
	if l == nil {
		return nil
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	// Emptied first, so that RunHolder or a run refused, reading meanwhile without the lock, reads
	// nothing rather than old details mixed with new.
	if err := l.f.Truncate(0); err != nil {
		return fmt.Errorf("cannot write %s: %w", l.path, err)
	}
	if _, err := l.f.WriteAt(append(b, '\n'), 0); err != nil {
		return fmt.Errorf("cannot write %s: %w", l.path, err)
	}
	return nil
}

// Close releases the lock; a nil RunLock holds nothing. The file stays.
func (l *RunLock) Close() error {
	if l == nil {
		return nil
	}
	return l.f.Close()
}

// RunHolder reports whether a run holds the lock of the repository repo is a checkout of, and what
// it wrote in it. It only looks: it makes no file, and holds the lock, shared, only for a moment.
func RunHolder(ctx context.Context, repo string) (Holder, bool, error) {
	path, err := LockPath(ctx, repo)
	if err != nil {
		return Holder{}, false, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Holder{}, false, nil // no run has taken it here
	}
	if err != nil {
		return Holder{}, false, err
	}
	defer func() { _ = f.Close() }() // read-only; releases the shared lock
	switch err := lockFile(f, false); {
	case errors.Is(err, errLocked):
		return readHolder(f), true, nil
	case err != nil:
		return Holder{}, false, fmt.Errorf("cannot lock %s: %w", path, err)
	}
	return Holder{}, false, nil
}

// readHolder reads the details in the lock's file f; zero when it holds none readable: none
// written yet, or by a version of orchestra that wrote something else.
func readHolder(f *os.File) Holder {
	var h Holder
	if err := json.NewDecoder(io.NewSectionReader(f, 0, 1<<16)).Decode(&h); err != nil {
		return Holder{}
	}
	return h
}
