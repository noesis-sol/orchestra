package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// What changed between two commits, when git can't say, as when git diff passes its time limit on a
// loaded machine, is unknown rather than nothing (orchestra-4n6j.2): the setup runs, the settings and
// the worker prompt are warned of, and a footprint git couldn't read isn't kept.

// errDiff is git diff failing to answer.
var errDiff = errors.New("git diff --no-renames --name-only -z: timed out after 30s")

// diffFails is git in memory whose ChangedFiles fails while failing is set, counting the calls.
type diffFails struct {
	*fakeGit
	mu      sync.Mutex
	failing bool
	asked   int
}

func (g *diffFails) ChangedFiles(ctx context.Context, repo, from, to string) ([]string, error) {
	g.mu.Lock()
	g.asked++
	failing := g.failing
	g.mu.Unlock()
	if failing {
		return nil, errDiff
	}
	return g.fakeGit.ChangedFiles(ctx, repo, from, to)
}

func (g *diffFails) set(failing bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failing = failing
}

func (g *diffFails) calls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.asked
}

// With git unable to say what the rebase before the check brought in, the setup runs again before
// it, saying why, rather than the check running on dependencies set up before. Nor can git say
// whether the merge changed settings.json: that is warned of, and the setup stays as it was.
func TestTheSetupRunsWhenGitCannotSayWhatChanged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, log := recordsSetups(t, "")
		h.cfg.Setup, h.cfg.SetupFiles, h.cfg.Check = cmd, project.DefaultSetupFiles, "true"
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			h.mem.commit("main", "meanwhile", "README.md")
			return finishes("a.txt")(w)
		})
		o := h.loop()
		g := &diffFails{fakeGit: h.mem, failing: true}
		o.history = g
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v, want A\n%s", got, h.sink.text())
		}
		if got := setUpIn(t, log); !equal(got, []string{"A", "A"}) {
			t.Errorf("the setup ran in %v, want A's worktree twice: new, and before the check\n%s", got,
				h.sink.text())
		}
		ev := h.sink.text()
		said := "  A: git could not say what changed since its dependencies were last set up; running '" + cmd +
			"' before the check\n"
		if !strings.Contains(ev, said) {
			t.Errorf("events lack %q:\n%s", said, ev)
		}
		warned := "  SETTINGS_UNREAD: git could not say whether A's merge changed .orchestra/settings.json on main: " +
			errDiff.Error() + "; the setup stays '" + cmd + "', and the requirements as they were"
		if got := eventsOf(h, EvWarn); len(got) != 1 || got[0].Text != warned {
			t.Errorf("warnings %+v, want %q", got, warned)
		}
		if s := o.setupNow(); s.cmd != cmd {
			t.Errorf("the setup is %q, want %q kept", s.cmd, cmd)
		}
	})
}

// forwardUnknown is git for TakeFastForward that can't say what the fast-forward changed.
type forwardUnknown struct{ forwardFiles }

func (forwardUnknown) ChangedFiles(context.Context, string, string, string) ([]string, error) {
	return nil, errDiff
}

// A fast-forward git can't say the changes of keeps the settings and the worker prompt the run read,
// and warns of both, rather than taking them as unchanged without a word.
func TestAFastForwardGitCannotReadKeepsTheSettingsAndThePrompt(t *testing.T) {
	t.Parallel()
	c := takeConfig(t)
	prompt := "Work on TICKET_ID."
	g := forwardUnknown{forwardFiles{project.SettingsFile: `{"setup": "npm ci"}`,
		".orchestra/worker-prompt.md": "Work on TICKET_ID, as upstream says."}}
	lines := TakeFastForward(t.Context(), g, &c, &prompt, takeBy, "a", "b")
	if c.Setup != "make deps" || c.Check != "make fast" || prompt != "Work on TICKET_ID." {
		t.Errorf("setup %q, check_fast %q, prompt %q: want those the run had", c.Setup, c.Check, prompt)
	}
	want := []string{
		"  SETTINGS_UNREAD: git could not say whether " + takeBy + " changed .orchestra/settings.json on main: " +
			errDiff.Error() + "; the setup, the requirements and the check commands stay as they were",
		"  PROMPT_UNREAD: git could not say whether " + takeBy + " changed .orchestra/worker-prompt.md on main: " +
			errDiff.Error() + "; the run's workers get the worker prompt it read before",
	}
	if got := eventTexts(lines); !slices.Equal(got, want) {
		t.Errorf("lines %q, want %q", got, want)
	}
	for _, l := range lines {
		if l.Kind != EvWarn {
			t.Errorf("%q is %v, want a warning", l.Text, l.Kind)
		}
	}

	c.PromptFile = filepath.Join(t.TempDir(), "worker-prompt.md") // outside the repository: not Base's
	if got := eventTexts(TakeFastForward(t.Context(), g, &c, &prompt, takeBy, "a", "b")); len(got) != 1 ||
		!strings.HasPrefix(got[0], "  SETTINGS_UNREAD: ") {
		t.Errorf("with the prompt outside the repository: lines %q, want the settings' warning alone", got)
	}
}

// A ticket's branch git can't say the changes of has no footprint from it, and isn't kept as
// changing nothing: git is asked again, and its answer then is the footprint.
func TestABranchGitCannotReadIsAskedAgain(t *testing.T) {
	t.Parallel()
	mem := newFakeGit("main")
	wt := filepath.Join(t.TempDir(), "A")
	if _, err := mem.NewWorktree(t.Context(), "", wt, "wt/A", "main"); err != nil {
		t.Fatal(err)
	}
	if err := mem.commitIn(wt, "A: change the loop", "internal/dispatch/loop.go"); err != nil {
		t.Fatal(err)
	}
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	g := &diffFails{fakeGit: mem, failing: true}
	o := &Loop{cfg: Config{Base: "main"}, log: log, predictor: newPredictor(), ledger: newLedger()}
	o.worktrees, o.merger, o.history = mem, mem, g
	r := &runState{o: o, ctx: t.Context(), files: newRepoFiles(trackedHere, ""), branches: map[string][]string{}}
	if got := r.branchChanges("A"); len(got) != 0 {
		t.Errorf("with git failing: %q, want none", got)
	}
	g.set(false)
	if got := r.branchChanges("A"); !slices.Equal(got, []string{"internal/dispatch/loop.go"}) {
		t.Errorf("once git answers: %q, want the branch's file", got)
	}
	if n := g.calls(); n != 2 {
		t.Errorf("git diff asked %d times, want twice: the failure not kept", n)
	}
	if got := r.branchChanges("A"); !slices.Equal(got, []string{"internal/dispatch/loop.go"}) || g.calls() != 2 {
		t.Errorf("the third time: %q, with git asked %d times; want the answer kept", got, g.calls())
	}
}
