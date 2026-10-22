package tmux

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/reap"
)

// A window's processes are its panes' pane_pids, each the root of what its pane runs, but for a dead
// pane's.
func TestTabProcessesAreThePanePIDs(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-panes", "0 4321\n1 4320\n0 4322\n")
	pids, err := Terminal{}.TabProcesses(bg, "@3")
	if err != nil || !slices.Equal(pids, []int{4321, 4322}) {
		t.Fatalf("got %v %v", pids, err)
	}
	if want := []string{"list-panes", "-t", "@3", "-F", "#{pane_dead} #{pane_pid}"}; !reflect.DeepEqual(f.call("list-panes"), want) {
		t.Errorf("called %q, want %q", f.call("list-panes"), want)
	}
	f.answer("list-panes", "0 x\n")
	if _, err := (Terminal{}).TabProcesses(bg, "@3"); err == nil || Missing(err) {
		t.Errorf("an answer that is no process ID gave %v, want an odd answer", err)
	}
	if _, err := (Terminal{}).TabProcesses(bg, "%3"); !Missing(err) {
		t.Errorf("a pane's ID gave %v, want a missing window", err)
	}
}

// A command left running in a process group of its own, which ignores the hangup, as Claude Code's
// Bash commands do, outlives its window's kill-window, and stops once reap.Close closes it. The shell
// that started it, and waits for it, stands for Claude Code.
func TestLiveCloseStopsADetachedCommand(t *testing.T) {
	l := newLive(t)
	detached := func(tab, pane string) int {
		t.Helper()
		pidFile := filepath.Join(t.TempDir(), "pid")
		l.tmux("send-keys", "-t", pane, `sh -c 'set -m; sh -c "trap \"\" HUP; exec sleep 300" & echo $! > "$1"; wait' _ `+
			`'`+pidFile+`'`, "Enter")
		var pid int
		l.eventually("the detached command never wrote its process ID", func() bool {
			b, _ := os.ReadFile(pidFile) // not yet written reads as nothing
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			return pid > 0
		})
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) }) // gone already, but for the plain close's
		return pid
	}
	alive := func(pid int) bool {
		// ps fails for a process it doesn't list, which has ended.
		out, err := command.Output(bg, command.ReadLimit, "", "ps", "-o", "stat=", "-p", strconv.Itoa(pid))
		out = strings.TrimSpace(out)
		return err == nil && out != "" && !strings.HasPrefix(out, "Z")
	}

	plain, plainPane := l.tab("x-1")
	left := detached(plain, plainPane)
	if err := l.term.CloseTab(bg, plain); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if !alive(left) {
		t.Fatal("the detached command ended with its window's plain close, so the test shows nothing")
	}

	tab, pane := l.tab("x-2")
	split, err := l.term.SplitPaneAside(bg, pane, l.dir)
	if err != nil {
		t.Fatal(err)
	}
	pids := []int{detached(tab, pane), detached(tab, split)}
	if err := reap.Close(bg, l.term, tab); err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		l.eventually("a detached command runs still once its window is closed", func() bool { return !alive(pid) })
	}
}
