package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Handing a rebase that stops on conflicts back to the ticket's worker, in whole runs.

// conflictHarness is a run whose tickets edit shared.txt, with a check that fails on conflict
// markers left in it and hand-backs on.
func conflictHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "shared file")
	h.cfg.Check = "! grep -q '<<<<<<<' shared.txt"
	h.cfg.ResolveConflicts = true
	h.cfg.ResolveTimeout = patience
	return h
}

// gitIn runs git in dir for a worker, failing its test on an error.
func (w *fakeWorker) gitIn(dir string, args ...string) {
	if out, err := command.Output(context.Background(), 0, dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "core.editor=true"}, args...)...); err != nil {
		w.t.Errorf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// write writes file in dir with content.
func (w *fakeWorker) write(dir, file, content string) {
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		w.t.Error(err)
	}
}

// landsOnMain commits content to file on main, as a ticket merged meanwhile would.
func landsOnMain(w *fakeWorker, repo, file, content string) {
	w.write(repo, file, content)
	w.gitIn(repo, "add", file)
	w.gitIn(repo, "commit", "-q", "-m", "landed on main: "+file)
}

// conflicting claims the ticket, has main change shared.txt while it works, commits its own
// change to it and closes the ticket.
func conflicting(repo string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		landsOnMain(w, repo, "shared.txt", "main\n")
		w.commit("shared.txt")
		w.close()
		return "idle"
	}
}

// resolvesWith resolves the stopped rebase with shared.txt as content and finishes it.
func resolvesWith(content string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.write(w.wt, "shared.txt", content)
		w.gitIn(w.wt, "add", "shared.txt")
		w.gitIn(w.wt, "rebase", "--continue")
		return "idle"
	}
}

func rebaseInProgress(t *testing.T, wt string) bool {
	t.Helper()
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		out, err := command.Output(context.Background(), 0, wt, "git", "rev-parse", "--path-format=absolute", "--git-path", d)
		if err != nil {
			t.Fatal(err)
		}
		if exists(strings.TrimSpace(out)) {
			return true
		}
	}
	return false
}

func TestAConflictResolvedByItsWorkerMerges(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo), resolvesWith("main\nA\n"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{
		"RESOLVING: wt/A conflicts with main in shared.txt; handed back to its worker in tab tab1",
		"'! grep -q '<<<<<<<' shared.txt' passes on wt/A as its worker resolved it",
		"A closed",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in events:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "MERGE_CONFLICT") {
		t.Errorf("events:\n%s", ev)
	}
	if got := read(t, filepath.Join(h.repo, "shared.txt")); got != "main\nA\n" {
		t.Errorf("shared.txt on main = %q", got)
	}
	if !equal(h.herdr.pastedTo(), []string{"A"}) {
		t.Errorf("prompts pasted to %v, want the hand-back to A", h.herdr.pastedTo())
	}
	if exists(h.worktree("A")) {
		t.Error("A's worktree should be removed once merged")
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A should not be labelled %q", UnmergedLabel)
	}
}

// A resolution orchestra can't verify leaves the ticket set aside as before, with its branch put
// back as the ticket closed it and a note saying resolution was tried and why it failed.
func TestAFailedResolutionSetsTheTicketAside(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		resolve behaviour
		why     string
	}{
		{"extra commit", func(w *fakeWorker) AgentState {
			resolvesWith("main\nA\n")(w)
			w.commit("extra.txt")
			return "idle"
		}, "wt/A has 2 commits where the ticket had 1 (a commit made besides the rebase?)"},
		{"markers left", resolvesWith("<<<<<<< ours\nmain\n=======\nA\n>>>>>>> theirs\n"), "'! grep -q '<<<<<<<' shared.txt' fails on the resolved wt/A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := conflictHarness(t)
			h.beads.add("A", "first", 1)
			h.worker("A", conflicting(h.repo), tc.resolve)
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			ev := h.sink.text()
			if !strings.Contains(ev, "MERGE_CONFLICT: A closed, but wt/A conflicts with main") || !strings.Contains(ev, "handed back to its worker, but "+tc.why) {
				t.Errorf("events:\n%s", ev)
			}
			if n := h.beads.notesOf("A"); !strings.Contains(n, "its worker was asked to resolve the rebase, but "+tc.why) {
				t.Errorf("notes:\n%s", n)
			}
			if got := read(t, filepath.Join(h.repo, "shared.txt")); got != "main\n" {
				t.Errorf("main must not change: shared.txt = %q", got)
			}
			wt := h.worktree("A")
			if rebaseInProgress(t, wt) {
				t.Error("the rebase should be aborted")
			}
			if st := h.git(wt, "status", "--porcelain"); st != "" {
				t.Errorf("the worktree should be left clean: %q", st)
			}
			if log := h.git(h.repo, "log", "--format=%s", "main..wt/A"); log != "A: add shared.txt\n" {
				t.Errorf("wt/A should be as the ticket closed it:\n%s", log)
			}
			if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
				t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
			}
		})
	}
}

