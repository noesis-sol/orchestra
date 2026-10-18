package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The project's setup runs in each new worktree before its worker starts (orchestra-qd9e.2): in the
// tutti run of 2026-10-16/17, setup was never set, and 112 of the 124 workers installed the
// dependencies themselves.

// recordsSetups is a setup command that notes the worktree it runs in by its folder's name, the
// ticket's ID, in a file it returns the path of, and then runs then, if set.
func recordsSetups(t *testing.T, then string) (cmd, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "setups")
	cmd = `basename "$PWD" >> ` + command.ShellQuote(log)
	if then != "" {
		cmd += "; " + then
	}
	return cmd, log
}

// setUpIn lists the worktrees the setup recordsSetups made ran in, in the order it ran there.
func setUpIn(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

// failsIn is a command for recordsSetups to run that fails, printing npm's error, in the worktree of
// ticket id until the file fixed is there, and works anywhere else.
func failsIn(id, fixed string) string {
	return `test "$(basename "$PWD")" != ` + id + ` || test -f ` + command.ShellQuote(fixed) +
		` || { echo 'npm ERR! offline'; exit 1; }`
}

// A ticket's worker starts in its new worktree once the setup has run there, and its standing rules
// say how its dependencies were installed. Without a setup, nothing runs and the rules say nothing of
// one.
func TestTheSetupRunsInANewWorktreeBeforeItsWorkerStarts(t *testing.T) {
	t.Parallel()
	for _, set := range []bool{true, false} {
		t.Run(map[bool]string{true: "setup", false: "no setup"}[set], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				cmd, log := recordsSetups(t, "touch installed")
				if set {
					h.cfg.Setup = cmd
				}
				h.beads.add("A", "first", 1)
				var installed bool
				var rules string
				h.worker("A", func(w *fakeWorker) AgentState {
					installed, rules = exists(filepath.Join(w.wt, "installed")), readRun(t, w.wt, project.RulesName)
					return finishes("a.txt")(w)
				})
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if !set {
					if got := setUpIn(t, log); installed || len(got) > 0 {
						t.Errorf("with no setup, it ran in %v", got)
					}
					if strings.Contains(rules, "dependencies are installed") {
						t.Errorf("with no setup, the rules say:\n%s", rules)
					}
					return
				}
				if got := setUpIn(t, log); !installed || !equal(got, []string{"A"}) {
					t.Errorf("the worker found the setup's work %v; it ran in %v", installed, got)
				}
				said := "  A: running '" + cmd + "' in its new worktree before its worker starts\n"
				if ev := h.sink.text(); !strings.Contains(ev, said) {
					t.Errorf("events lack %q:\n%s", said, ev)
				}
				want := "This worktree's dependencies are installed with '" + cmd + "'. Run it again only after " +
					"changing a file it reads, such as a manifest or a lockfile."
				if !strings.Contains(rules, want) {
					t.Errorf("the rules lack %q:\n%s", want, rules)
				}
			})
		})
	}
}

// The check after the ticket, on its branch rebased onto a Base that moved on, sets the dependencies
// up again only when a file the setup reads changed since its worktree was set up: on Base, or in the
// worker's own commits.
func TestTheCheckAfterANewWorktreeSetsUpAgainOnlyForAChangedLockfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, onMain, own string
		want              []string
	}{
		{"nothing it reads changed", "README.md", "a.txt", []string{"A"}},
		{"a lockfile changed on main", "package-lock.json", "a.txt", []string{"A", "A"}},
		{"its worker changed the lockfile", "README.md", "package-lock.json", []string{"A", "A"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				cmd, log := recordsSetups(t, "")
				h.cfg.Setup, h.cfg.SetupFiles, h.cfg.Check = cmd, project.DefaultSetupFiles, "true"
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					h.mem.commit("main", "meanwhile", tc.onMain)
					return finishes(tc.own)(w)
				})
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := closedIDs(h); !equal(got, []string{"A"}) {
					t.Errorf("merged %v, want A\n%s", got, h.sink.text())
				}
				if got := setUpIn(t, log); !equal(got, tc.want) {
					t.Errorf("the setup ran in %v, want %v\n%s", got, tc.want, h.sink.text())
				}
				said := "  A: package-lock.json changed since its dependencies were last set up; running '" + cmd +
					"' before the check\n"
				if ev := h.sink.text(); strings.Contains(ev, said) != (len(tc.want) > 1) ||
					!strings.Contains(ev, "'true' passes on the rebased wt/A") {
					t.Errorf("events should say %q only when the setup runs again:\n%s", said, ev)
				}
			})
		})
	}
}

