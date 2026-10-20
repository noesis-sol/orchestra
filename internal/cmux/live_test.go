package cmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/claudetest"
	"github.com/noesis-sol/orchestra/internal/cmuxtest"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// The tests in this file run the real cmux, an app of the test's own (cmuxtest.Start), and a fake
// Claude Code that runs the hooks orchestra gives it (claudetest.Claude): what cmux does that the fake
// cmux can only be told to do, such as opening a workspace without switching to it, typing into a
// shell, showing a dialog on its screen and listing the processes in a workspace.

var _ = faketool.Func("claude", claudetest.Claude)

// live is a cmux of the test's own, with orchestra's book of it, and a fake claude on PATH that goes
// through its script; workspaces start in dir, whose name has a space and a '#'.
type live struct {
	t         *testing.T
	app       *cmuxtest.App
	term      Terminal
	dir       string
	claudeDir string // the fake claude's folder
}

func newLive(t *testing.T) *live {
	t.Helper()
	app := cmuxtest.Start(t)
	dir := filepath.Join(t.TempDir(), "work #1")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, claudeDir := t.TempDir(), t.TempDir()
	faketool.WriteFunc(t, bin, "claude", claudetest.Env(claudeDir))
	t.Setenv("PATH", faketool.Path(t, bin, "cmux")) // the app's CLI, which Start put first on PATH
	term := Terminal{Book: filepath.Join(t.TempDir(), BookName), Own: app.Own}
	return &live{t: t, app: app, term: term, dir: dir, claudeDir: claudeDir}
}

// script has the fake claude take these steps.
func (l *live) script(steps ...string) {
	l.t.Helper()
	if err := os.WriteFile(filepath.Join(l.claudeDir, "script"), []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		l.t.Fatal(err)
	}
}

// file reads a file of the fake claude's; "" if there is none.
func (l *live) file(name string) string {
	b, _ := os.ReadFile(filepath.Join(l.claudeDir, name)) // none yet reads as nothing
	return string(b)
}

// tab opens a workspace labelled label, as a worker's.
func (l *live) tab(label string) (tab, pane string) {
	l.t.Helper()
	tab, pane, err := l.term.CreateTab(bg, "", l.dir, label)
	if err != nil {
		l.t.Fatal(err)
	}
	return tab, pane
}

// eventually waits up to 30 seconds for cond.
func (l *live) eventually(what string, cond func() bool) {
	l.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !cond(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			l.t.Fatalf("%s; the fake claude's log:\n%s", what, l.file("log"))
		}
	}
}

// status is the agent's state, failing the test if cmux can't say.
func (l *live) status(name string) dispatch.AgentState {
	l.t.Helper()
	st, err := l.term.Status(bg, name)
	if err != nil {
		l.t.Fatal(err)
	}
	return st
}

// A worker's workspace opens named with its label, without switching to it; the dashboard's number
// selects it. Closing it closes it and no other: orchestra's own and the user's stay, and orchestra
// closes neither of them. Once closed, it is gone, which closing again doesn't mind.
func TestLiveWorkspaces(t *testing.T) {
	l := newLive(t)
	var made struct {
		Workspace string `json:"workspace_id"`
	}
	if err := json.Unmarshal([]byte(l.app.CLI("--json", "workspace", "create", "--name", "mine", "--focus", "false")),
		&made); err != nil || !isUUID(made.Workspace) {
		t.Fatalf("the user's workspace: %v", err)
	}
	mine := key(made.Workspace)
	tab, pane := l.tab("tx-1")
	tree := l.app.Tree()
	if tree.Titles[tab] != "tx-1" || !slices.Equal(tree.Surfaces[tab], []string{pane}) {
		t.Errorf("the worker's workspace is %q with terminals %v, want tx-1 with %s", tree.Titles[tab],
			tree.Surfaces[tab], pane)
	}
	if tree.Selected != l.app.Own {
		t.Errorf("the workspace in view is %s (%s), want orchestra's own, %s", tree.Selected, tree.Titles[tree.Selected],
			l.app.Own)
	}
	if label, open, err := l.term.TabLabel(bg, strings.ToLower(tab)); label != "tx-1" || !open || err != nil {
		t.Errorf("TabLabel: %q, %v, %v", label, open, err)
	}

	if err := l.term.FocusTab(bg, tab); err != nil {
		t.Fatal(err)
	}
	l.eventually("the dashboard's number for the worker never showed its workspace", func() bool {
		return l.app.Tree().Selected == tab
	})
	if err := l.term.FocusTab(bg, l.app.Own); err != nil {
		t.Fatal(err)
	}
	l.eventually("orchestra's workspace was never shown again", func() bool { return l.app.Tree().Selected == l.app.Own })

	if err := l.term.CloseTab(bg, tab); err != nil {
		t.Fatal(err)
	}
	if got := l.app.Tree().Order; !slices.Equal(got, []string{l.app.Own, mine}) {
		t.Errorf("workspaces left: %v, want orchestra's own and the user's, %s and %s", got, l.app.Own, mine)
	}
	if _, open, err := l.term.TabLabel(bg, tab); open || err != nil {
		t.Errorf("TabLabel of the closed workspace: %v, %v", open, err)
	}
	if err := l.term.CloseTab(bg, tab); err != nil {
		t.Errorf("closing it again: %v", err)
	}
	for _, other := range []string{mine, l.app.Own} {
		if err := l.term.CloseTab(bg, other); err == nil {
			t.Errorf("closing %s: no error", other)
		}
	}
	if got := l.app.Tree().Order; !slices.Equal(got, []string{l.app.Own, mine}) {
		t.Errorf("workspaces left: %v, want orchestra's own and the user's", got)
	}
}