// A hand-back its worker doesn't finish in time sets the ticket aside as a failed resolution does:
// a worker idle with the rebase still stopped once its idle grace is over, and one still working at
// the hand-back's time limit.
func TestAHandBackNotFinishedInTimeSetsTheTicketAside(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		resolve behaviour
		after   time.Duration
		why     string
	}{
		{"unfinished", func(w *fakeWorker) AgentState { return "idle" }, idleGrace, "its worker left the rebase unfinished"},
		{"timed out", func(w *fakeWorker) AgentState { return "working" }, project.DefaultResolveTimeout,
			"its worker was still working after 20m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.cfg.Check = "false" // never run: no resolution gets that far
				h.cfg.ResolveConflicts = true
				h.mem.conflict("wt/A", "shared.txt")
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					w.claim()
					w.git.commit("main", "landed on main: shared.txt", "shared.txt")
					w.commit("shared.txt")
					w.close()
					return "idle"
				}, tc.resolve)
				var handedBack time.Time
				h.herdr.onPrompt = func(id string) { handedBack = time.Now() }
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if took := time.Since(handedBack); took < tc.after || took > tc.after+2*statusPoll {
					t.Errorf("set aside %s after the hand-back, want %s", took, tc.after)
				}
				ev := h.sink.text()
				if !strings.Contains(ev, "MERGE_CONFLICT: A closed, but wt/A conflicts with main") ||
					!strings.Contains(ev, "handed back to its worker, but "+tc.why+"; the rebase was aborted") {
					t.Errorf("events:\n%s", ev)
				}
				if n := h.beads.notesOf("A"); !strings.Contains(n, "its worker was asked to resolve the rebase, but "+tc.why) {
					t.Errorf("notes:\n%s", n)
				}
				if log := h.mainLog(); strings.Contains(log, "A: add") {
					t.Errorf("main must not change:\n%s", log)
				}
				if h.mem.RebaseInProgress(t.Context(), h.worktree("A")) {
					t.Error("the rebase should be aborted")
				}
				if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
					t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
				}
			})
		})
	}
}

func TestAConflictIsNotHandedBackWithoutACheckCommand(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.Check = ""
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo))
	h.run()
	if ev := h.sink.text(); !strings.Contains(ev, "not handed back to its worker: there is no check command in .orchestra/settings.json") || strings.Contains(ev, "RESOLVING") {
		t.Errorf("events:\n%s", ev)
	}
	if p := h.herdr.pastedTo(); len(p) > 0 {
		t.Errorf("nothing should be pasted: %v", p)
	}
}

func TestAConflictIsNotHandedBackWhenItsWorkerIsGone(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		conflicting(h.repo)(w)
		return "gone"
	})
	h.run()
	if ev := h.sink.text(); !strings.Contains(ev, "not handed back to its worker: its worker is gone") || strings.Contains(ev, "RESOLVING") {
		t.Errorf("events:\n%s", ev)
	}
	if rebaseInProgress(t, h.worktree("A")) {
		t.Error("the rebase should be aborted")
	}
}

func TestAConflictIsNotHandedBackWhenTurnedOff(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.ResolveConflicts = false
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo))
	h.run()
	if ev := h.sink.text(); !strings.Contains(ev, "not handed back to its worker: resolving conflicts is turned off") {
		t.Errorf("events:\n%s", ev)
	}
}