// A worktree an earlier attempt left is set up again before its new worker starts only when its
// rebase onto Base changes a file the setup reads.
func TestAReusedWorktreeIsSetUpWhenItsRebaseChangesALockfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		onMain string
		want   []string
	}{
		{"package-lock.json", []string{"A"}},
		{"README.md", nil},
	} {
		t.Run(tc.onMain, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				cmd, log := recordsSetups(t, "")
				h.cfg.Setup, h.cfg.SetupFiles = cmd, project.DefaultSetupFiles
				wt := h.worktree("A")
				if _, err := h.mem.NewWorktree(t.Context(), h.repo, wt, "wt/A", "main"); err != nil {
					t.Fatal(err)
				}
				if err := h.mem.commitIn(wt, "A: an earlier attempt", "left.txt"); err != nil {
					t.Fatal(err)
				}
				h.mem.commit("main", "meanwhile", tc.onMain)
				h.beads.add("A", "first", 1)
				h.worker("A", finishes("a.txt"))
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := setUpIn(t, log); !equal(got, tc.want) {
					t.Errorf("the setup ran in %v, want %v\n%s", got, tc.want, h.sink.text())
				}
				said := "  A: package-lock.json changed since its dependencies were last set up; running '" + cmd +
					"' in its worktree before its worker starts\n"
				if ev := h.sink.text(); !strings.Contains(ev, "reusing worktree "+wt) ||
					strings.Contains(ev, said) != (tc.want != nil) {
					t.Errorf("events should say %q only when the setup runs:\n%s", said, ev)
				}
			})
		})
	}
}

// A setup that fails in a ticket's new worktree starts no worker: SETUP_FAILED, and the ticket goes
// back in the queue, open and not set aside, its worktree and the setup's output kept. It is taken
// again once a ticket merges, when the setup runs again in its worktree, works, and the worker starts.
func TestAFailedSetupStartsNoWorkerAndPutsTheTicketBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		fixed := filepath.Join(t.TempDir(), "fixed")
		cmd, log := recordsSetups(t, failsIn("A", fixed))
		h.cfg.Setup = cmd
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		wt := h.worktree("A")
		output := filepath.Join(wt, ".orchestra", "run", setupLogName)
		h.worker("B", func(w *fakeWorker) AgentState {
			if st := h.statusOf("A"); st != StatusOpen {
				t.Errorf("A is %s while B runs, want open", st)
			}
			if b, err := os.ReadFile(output); string(b) != "npm ERR! offline\n" {
				t.Errorf("A's %s = %q, %v", setupLogName, b, err)
			}
			if err := os.WriteFile(fixed, nil, 0o644); err != nil {
				t.Error(err)
			}
			return finishes("b.txt")(w)
		})
		h.worker("A", func(w *fakeWorker) AgentState {
			if exists(output) {
				t.Errorf("%s is left after the setup worked there", output)
			}
			return finishes("a.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
			t.Errorf("merged %v, want B, then A\n%s", got, h.sink.text())
		}
		if got := setUpIn(t, log); !equal(got, []string{"A", "B", "A"}) {
			t.Errorf("the setup ran in %v, want A's worktree, B's, then A's again", got)
		}
		if n, starts := dispatches(h, "A"), len(h.herdr.argsFor("A")); n != 2 || starts != 1 {
			t.Errorf("A dispatched %d times and its worker started %d times; want twice, and once", n, starts)
		}
		warned := "SETUP_FAILED: '" + cmd + "' fails in " + wt + " for A; the whole output is in " + output
		if got := eventsOf(h, EvWarn); len(got) != 1 || got[0].Ticket != "A" || got[0].Aside ||
			strings.TrimSpace(got[0].Text) != warned {
			t.Errorf("warnings %+v, want only %q", got, warned)
		}
		if got := eventsOf(h, EvDeferred); len(got) != 0 {
			t.Errorf("set aside: %+v", got)
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A: running '" + cmd + "' in its new worktree before its worker starts\n",
			"  A: the last setup in its worktree failed; running '" + cmd + "' there again before its worker starts\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		note := "Orchestra: the setup '" + cmd + "' fails in worktree " + wt + ", so no worker was started; " +
			"back in the queue (the whole output is in " + output + ")."
		if n := h.beads.notesOf("A"); !strings.Contains(n, note) {
			t.Errorf("A's notes lack %q:\n%s", note, n)
		}
	})
}

