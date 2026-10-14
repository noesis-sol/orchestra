package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/tmuxtest"
)

// The tests in this file run the real tmux, each its own server (tmuxtest.Server), and a fake Claude
// Code that runs the hooks orchestra gives it (tmuxtest.Claude): what tmux does that the fake tmux
// can only be told to do, such as keeping a window's name, pasting a prompt bracketed and keeping a
// dead pane open.

var _ = faketool.Func("claude", tmuxtest.Claude)

// live is a tmux server of the test's own, with a session t, and a fake claude on PATH that goes
// through script; t's working directory is dir, whose name has a '#', which tmux would expand.
type live struct {
	t         *testing.T
	term      Terminal
	dir       string // where windows start
	claudeDir string // the fake claude's folder
}

func newLive(t *testing.T) *live {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped by -short: runs a real tmux")
	}
	dir := filepath.Join(t.TempDir(), "work #1")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, claudeDir := t.TempDir(), t.TempDir()
	faketool.WriteFunc(t, bin, "claude", tmuxtest.ClaudeEnv(claudeDir))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &live{t: t, term: Terminal{Socket: tmuxtest.Server(t, "t", dir)}, dir: dir, claudeDir: claudeDir}
}

// tmux runs a tmux command on the test's server and returns its output.
func (l *live) tmux(args ...string) string {
	l.t.Helper()
	out, err := command.Output(bg, command.ReadLimit, "", "tmux", append([]string{"-L", l.term.Socket}, args...)...)
	if err != nil {
		l.t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// script has the fake claude take these steps, in a session it resumes if resumed.
func (l *live) script(resumed bool, steps ...string) {
	l.t.Helper()
	name := "script"
	if resumed {
		name = "script.resume"
	}
	if err := os.WriteFile(filepath.Join(l.claudeDir, name), []byte(strings.Join(steps, "\n")+"\n"), 0o644); err != nil {
		l.t.Fatal(err)
	}
}

// file reads a file of the fake claude's; "" if there is none.
func (l *live) file(name string) string {
	b, _ := os.ReadFile(filepath.Join(l.claudeDir, name)) // none yet reads as nothing
	return string(b)
}

// tab opens a window labelled label.
func (l *live) tab(label string) (tab, pane string) {
	l.t.Helper()
	tab, pane, err := l.term.CreateTab(bg, "t", l.dir, label)
	if err != nil {
		l.t.Fatal(err)
	}
	return tab, pane
}

// hooks returns the arguments that give a Claude worker in a worktree of its own its hooks.
func (l *live) hooks() []string {
	l.t.Helper()
	args, err := claude.Reporter{}.ReportArgs(l.t.TempDir())
	if err != nil {
		l.t.Fatal(err)
	}
	return args
}

// eventually waits up to 30 seconds for cond.
func (l *live) eventually(what string, cond func() bool) {
	l.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			l.t.Fatalf("%s; the fake claude's log:\n%s", what, l.file("log"))
		}
	}
}

// status is the agent's state, failing the test if tmux can't say.
func (l *live) status(name string) dispatch.AgentState {
	l.t.Helper()
	st, err := l.term.Status(bg, name)
	if err != nil {
		l.t.Fatal(err)
	}
	return st
}

