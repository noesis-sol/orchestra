package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The run reads its settings before it fast-forwards Base to its upstream as it starts: the settings
// the fast-forward brings in are taken as a merge's are (orchestra-qw5v.4), else an upstream commit
// that sets the setup or adds a requirement goes unheeded for the whole run.

// A fast-forward to a commit that changes the setup and the requirements has the run check the new
// requirement, once, before its first ticket, and set up that ticket's worktree with the new setup.
func TestTheStartsFastForwardBringsInTheSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := withRemote(h)
	setup, setups := recordsSetups(t, "")
	tool := filepath.Join(t.TempDir(), "tool")
	settings, err := json.Marshal(project.Settings{Setup: setup, Requires: []project.Requirement{
		{Name: "tool", Check: "test -e " + command.ShellQuote(tool), Install: "touch " + command.ShellQuote(tool)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(other, project.SettingsFile)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, settings, 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(other, "add", "-f", project.SettingsFile) // the harness's repository ignores .orchestra/
	h.git(other, "commit", "-q", "-m", "Set the setup and a requirement")
	h.git(other, "push", "-q", "origin", "main")

	h.beads.add("A", "needs them", 1)
	var met bool
	h.worker("A", func(w *fakeWorker) AgentState {
		met = exists(tool)
		return finishes("a.txt")(w)
	})
	runFetching(h)
	if !met {
		t.Error("A's worker started before the new requirement was installed")
	}
	if got := setUpIn(t, setups); !slices.Equal(got, []string{"A"}) {
		t.Errorf("the new setup ran in %v, want A's worktree", got)
	}
	ev := h.sink.text()
	for _, want := range []string{
		"  SETUP: the fast-forward to origin/main changed .orchestra/settings.json on main: the setup is '" +
			setup + "' from now on\n",
		"  REQUIRES: the fast-forward to origin/main changed the requirements in .orchestra/settings.json on " +
			"main: they are checked before the next ticket starts\n",
		"  REQUIRES: tool (installed with 'touch " + command.ShellQuote(tool) + "') installed\n",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if n := strings.Count(ev, "REQUIRES: checking 1 requirement\n"); n != 1 {
		t.Errorf("the requirements were checked %d times, want once, as the run starts:\n%s", n, ev)
	}
}