// Setups failing ticket after ticket hold the run for the environment, as workers failing at once
// do, rather than leave the queue to them: no more tickets start, and those whose setup failed stay
// in the queue, open, without being reopened.
func TestSetupsFailingInARowHoldTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		cmd, log := recordsSetups(t, "echo 'npm ERR! offline'; exit 1")
		h.cfg.Setup = cmd
		for i, id := range []string{"A", "B", "C"} {
			h.beads.add(id, "ticket "+id, i+1) // no behaviours: a worker on any fails the test
		}
		o, code := h.run()
		if code != ExitEnvironment {
			t.Fatalf("exit %d, want %d\n%s", code, ExitEnvironment, h.sink.text())
		}
		final := "ENVIRONMENT: the setup '" + cmd + "' failed in the worktrees of the last 2 tickets (A, B); " +
			"check the machine, then restart"
		if o.Final() != final {
			t.Errorf("final line %q, want %q", o.Final(), final)
		}
		if got := setUpIn(t, log); !equal(got, []string{"A", "B"}) {
			t.Errorf("the setup ran in %v, want A's and B's worktrees", got)
		}
		for _, id := range []string{"A", "B", "C"} {
			if st := h.statusOf(id); st != StatusOpen {
				t.Errorf("%s is %s, want open", id, st)
			}
			if n := h.beads.notesOf(id); strings.Contains(n, "reopened") {
				t.Errorf("%s was reopened:\n%s", id, n)
			}
		}
		if got := h.sink.of(EvReopened); len(got) != 0 {
			t.Errorf("reopened %v", got)
		}
		if aside := o.asideList(); len(aside) != 0 {
			t.Errorf("set aside: %v", aside)
		}
	})
}

// A worker failing at once and a setup failing after it are one row: the run holds, saying both.
func TestAWorkerFailingAtOnceAndAFailedSetupHoldTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		cmd, _ := recordsSetups(t, failsIn("B", filepath.Join(t.TempDir(), "never")))
		h.cfg.Setup = cmd
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3)
		h.worker("A", givesUp)
		o, code := h.run()
		if code != ExitEnvironment {
			t.Fatalf("exit %d, want %d\n%s", code, ExitEnvironment, h.sink.text())
		}
		final := "ENVIRONMENT: the last 2 tickets (A, B) each failed at once: the setup '" + cmd + "' failed in " +
			"their worktree, or their worker settled within 1m of starting without being claimed or changed; " +
			"check the machine, then restart"
		if o.Final() != final {
			t.Errorf("final line %q, want %q", o.Final(), final)
		}
		// A's worker failed at once: reopened. B's setup failed: back in the queue already.
		if n := h.beads.notesOf("A"); !strings.Contains(n, "reopened") {
			t.Errorf("A's notes:\n%s", n)
		}
		if got := h.sink.of(EvReopened); len(got) != 1 || !strings.HasPrefix(got[0], "A ") {
			t.Errorf("reopened %v, want A alone", got)
		}
	})
}