// The interview's terminal opens to the right of orchestra's, with the keyboard focus, and closing it
// closes it and no other, giving orchestra's terminal the focus back.
func TestLiveSplit(t *testing.T) {
	l := newLive(t)
	split, err := l.term.SplitPane(bg, l.app.OwnSurface, l.dir)
	if err != nil {
		t.Fatal(err)
	}
	tree := l.app.Tree()
	if want := []string{l.app.OwnSurface, split}; !slices.Equal(tree.Surfaces[l.app.Own], want) {
		t.Errorf("orchestra's workspace has terminals %v, want %v", tree.Surfaces[l.app.Own], want)
	}
	if tree.Focused != split {
		t.Errorf("the focus is on %s, want the split, %s", tree.Focused, split)
	}
	if open, err := l.term.PaneOpen(bg, split); !open || err != nil {
		t.Errorf("PaneOpen: %v, %v", open, err)
	}
	if err := l.term.ClosePane(bg, split); err != nil {
		t.Fatal(err)
	}
	tree = l.app.Tree()
	if want := []string{l.app.OwnSurface}; !slices.Equal(tree.Surfaces[l.app.Own], want) {
		t.Errorf("orchestra's workspace has terminals %v, want %v", tree.Surfaces[l.app.Own], want)
	}
	if tree.Focused != l.app.OwnSurface {
		t.Errorf("the focus is on %s, want orchestra's terminal, %s", tree.Focused, l.app.OwnSurface)
	}
	if err := l.term.ClosePane(bg, l.app.OwnSurface); err == nil {
		t.Error("closing orchestra's own terminal: no error")
	}
}

// A worker launched in a folder Claude Code doesn't trust yet shows its trust dialog, which runs no
// hook: cmux's screen shows it blocked. The key that answers it goes through, and the worker starts,
// takes a prompt of several lines whole, as cmux gives it (pasted from 0.65.0, else through a file
// it attaches), and works on it. cmux lists its process in its workspace while it runs; once it
// exits, it is gone, though its terminal stays.
func TestLiveTrustDialogPromptAndProcess(t *testing.T) {
	l := newLive(t)
	l.script("trust", "start", "prompt", "sleep 1s", "stop", "idle")
	tab, pane := l.tab("tx-1")
	args, err := claude.Reporter{}.ReportArgs(l.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.term.LaunchInPane(bg, pane, "claude", args); err != nil {
		t.Fatal(err)
	}
	if _, err := l.term.AdoptAgent(bg, pane, "claude", "tx-1"); err != nil {
		t.Fatal(err)
	}
	l.eventually("the trust dialog was never shown as blocked", func() bool { return l.status("tx-1") == dispatch.StateBlocked })
	if err := l.term.SendKeys(bg, "tx-1", "enter"); err != nil {
		t.Fatal(err)
	}
	if !l.term.WaitReady(bg, "tx-1") {
		t.Fatalf("the worker never got ready after the trust dialog: %s; its screen:\n%s\nthe fake claude's log:\n%s",
			l.status("tx-1"), l.term.Screen(bg, "tx-1", ""), l.file("log"))
	}

	pid, _, ok := l.term.launched(pane)
	if !ok {
		t.Fatal("the launch wrote no process ID")
	}
	l.eventually("cmux top never listed the worker's process in its workspace", func() bool { return l.listed(tab, pid) })

	prompt := "Your ticket is tx-1.\n\nRead it with: bd show tx-1\nA 'quoted' word, a \\ and a $HOME."
	if err := l.term.Prompt(bg, "tx-1", prompt); err != nil {
		t.Fatalf("%v; the fake claude's log:\n%s", err, l.file("log"))
	}
	l.eventually("the worker's turn never ended", func() bool { return l.status("tx-1") == dispatch.StateIdle })
	if got := l.file("prompt-1"); got != prompt {
		t.Errorf("the worker got the prompt %q, want %q", got, prompt)
	}
	attached, _ := filepath.Glob(filepath.Join(l.dir, ".orchestra", "run", "cmux-prompt-*.md"))
	if pastes := l.term.features(bg).paste; pastes == (len(attached) > 0) {
		t.Errorf("cmux pastes: %v, and the prompt files written are %q", pastes, attached)
	}

	if err := l.term.SendKeys(bg, "tx-1", "ctrl+d"); err != nil { // the end of its input: it exits
		t.Fatal(err)
	}
	l.eventually("the worker that exited is not gone", func() bool { return l.status("tx-1") == dispatch.StateGone })
	if open, err := l.term.PaneOpen(bg, pane); !open || err != nil {
		t.Errorf("the worker's terminal: open %v, %v", open, err)
	}
	if l.listed(tab, pid) {
		t.Errorf("cmux top still lists the worker's process %d", pid)
	}
}

// listed reports whether cmux top lists process pid in the workspace.
func (l *live) listed(workspace string, pid int) bool {
	l.t.Helper()
	var v any
	if err := json.Unmarshal([]byte(l.app.CLI("--json", "top", "--processes", "--workspace", workspace)), &v); err != nil {
		l.t.Fatal(err)
	}
	return hasPID(v, pid)
}
