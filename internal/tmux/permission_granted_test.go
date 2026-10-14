package tmux

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A worker allowed to run a command that takes long works while it runs, though no hook ran since
// it asked: the fake Claude Code runs its hooks in Claude Code's order (PreToolUse, PermissionRequest,
// then PostToolUse once the command is done) and clears the dialog as the command runs.
func TestLivePermissionAllowedWorks(t *testing.T) {
	l := newLive(t)
	done := filepath.Join(t.TempDir(), "done")
	l.script(false, "start", "prompt", "permission while [ ! -e '"+done+"' ]; do sleep 0.05; done", "stop", "idle")
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
	l.eventually("not working while its command allowed runs", func() bool { return l.status("x-1") == dispatch.StateWorking })
	if err := os.WriteFile(done, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !l.term.WaitReady(bg, "x-1") {
		t.Fatalf("not idle once its command was done: %s", l.status("x-1"))
	}
}