// A setup set during the run counts from then on: a ticket whose merge sets setup and setup_files in
// .orchestra/settings.json has the next new worktree set up with them, as committed, and its worker
// told so. One whose settings.json doesn't parse is warned of, and the setup stays as it was.
func TestAMergeThatSetsTheSetupHasTheNextWorktreeSetUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, log := recordsSetups(t, "touch installed")
		setup, err := json.Marshal(cmd)
		if err != nil {
			t.Fatal(err)
		}
		settings := `{"setup": ` + string(setup) + `, "setup_files": ["bun.lock"]}`
		h.beads.add("A", "the environment", 1)
		h.beads.add("B", "after it", 2)
		h.beads.add("C", "last", 3)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			if err := h.mem.commitFile(w.wt, "A: set the setup", project.SettingsFile, settings); err != nil {
				t.Error(err)
			}
			w.close()
			return "idle"
		})
		var installed bool
		var rules string
		h.worker("B", func(w *fakeWorker) AgentState {
			installed, rules = exists(filepath.Join(w.wt, "installed")), readRun(t, w.wt, project.RulesName)
			w.claim()
			if err := h.mem.commitFile(w.wt, "B: break the settings", project.SettingsFile, "{"); err != nil {
				t.Error(err)
			}
			w.close()
			return "idle"
		})
		h.worker("C", finishes("c.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := setUpIn(t, log); !installed || !equal(got, []string{"B", "C"}) {
			t.Errorf("B's worker found the setup's work %v; it ran in %v, want B's and C's worktrees", installed, got)
		}
		if !strings.Contains(rules, "This worktree's dependencies are installed with '"+cmd+"'") {
			t.Errorf("B's rules don't name the setup:\n%s", rules)
		}
		ev := h.sink.text()
		taken := "  SETUP: A's merge changed .orchestra/settings.json on main: the setup is '" + cmd + "' from now on\n"
		if !strings.Contains(ev, taken) {
			t.Errorf("events lack %q:\n%s", taken, ev)
		}
		warned := "  SETTINGS_UNREAD: B's merge changed .orchestra/settings.json on main, but it could not be read: "
		if got := eventsOf(h, EvWarn); len(got) != 1 || !strings.HasPrefix(got[0].Text, warned) ||
			!strings.HasSuffix(got[0].Text, "; the setup stays '"+cmd+"'") {
			t.Errorf("warnings %+v, want B's settings unread", got)
		}
		if s := o.setupNow(); s.cmd != cmd || !equal(s.files, []string{"bun.lock"}) {
			t.Errorf("the setup is %+v, want %q reading bun.lock", s, cmd)
		}
	})
}

// A worker adopted is in its worktree already, set up as it started: nothing runs before it carries
// on, here once its question is answered and it closes its ticket in its tab.
func TestAnAdoptedWorkerRunsNoSetup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		cmd, log := recordsSetups(t, "")
		h.cfg.Setup = cmd
		h.beads.add("A", "asks", 1)
		h.beads.add("B", "answers", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, finishes("a.txt")))
		// B answers between two polls, so A's worker has closed its ticket by the next: one answered on a
		// poll might be seen idle first and told the news, starting it again.
		h.worker("B", answersAfter(5*time.Minute+7*time.Second, answered, time.Minute, "b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); len(got) != 2 {
			t.Errorf("merged %v, want A and B\n%s", got, h.sink.text())
		}
		if starts := len(h.herdr.argsFor("A")); starts != 1 || !strings.Contains(h.sink.text(), "adopting it") {
			t.Errorf("A's worker started %d times; want once, and adopted after\n%s", starts, h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("pasted to %v; a worker that closed its ticket has nothing to be told", got)
		}
		if got := slices.Sorted(slices.Values(setUpIn(t, log))); !equal(got, []string{"A", "B"}) {
			t.Errorf("the setup ran in %v, want A's and B's new worktrees alone\n%s", got, h.sink.text())
		}
	})
}