// Base moving on while the worker resolves means another rebase: cleanly, checked again; or on a
// new conflict, handed back a second time.
func TestBaseMovingDuringAResolutionIsRebasedAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		file, text string
		more       []behaviour
		wantShared string
	}{
		{"cleanly", "other.txt", "other\n", nil, "main\nA\n"},
		{"with a new conflict", "shared.txt", "main 2\n", []behaviour{resolvesWith("main 2\nA\n")}, "main 2\nA\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := conflictHarness(t)
			h.beads.add("A", "first", 1)
			h.worker("A", conflicting(h.repo), func(w *fakeWorker) AgentState {
				landsOnMain(w, h.repo, tc.file, tc.text)
				return resolvesWith("main\nA\n")(w)
			})
			h.worker("A", tc.more...)
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			ev := h.sink.text()
			if !strings.Contains(ev, "A closed") || strings.Contains(ev, "MERGE_CONFLICT") {
				t.Errorf("events:\n%s", ev)
			}
			if n := strings.Count(ev, "RESOLVING: wt/A"); n != 1+len(tc.more) {
				t.Errorf("%d hand-backs, want %d:\n%s", n, 1+len(tc.more), ev)
			}
			if len(tc.more) == 0 && !strings.Contains(ev, "rebased wt/A onto main") {
				t.Errorf("A should be rebased again:\n%s", ev)
			}
			if got := read(t, filepath.Join(h.repo, "shared.txt")); got != tc.wantShared {
				t.Errorf("shared.txt on main = %q", got)
			}
			if !strings.Contains(h.mainLog(), "landed on main: "+tc.file) {
				t.Errorf("history:\n%s", h.mainLog())
			}
		})
	}
}

// The merge queue is free while a worker resolves: another finished ticket merges meanwhile.
func TestOtherTicketsMergeWhileOneIsResolving(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.Concurrency, h.cfg.NoFootprint = 2, true
	resolving := make(chan struct{})
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", conflicting(h.repo), func(w *fakeWorker) AgentState {
		close(resolving)
		eventually(t, "B never merged while A was resolving", func() bool {
			out, _ := command.Output(context.Background(), 0, h.repo, "git", "log", "--format=%s", "main")
			return strings.Contains(out, "B: add b.txt")
		})
		return resolvesWith("main\nA\n")(w)
	})
	h.worker("B", func(w *fakeWorker) AgentState {
		w.claim()
		<-resolving
		w.commit("b.txt")
		w.close()
		return "idle"
	})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	log := h.mainLog()
	if !strings.Contains(log, "A: add shared.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("history:\n%s", log)
	}
	if ev := h.sink.text(); strings.Index(ev, "B closed") > strings.Index(ev, "A closed") {
		t.Errorf("B should merge before A:\n%s", ev)
	}
}

// Ctrl+C during a hand-back leaves the rebase in progress for the worker to finish, and says so.
func TestInterruptDuringAResolutionLeavesTheRebase(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.beads.add("A", "first", 1)
	started, release := make(chan struct{}), make(chan struct{})
	h.worker("A", conflicting(h.repo), func(w *fakeWorker) AgentState { close(started); <-release; return "idle" })
	defer close(release)
	o := h.loop()
	o.ReportInterrupt = true
	ctx, cancel := context.WithCancel(context.Background())
	codes := make(chan int, 1)
	go func() { codes <- o.Run(ctx) }()
	<-started
	cancel()
	select {
	case code := <-codes:
		if code != ExitInterrupted || !strings.Contains(o.Final(), "A (tab tab1, resolving conflicts: its rebase is left in progress") {
			t.Errorf("exit %d, final %q", code, o.Final())
		}
	case <-time.After(patience):
		t.Fatal("Run did not return after Ctrl+C")
	}
	if !rebaseInProgress(t, h.worktree("A")) {
		t.Error("the rebase should be left in progress")
	}
	if strings.Contains(h.sink.text(), "MERGE_CONFLICT") {
		t.Errorf("events:\n%s", h.sink.text())
	}
}
