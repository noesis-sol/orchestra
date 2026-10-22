package tmux

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
	"github.com/noesis-sol/orchestra/internal/claude"
)

// Every call runs tmux in UTF-8 mode (-u), whatever the locale says.
func TestCallsUTF8(t *testing.T) {
	f := newFakeTmux(t)
	if _, _, err := (Terminal{}).TabLabel(bg, "@1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (Terminal{Socket: "orch-test"}).TabLabel(bg, "@1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.argv() {
		if len(c) == 0 || c[0] != "-u" {
			t.Errorf("tmux called without -u first: %q", c)
		}
	}
	if got := f.argv(); len(got) != 2 || !slices.Equal(got[1][:3], []string{"-u", "-L", "orch-test"}) {
		t.Errorf("calls: %q", got)
	}
}

// With no locale saying UTF-8, as under cron or launchd, tmux still answers non-ASCII text as it is:
// an agent named for a repository at a non-ASCII path, with its records in one, is found and its
// state read, and a session with a non-ASCII name is found again rather than started twice.
func TestLiveNonASCIIWithoutLocale(t *testing.T) {
	for _, v := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(v, "")
	}
	l := newLive(t)
	// tmux takes any $TMUX, even empty, as the sign of a client in tmux, which is in UTF-8 mode: as under
	// cron, there is none. Server set it empty, and restores it as the test ends.
	if err := os.Unsetenv("TMUX"); err != nil {
		t.Fatal(err)
	}
	l.term.Repo = "/Users/me/Código/shop"
	l.script(false, "start", "idle")
	records := filepath.Join(t.TempDir(), "Código")
	if err := os.Mkdir(records, 0o755); err != nil {
		t.Fatal(err)
	}
	args, err := claude.Reporter{}.ReportArgs(records)
	if err != nil {
		t.Fatal(err)
	}
	_, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, args); err != nil {
		t.Fatal(err)
	}
	if st := l.status("x-1"); st != agentio.StateIdle {
		t.Errorf("started: %s", st)
	}
	if tab, err := l.term.AgentTab(bg, "x-1"); tab == "" || err != nil {
		t.Errorf("AgentTab = %q %v", tab, err)
	}

	var ids []string
	for range 2 {
		id, err := l.term.NewSession(bg, "orchestra-Código", l.dir)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] != ids[1] {
		t.Errorf("started twice: %q", ids)
	}
}