// A window opens in the session, in its folder, with its label, which it keeps whatever runs in it
// says; it can be focused and closed, and once closed it is gone, which closing again doesn't mind.
func TestLiveTabs(t *testing.T) {
	l := newLive(t)
	tab, pane := l.tab("x-1 #{pane_id}")
	if !isID(tab, '@') || !isID(pane, '%') {
		t.Fatalf("CreateTab = %q %q", tab, pane)
	}
	want, err := filepath.EvalSymlinks(l.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.tmux("display-message", "-p", "-t", pane, "#{pane_current_path}"); got != want {
		t.Errorf("the window starts in %q, want %q", got, want)
	}
	// The title escapes a program writes, and the commands that run, rename no window of orchestra's.
	l.tmux("send-keys", "-t", pane, `printf '\033kretitled\033\\'; printf '\033]2;titled\007'; sleep 0.2`, "Enter")
	// Its title shows tmux has read both escapes; the second's wait then gives the command time to rename it.
	l.eventually("the pane never read the escapes", func() bool {
		return l.tmux("display-message", "-p", "-t", pane, "#{pane_title}") == "titled"
	})
	time.Sleep(time.Second)
	if label, open, err := l.term.TabLabel(bg, tab); label != "x-1 #{pane_id}" || !open || err != nil {
		t.Errorf("TabLabel = %q %v %v", label, open, err)
	}

	other, _ := l.tab("x-2")
	if got := l.tmux("display-message", "-p", "-t", "t", "#{window_id}"); got == other {
		t.Error("CreateTab switched to the window it opened")
	}
	if err := l.term.FocusTab(bg, other); err != nil {
		t.Fatal(err)
	}
	if got := l.tmux("display-message", "-p", "-t", "t", "#{window_id}"); got != other {
		t.Errorf("the session's window is %s, want %s", got, other)
	}

	if err := l.term.CloseTab(bg, tab); err != nil {
		t.Fatal(err)
	}
	if label, open, err := l.term.TabLabel(bg, tab); open || err != nil {
		t.Errorf("a closed window: %q %v %v", label, open, err)
	}
	if err := l.term.CloseTab(bg, tab); err != nil {
		t.Errorf("closing a closed window: %v", err)
	}
	if err := l.term.FocusTab(bg, tab); err == nil {
		t.Error("focused a closed window")
	}
}

// A pane splits off another, in its session; it can be closed, which closing again doesn't mind. A
// session is started once, and a pane tmux doesn't have is in none.
func TestLivePanesAndSessions(t *testing.T) {
	l := newLive(t)
	_, pane := l.tab("x-1")
	split, err := l.term.SplitPane(bg, pane, l.dir)
	if err != nil || !isID(split, '%') || split == pane {
		t.Fatalf("SplitPane = %q %v", split, err)
	}
	if open, err := l.term.PaneOpen(bg, split); !open || err != nil {
		t.Errorf("PaneOpen = %v %v", open, err)
	}
	if s, err := l.term.PaneSession(bg, split); s != "t" || err != nil {
		t.Errorf("PaneSession = %q %v", s, err)
	}
	if err := l.term.ClosePane(bg, split); err != nil {
		t.Fatal(err)
	}
	if open, err := l.term.PaneOpen(bg, split); open || err != nil {
		t.Errorf("a closed pane: %v %v", open, err)
	}
	if err := l.term.ClosePane(bg, split); err != nil {
		t.Errorf("closing a closed pane: %v", err)
	}
	if _, err := l.term.PaneSession(bg, split); !Missing(err) {
		t.Errorf("the session of a closed pane: %v", err)
	}

	name := SessionName("orchestra-my.repo")
	var ids []string
	for range 2 {
		id, err := l.term.NewSession(bg, name, l.dir)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] != ids[1] {
		t.Errorf("started twice: %q", ids)
	}
	if got := l.tmux("list-sessions", "-F", "#{session_name}"); got != "orchestra-my_repo\nt" {
		t.Errorf("sessions:\n%s", got)
	}
	if _, _, err := l.term.CreateTab(bg, ids[0], l.dir, "x-2"); err != nil {
		t.Errorf("a window in the new session: %v", err)
	}
}

// A Claude worker starts in a window's pane, named, and is idle once its session starts; a prompt
// with several lines, quotes and characters tmux or a shell would expand is pasted in, bracketed,
// and arrives intact, and the worker works on it; keys are pressed in its pane; its screen is read;
// and it is idle again once its turn ends.
func TestLiveStartPromptKeysAndScreen(t *testing.T) {
	l := newLive(t)
	l.script(false, "start", "prompt", "keys", "sleep 0.5s", "stop", "idle")
	_, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if st := l.status("x-1"); st != dispatch.StateIdle {
		t.Errorf("started: %s", st)
	}
	if n, k, st, err := l.term.PaneAgent(bg, pane); n != "x-1" || k != "claude" || st != dispatch.StateIdle || err != nil {
		t.Errorf("PaneAgent = %q %q %q %v", n, k, st, err)
	}

	prompt := "Work on x-1.\n\nIt's \"quoted\", costs $HOME and `date`, names #{pane_id} and ends in a ;\n" +
		"  indented, with a \\ and ünïcödé ✓\nlast line"
	if err := l.term.Prompt(bg, "x-1", prompt); err != nil {
		t.Fatal(err)
	}
	if got := l.file("prompt-1"); got != prompt {
		t.Errorf("the prompt arrived as\n%q\nwant\n%q", got, prompt)
	}
	if st := l.status("x-1"); st != dispatch.StateWorking {
		t.Errorf("on its prompt: %s", st)
	}
	if !l.term.WaitStarted(bg, "x-1") {
		t.Error("WaitStarted is false for a working agent")
	}

	if err := l.term.SendKeys(bg, "x-1", "typed; as it is #{x}", "space", "enter", "exit", "enter"); err != nil {
		t.Fatal(err)
	}
	l.eventually("the keys never arrived", func() bool { return l.file("keys") == "typed; as it is #{x} \nexit\n" })
	if got := l.file("keys"); got != "typed; as it is #{x} \nexit\n" {
		t.Errorf("keys arrived as %q", got)
	}
	if !l.term.WaitReady(bg, "x-1") {
		t.Fatalf("not idle once its turn ended: %s", l.status("x-1"))
	}
	screen := l.term.Screen(bg, "x-1", dispatch.StateIdle)
	for _, want := range []string{"fake claude: session", "● Done."} {
		if !strings.Contains(screen, want) {
			t.Errorf("the screen lacks %q:\n%s", want, screen)
		}
	}
	if got := l.term.Screen(bg, "x-2", ""); got != "" {
		t.Errorf("the screen of no agent: %q", got)
	}
}

// A worker launched in a pane, as from its prompt file, is adopted by its name, renamed, and found
// by its new name only; an agent of another kind isn't adopted.
func TestLiveLaunchAdoptAndRename(t *testing.T) {
	l := newLive(t)
	l.script(false, "start", "idle")
	_, pane := l.tab("x-1")
	if err := l.term.LaunchInPane(bg, pane, "claude", l.hooks()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.term.AdoptAgent(bg, pane, "codex", "x-1"); !Missing(err) {
		t.Errorf("adopted another kind: %v", err)
	}
	if _, err := l.term.AdoptAgent(bg, pane, "claude", "x-1"); err != nil {
		t.Fatal(err)
	}
	l.eventually("the adopted agent never got idle", func() bool { return l.status("x-1") == dispatch.StateIdle })
	if got := l.term.FreeName(bg, "x-1"); got != "x-1-1" {
		t.Errorf("FreeName = %q", got)
	}
	if err := l.term.RenameAgent(bg, "x-1", "x-1-1"); err != nil {
		t.Fatal(err)
	}
	if st := l.status("x-1"); st != dispatch.StateGone {
		t.Errorf("by its old name: %s", st)
	}
	if st := l.status("x-1-1"); st != dispatch.StateIdle {
		t.Errorf("by its new name: %s", st)
	}
	if got := l.term.FreeName(bg, "x-1"); got != "x-1-2" {
		t.Errorf("FreeName once x-1-1 is taken = %q", got)
	}
}

// A permission prompt blocks the worker until it is answered; a dialog that runs no hook, such as
// whether to trust the folder, is read from the screen.
func TestLiveBlocked(t *testing.T) {
	l := newLive(t)
	l.script(false, "start", "prompt", "permission true", "sleep 0.5s", "stop", "idle")
	_, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if err := l.term.Prompt(bg, "x-1", "Work on x-1."); err != nil {
		t.Fatal(err)
	}
	l.eventually("never blocked on its permission prompt", func() bool { return l.status("x-1") == dispatch.StateBlocked })
	if err := l.term.SendKeys(bg, "x-1", "1", "enter"); err != nil {
		t.Fatal(err)
	}
	if !l.term.WaitReady(bg, "x-1") {
		t.Fatalf("not idle once its permission prompt was answered: %s", l.status("x-1"))
	}
	if log := l.file("log"); !strings.Contains(log, `permission answered "1"`) {
		t.Errorf("the fake claude's log:\n%s", log)
	}

	l.script(false, "say ❯ 1. Yes, I trust this folder", "keys")
	_, pane = l.tab("x-2")
	if err := l.term.LaunchInPane(bg, pane, "claude", l.hooks()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.term.AdoptAgent(bg, pane, "claude", "x-2"); err != nil {
		t.Fatal(err)
	}
	l.eventually("a dialog never showed it blocked", func() bool { return l.status("x-2") == dispatch.StateBlocked })
}

// A worker whose process exits is gone, though its pane stays open, dead, its screen still read, and
// its window with it; a prompt to it fails and it is never ready. A worker whose session ends is gone
// too. The dead window is closed as any other.
func TestLiveAgentGone(t *testing.T) {
	l := newLive(t)
	l.script(false, "start", "keys", "exit 3")
	tab, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if err := l.term.SendKeys(bg, "x-1", "exit", "enter"); err != nil {
		t.Fatal(err)
	}
	l.eventually("never gone once it exited", func() bool { return l.status("x-1") == dispatch.StateGone })
	// tmux marks the pane dead as its terminal closes, and has its exit status once it has reaped it.
	l.eventually("the pane never showed it dead with exit status 3", func() bool {
		return l.tmux("display-message", "-p", "-t", pane, "#{pane_dead} #{pane_dead_status}") == "1 3"
	})
	if open, err := l.term.PaneOpen(bg, pane); !open || err != nil {
		t.Errorf("PaneOpen = %v %v", open, err)
	}
	if label, open, err := l.term.TabLabel(bg, tab); label != "x-1" || !open || err != nil {
		t.Errorf("the dead window: %q %v %v", label, open, err)
	}
	if n, k, st, err := l.term.PaneAgent(bg, pane); n != "x-1" || k != "claude" || st != dispatch.StateGone || err != nil {
		t.Errorf("PaneAgent = %q %q %q %v", n, k, st, err)
	}
	if screen := l.term.Screen(bg, "x-1", dispatch.StateGone); !strings.Contains(screen, "fake claude: session") {
		t.Errorf("the dead pane's screen:\n%s", screen)
	}
	if err := l.term.Prompt(bg, "x-1", "Carry on."); err == nil {
		t.Errorf("a prompt to a gone agent: %v", err)
	}
	if l.term.WaitReady(bg, "x-1") || l.term.WaitStarted(bg, "x-1") {
		t.Error("a gone agent is ready")
	}
	if err := l.term.CloseTab(bg, tab); err != nil {
		t.Fatal(err)
	}
	if _, open, err := l.term.TabLabel(bg, tab); open || err != nil {
		t.Errorf("the closed dead window: %v %v", open, err)
	}

	l.script(false, "start", "keys", "end")
	_, pane = l.tab("x-2")
	if err := l.term.StartAgent(bg, "x-2", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if err := l.term.SendKeys(bg, "x-2", "exit", "enter"); err != nil {
		t.Fatal(err)
	}
	l.eventually("never gone once its session ended", func() bool { return l.status("x-2") == dispatch.StateGone })
}

// Once the server is gone, there are no windows, panes or agents, which is no error.
func TestLiveServerGone(t *testing.T) {
	l := newLive(t)
	tab, pane := l.tab("x-1")
	l.tmux("kill-server")
	// kill-server returns before the server has gone: a call meanwhile can find it exiting.
	l.eventually("the server never went", func() bool { return noServer(l.term.socketPath()) })
	if _, open, err := l.term.TabLabel(bg, tab); open || err != nil {
		t.Errorf("TabLabel = %v %v", open, err)
	}
	if open, err := l.term.PaneOpen(bg, pane); open || err != nil {
		t.Errorf("PaneOpen = %v %v", open, err)
	}
	if st, err := l.term.Status(context.Background(), "x-1"); st != dispatch.StateGone || err != nil {
		t.Errorf("Status = %s %v", st, err)
	}
}
