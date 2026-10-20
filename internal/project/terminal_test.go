package project

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// A run starts from a Herdr pane, or from any terminal with tmux: init's last step says which, as
// the machine has them.
func TestStartRunSaysWhereToStartFrom(t *testing.T) {
	for _, tc := range []struct {
		installed []string
		want      string
	}{
		{[]string{"herdr"}, "From a Herdr pane, on the branch"},
		{[]string{"tmux"}, "From any terminal (workers run in tmux), on the branch"},
		{[]string{"herdr", "tmux"}, "From a Herdr pane, or any terminal with tmux, on the branch"},
	} {
		got := startRun(func(cmd string) bool { return slices.Contains(tc.installed, cmd) })
		if !strings.HasPrefix(got, tc.want) || !strings.HasSuffix(got, ":\norchestra") {
			t.Errorf("with %v: %q, want it to start %q", tc.installed, got, tc.want)
		}
	}
}

// terminals are the choices a run gives ResolveTerminal (cmd/orchestra's terminalChoices).
var terminals = []string{TerminalHerdr, TerminalTmux, TerminalCmux, TerminalAuto}

func TestResolveTerminal(t *testing.T) {
	for _, tc := range []struct {
		flag, setting, want string
		err                 bool
	}{
		{"", "", TerminalAuto, false},
		{"", "tmux", TerminalTmux, false},
		{"herdr", "tmux", TerminalHerdr, false},
		{"auto", "herdr", TerminalAuto, false},
		{"screen", "", "", true},
		{"", "screen", "", true},
	} {
		got, err := ResolveTerminal(Given[string]{Value: tc.flag, Name: "--terminal"}, Settings{Terminal: tc.setting}, terminals)
		if got != tc.want || (err != nil) != tc.err {
			t.Errorf("flag %q, setting %q: %q %v, want %q (an error: %v)", tc.flag, tc.setting, got, err, tc.want, tc.err)
		}
	}
}

// A worker left behind before workers could run in tmux has no terminal recorded: it is Herdr's.
func TestSameTerminal(t *testing.T) {
	if !SameTerminal("", TerminalHerdr) || !SameTerminal(TerminalTmux, TerminalTmux) || SameTerminal("", TerminalTmux) {
		t.Error("SameTerminal takes \"\" for anything but Herdr, or tells two the same apart")
	}
}

// The terminal each worker runs in is saved with it, and a worker saved before there was a choice,
// with none, loads as it was: Herdr's.
func TestStateRoundTripWithTheTerminal(t *testing.T) {
	repo := t.TempDir()
	want := RunState{Saved: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC), Workers: []LeftWorker{
		{Ticket: "x-1", Agent: "x-1", Terminal: TerminalTmux, Tab: "@4", Pane: "%9", Worktree: "/wt/x-1", Hooks: true},
		{Ticket: "x-2", Agent: "x-2", Terminal: TerminalHerdr, Tab: "w1:t4", Worktree: "/wt/x-2", Left: "PAUSED"},
	}}
	if err := SaveState(repo, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(repo)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded %+v, %v; want %+v", got, err, want)
	}
	if b, err := os.ReadFile(filepath.Join(repo, RunPath(StateName))); err != nil || !strings.Contains(string(b), `"terminal": "tmux"`) {
		t.Errorf("%s lacks the terminal: %s %v", StateName, b, err)
	}

	old := `{"workers": [{"ticket": "x-3", "agent": "x-3", "tab": "w1:t5", "worktree": "/wt/x-3"}]}`
	if err := os.WriteFile(filepath.Join(repo, RunPath(StateName)), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadState(repo)
	if err != nil || len(got.Workers) != 1 || got.Workers[0].Terminal != "" ||
		!SameTerminal(got.Workers[0].Terminal, TerminalHerdr) {
		t.Errorf("an earlier state loaded as %+v, %v", got, err)
	}
}
