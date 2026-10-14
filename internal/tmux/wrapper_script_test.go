package tmux

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// A claude that is a wrapper script running Claude Code as its child, not through exec, leaves the
// script's shell the pane's foreground process: the worker reads its hooks' state all the same, and
// gone only once its process exits.
func TestLiveWrapperScript(t *testing.T) {
	l := newLive(t)
	fake, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := t.TempDir()
	faketool.Write(t, wrapper, "claude", command.ShellQuote(fake)+` "$@"`+"\n")
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))

	l.script(false, "start", "keys", "exit 3")
	_, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if got := l.tmux("display-message", "-p", "-t", pane, "#{pane_current_command}"); !slices.Contains(shells, got) {
		t.Fatalf("the pane's foreground process is %q, not the wrapper's shell", got)
	}
	if st := l.status("x-1"); st != dispatch.StateIdle {
		t.Errorf("started through a wrapper script: %s", st)
	}
	if err := l.term.SendKeys(bg, "x-1", "exit", "enter"); err != nil {
		t.Fatal(err)
	}
	l.eventually("never gone once it exited", func() bool { return l.status("x-1") == dispatch.StateGone })
}

// An agent run from a shell's prompt, in a pane another respawned after orchestra launched one there,
// is gone once back at the prompt.
func TestLiveAgentRunFromAShell(t *testing.T) {
	l := newLive(t)
	fake, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	l.script(false, "start", "end")
	hooks := l.hooks()
	_, pane := l.tab("x-1")
	if err := l.term.LaunchInPane(bg, pane, "claude", hooks); err != nil {
		t.Fatal(err)
	}
	if _, err := l.term.AdoptAgent(bg, pane, "claude", "x-1"); err != nil {
		t.Fatal(err)
	}
	l.eventually("never gone once its session ended", func() bool { return l.status("x-1") == dispatch.StateGone })

	l.script(false, "start", "keys")
	l.tmux("respawn-pane", "-k", "-t", pane, "/bin/sh")
	line := []string{command.ShellQuote(fake)}
	for _, a := range hooks {
		line = append(line, command.ShellQuote(a))
	}
	l.tmux("send-keys", "-t", pane, "-l", strings.Join(line, " "))
	l.tmux("send-keys", "-t", pane, "Enter")
	l.eventually("never idle, run from a shell", func() bool { return l.status("x-1") == dispatch.StateIdle })
	if err := l.term.SendKeys(bg, "x-1", "exit", "enter"); err != nil {
		t.Fatal(err)
	}
	l.eventually("never gone once back at the shell's prompt", func() bool {
		return l.status("x-1") == dispatch.StateGone
	})
	if got := l.tmux("display-message", "-p", "-t", pane, "#{pane_dead}"); got != "0" {
		t.Errorf("the shell's pane is dead: %s", got)
	}
}
