package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
)

// A run's safeguards (the merge queue, the single git writer, the tickets in flight) live in its own
// process, so a second run in the same repository would race the first for the same tickets,
// worktrees and branch. Each run therefore holds a lock on .orchestra/run/orchestra.lock in the main
// checkout for as long as it runs: an advisory flock, which the kernel releases when the process
// ends, however it ends, so a crash leaves nothing stale to clean up. The file stays when the run
// ends (removing it would race a run taking it), and says which run held it last.

// LockName is the run lock's file in the main checkout's .orchestra/run/.
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

// RunLock is a run's hold on the lock of its repository's main checkout (see LockRun).
type RunLock struct {
	f    *os.File
	repo string // the main checkout, for errors
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
		return RunPath(LockName) + " doesn't say which"
	}
	return strings.Join(parts, ", ")
}

// errLocked is lockFile's error when another open file holds the lock.
var errLocked = errors.New("locked by another")

// LockRun takes the run lock of the main checkout repo for the run h describes, and writes h into
// it. Another run holding it is a *HeldError naming that run. The lock is held until Close, or until
// the process ends; keep the RunLock reachable until then, as an *os.File that isn't is closed. On a
// system without flock (Windows) it holds nothing and returns an error wrapping
// errors.ErrUnsupported.
func LockRun(repo string, h Holder) (*RunLock, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // the lock's file stays open without it
	rel := RunPath(LockName)
	f, err := root.OpenFile(rel, os.O_RDWR|os.O_CREATE, 0o644) // no O_TRUNC: the holder's details are in it
	if err != nil {
		return nil, RunError(repo, rel, err)
	}
	// RunHolder holds the lock for a moment, shared; a few tries over a tenth of a second get past
	// it, where a run holds it for hours.
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
			return nil, &HeldError{Repo: repo, Holder: held}
		}
		return nil, fmt.Errorf("cannot lock %s in %s: %w", rel, repo, err)
	}
	l := &RunLock{f: f, repo: repo}
	if err := l.Rewrite(h); err != nil {
		_ = f.Close() // releases the lock: the run doesn't start
		return nil, err
	}
	return l, nil
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
	rel := RunPath(LockName)
	if err := l.f.Truncate(0); err != nil {
		return fmt.Errorf("cannot write %s in %s: %w", rel, l.repo, err)
	}
	if _, err := l.f.WriteAt(append(b, '\n'), 0); err != nil {
		return fmt.Errorf("cannot write %s in %s: %w", rel, l.repo, err)
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

// RunHolder reports whether a run holds the lock of the main checkout repo, and what it wrote in
// it. It only looks: it makes no file, and holds the lock, shared, only for a moment.
func RunHolder(repo string) (Holder, bool, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return Holder{}, false, err
	}
	defer func() { _ = root.Close() }() // read-only
	rel := RunPath(LockName)
	f, err := root.Open(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return Holder{}, false, nil // no run has taken it here
	}
	if err != nil {
		return Holder{}, false, RunError(repo, rel, err)
	}
	defer func() { _ = f.Close() }() // read-only; releases the shared lock
	switch err := lockFile(f, false); {
	case errors.Is(err, errLocked):
		return readHolder(f), true, nil
	case err != nil:
		return Holder{}, false, fmt.Errorf("cannot lock %s in %s: %w", rel, repo, err)
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
