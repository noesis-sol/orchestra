package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// A run leaves workers behind in their tabs: on tickets waiting on a question, and on tickets it
// left running when it stopped. It writes them to .orchestra/run/state.json in the main checkout as
// it ends, while it holds the run lock, and the next run, holding the lock in turn, reads them and
// carries on with them: it merges a ticket closed meanwhile, adopts a worker at work again, and
// tells an idle one its question is answered. So only one run ever reads or writes the file.

// StateName is the file in the main checkout's .orchestra/run/ that holds the workers the last run
// left behind.
const StateName = "state.json"

// LeftWorker is a worker a run left behind in its tab, as the next run reads it.
type LeftWorker struct {
	Ticket   string `json:"ticket"`
	Agent    string `json:"agent"` // its Herdr name
	Tab      string `json:"tab"`
	Worktree string `json:"worktree"`
	Hooks    bool   `json:"hooks,omitempty"` // it reports through hooks
	// For a ticket waiting on a question: the question's ID and title.
	Question      string `json:"question,omitempty"`
	QuestionTitle string `json:"question_title,omitempty"`
	// For a ticket left running when the run stopped: why it stopped, as its final line begins
	// (PAUSED, BLOCKED, TICKET_LIMIT, INTERRUPTED, …).
	Left string `json:"left,omitempty"`
}

// RunState is what a run leaves for the next one in StateName.
type RunState struct {
	Saved   time.Time    `json:"saved,omitzero"`
	Workers []LeftWorker `json:"workers"`
}

// SaveState writes s to StateName in the main checkout repo, reached through OpenRun: to a
// temporary file first, then renamed over it, so a reader finds the old state or the new one and
// never half of either. With no workers in s it removes the file, if there is one, and makes no
// folder.
func SaveState(repo string, s RunState) error {
	rel := RunPath(StateName)
	if len(s.Workers) == 0 {
		root, err := os.OpenRoot(repo)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }() // the removal is done or failed by then
		if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return RunError(repo, rel, err)
		}
		return nil
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	root, err := OpenRun(repo)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // the file is written and renamed, or not, by then
	tmp := rel + ".tmp"
	if err := RemoveRun(root, repo, tmp); err != nil { // a run that died writing it left it
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return RunError(repo, tmp, err)
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync() // on disk before the rename makes it the state
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, rel)
	}
	if err != nil {
		_ = root.Remove(tmp) // the state stays as it was; the error says why
		return fmt.Errorf("cannot write %s in %s: %w", rel, repo, RunError(repo, rel, err))
	}
	return nil
}

// LoadState reads StateName in the main checkout repo: the zero RunState when there is none. A
// file that can't be read or decoded is an error.
func LoadState(repo string) (RunState, error) {
	rel := RunPath(StateName)
	root, err := os.OpenRoot(repo)
	if errors.Is(err, fs.ErrNotExist) {
		return RunState{}, nil
	}
	if err != nil {
		return RunState{}, err
	}
	defer func() { _ = root.Close() }() // read-only
	b, err := root.ReadFile(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return RunState{}, nil
	}
	if err != nil {
		return RunState{}, RunError(repo, rel, err)
	}
	var s RunState
	if err := json.Unmarshal(b, &s); err != nil {
		return RunState{}, fmt.Errorf("%s in %s: %w", rel, repo, err)
	}
	return s, nil
}
