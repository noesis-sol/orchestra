package project

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The state a run saves is the state the next run loads, written through a temporary file that
// doesn't stay behind; saving none removes the file.
func TestStateRoundTrip(t *testing.T) {
	repo := t.TempDir()
	want := RunState{Saved: time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC), Workers: []LeftWorker{
		{Ticket: "x-1", Agent: "x-1", Tab: "w1:t3", Worktree: "/wt/x-1", Hooks: true, Question: "x-9",
			QuestionTitle: "which way?"},
		{Ticket: "x-2", Agent: "x-2", Tab: "w1:t4", Worktree: "/wt/x-2", Left: "PAUSED"},
	}}
	if err := SaveState(repo, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(repo)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded %+v, %v; want %+v", got, err, want)
	}
	if files := listing(t, filepath.Join(repo, Dir, RunName)); len(files) != 1 {
		t.Errorf("files left: %v; want only %s", files, StateName)
	}

	if err := SaveState(repo, RunState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, RunPath(StateName))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s should be removed with nothing to save: %v", StateName, err)
	}
	if got, err := LoadState(repo); err != nil || len(got.Workers) != 0 {
		t.Errorf("loaded %+v, %v; want nothing", got, err)
	}
}

// With nothing to save and no file, saving makes no folder, even in a checkout that isn't there.
func TestSavingNoStateMakesNothing(t *testing.T) {
	repo := t.TempDir()
	if err := SaveState(repo, RunState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, Dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s made: %v", Dir, err)
	}
	if err := SaveState(filepath.Join(repo, "gone"), RunState{}); err != nil {
		t.Errorf("a checkout that isn't there has no state to remove: %v", err)
	}
}

// A file left half written by a run that died is replaced; a file that isn't JSON is an error,
// not an empty state.
func TestStateFileLeftOrUnreadable(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, Dir, RunName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateName+".tmp"), []byte(`{"workers": [`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := RunState{Workers: []LeftWorker{{Ticket: "x-1", Left: "INTERRUPTED"}}}
	if err := SaveState(repo, s); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadState(repo); err != nil || !reflect.DeepEqual(got, s) {
		t.Errorf("loaded %+v, %v; want %+v", got, err, s)
	}

	if err := os.WriteFile(filepath.Join(dir, StateName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(repo); err == nil || !strings.Contains(err.Error(), RunPath(StateName)) {
		t.Errorf("an unreadable file should be an error naming it: %v", err)
	}
}

// The state is written into the main checkout's own .orchestra/run/, never through a symlink in
// place of it.
func TestStateIsNotWrittenThroughALink(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	plant(t, repo, filepath.Join(Dir, RunName), outside)
	err := SaveState(repo, RunState{Workers: []LeftWorker{{Ticket: "x-1", Left: "PAUSED"}}})
	if e := (*EscapeError)(nil); !errors.As(err, &e) || !errors.Is(err, ErrRunLink) {
		t.Errorf("want an *EscapeError for the link, got %v", err)
	}
	if files := listing(t, outside); len(files) != 0 {
		t.Errorf("written outside: %v", files)
	}
}
