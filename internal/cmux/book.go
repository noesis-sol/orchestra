package cmux

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// cmux keeps nothing of a program's on a workspace or surface, as tmux keeps a pane's options: what
// orchestra needs to find a worker again (the label it opened the workspace with, the agent each
// terminal holds, its kind and records folder) it keeps in its book, a file in the main checkout's
// .orchestra/run/, so that a later run finds it there too. Every workspace and surface is in it by
// its UUID, never by a short ref such as workspace:2, which cmux numbered afresh on each start
// before 0.65.0 (manaflow-ai/cmux#13633).

// BookName is the book's file in .orchestra/run/.
const BookName = "cmux.json"

// book is what orchestra keeps of the workspaces and terminals it opened in cmux.
type book struct {
	Workspaces map[string]workspaceEntry `json:"workspaces,omitempty"` // by UUID
	Surfaces   map[string]surfaceEntry   `json:"surfaces,omitempty"`   // by UUID
}

// workspaceEntry is a workspace orchestra opened: only such a workspace, still under its label, is
// ever closed.
type workspaceEntry struct {
	Label string `json:"label"`
}

// surfaceEntry is a terminal orchestra opened, as a workspace's first or split off another.
type surfaceEntry struct {
	Workspace string `json:"workspace"`
	Dir       string `json:"dir,omitempty"`     // the folder it started in
	Agent     string `json:"agent,omitempty"`   // the agent's name, "" until it was given one
	Kind      string `json:"kind,omitempty"`    // the kind launched there, such as claude; "" for none
	Records   string `json:"records,omitempty"` // where its hooks write their records; "" without hooks
	Split     bool   `json:"split,omitempty"`   // split off another terminal (SplitPane), not a workspace's own
}

// bookMu keeps one goroutine at a time reading and writing a book: a run's workers start, and are
// named, side by side. Only one run at a time works on a repository (project.LockRun).
var bookMu sync.Mutex

// read returns the book at path, empty when there is none yet.
func readBook(path string) (book, error) {
	var b book
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return b, nil
	case err != nil:
		return b, fmt.Errorf("cannot read orchestra's book of cmux workspaces: %w", err)
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("cannot read orchestra's book of cmux workspaces, %s: %w", path, err)
	}
	return b, nil
}

// view returns a copy of the book.
func (t Terminal) view() (book, error) {
	if t.Book == "" {
		return book{}, errors.New("no book of cmux workspaces to read")
	}
	bookMu.Lock()
	defer bookMu.Unlock()
	return readBook(t.Book)
}

// edit changes the book with change, and writes it back unless change fails. The file is replaced
// whole, so a reader never finds half of it.
func (t Terminal) edit(change func(*book) error) error {
	if t.Book == "" {
		return errors.New("no book of cmux workspaces to keep")
	}
	bookMu.Lock()
	defer bookMu.Unlock()
	b, err := readBook(t.Book)
	if err != nil {
		return err
	}
	b.Workspaces, b.Surfaces = maps.Clone(b.Workspaces), maps.Clone(b.Surfaces)
	if b.Workspaces == nil {
		b.Workspaces = map[string]workspaceEntry{}
	}
	if b.Surfaces == nil {
		b.Surfaces = map[string]surfaceEntry{}
	}
	if err := change(&b); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.Book), 0o755); err != nil {
		return fmt.Errorf("cannot keep orchestra's book of cmux workspaces: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(t.Book), "."+BookName+".*")
	if err != nil {
		return fmt.Errorf("cannot keep orchestra's book of cmux workspaces: %w", err)
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name()) // a half-written file is no use
		return fmt.Errorf("cannot keep orchestra's book of cmux workspaces: %w", err)
	}
	if err := os.Rename(tmp.Name(), t.Book); err != nil {
		_ = os.Remove(tmp.Name()) // the book stays as it was
		return fmt.Errorf("cannot keep orchestra's book of cmux workspaces: %w", err)
	}
	return nil
}
