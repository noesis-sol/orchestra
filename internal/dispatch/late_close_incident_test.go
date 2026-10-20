package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The tutti run of 2026-10-16/17, from 04:25 to 08:35: three workers at the account's usage limit
// wrapped up after Claude Code's grace note and ended their turns normally, two deferring their own
// tickets; the run set them aside and stopped watching them. Claude Code continued each by itself at
// the reset, and each closed its ticket, which nothing merged; nine tickets blocked by one of them
// started on a main without its code. Played here on a backlog of the same shape, at concurrency 4.

// The tutti run's tickets: three its workers took to the usage limit, the tickets the last two
// block, and two beside them.
const (
	tuttiOpen     = "nrg.1.7"  // its worker leaves it in progress at the limit
	tuttiDefers   = "nrg.3.10" // its worker defers it at the limit; it blocks tuttiAfter310
	tuttiBlocker  = "nrg.4.14" // its worker defers it at the limit; it blocks tuttiAfter414
	tuttiAfter310 = "nrg.8.3"
	tuttiFirst    = "nrg.2.1" // finishes before the limit, moving main on
	tuttiBeside   = "nrg.2.2" // takes the slot tuttiFirst leaves
)

// tuttiAfter414 is the tickets tuttiBlocker blocks.
var tuttiAfter414 = []string{"nrg.5.2", "nrg.7.4", "nrg.7.6"}

// tuttiLimited is the three tickets whose workers the usage limit reached, in the order they stop.
var tuttiLimited = []string{tuttiOpen, tuttiDefers, tuttiBlocker}

// tuttiBacklog gives h's bd the tutti run's backlog, and the blocked tickets their workers: each
// checks its branch was cut from a main with its blocker's last commit, then finishes.
func tuttiBacklog(h *harness) {
	h.cfg.Concurrency = 4
	h.cfg.Check = "true"
	h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = project.DefaultEnvironmentHoldCount, project.DefaultEnvironmentHoldWindow
	for _, id := range []string{tuttiOpen, tuttiDefers, tuttiBlocker, tuttiFirst} {
		h.beads.add(id, "part of the night's work", 1)
	}
	h.beads.add(tuttiBeside, "waits for a slot", 2)
	h.beads.add(tuttiAfter310, "needs "+tuttiDefers, 1)
	h.beads.link(tuttiAfter310, tuttiDefers, "blocks")
	h.worker(tuttiAfter310, cutWith(tuttiDefers+": add "+tuttiDefers+"-2.txt", tuttiAfter310+".txt"))
	for _, id := range tuttiAfter414 {
		h.beads.add(id, "needs "+tuttiBlocker, 1)
		h.beads.link(id, tuttiBlocker, "blocks")
		h.worker(id, cutWith(tuttiBlocker+": add "+tuttiBlocker+"-2.txt", id+".txt"))
	}
}

// tuttiBlocked is each ticket a tutti ticket blocks, with its blocker.
func tuttiBlocked() map[string]string {
	m := map[string]string{tuttiAfter310: tuttiDefers}
	for _, id := range tuttiAfter414 {
		m[id] = tuttiBlocker
	}
	return m
}

// continuedAtReset is a tutti worker at the usage limit: after stopAfter, told by Claude Code's grace
// note that the limit is reached, it commits its work in progress, leaves its ticket as leave does (nil:
// in progress) and ends its turn with a Stop, its transcript saying Claude Code continues by itself at
// reset. Claude Code continues it goOn after the reset: it takes its ticket up again (bd undefer, bd
// update --claim), works for took, commits and closes it, and ends its turn a minute later.
func continuedAtReset(hooks *hookReporter, leave func(w *fakeWorker), stopAfter time.Duration, reset time.Time,
	goOn, took time.Duration) behaviour {
	return func(w *fakeWorker) AgentState {
		time.Sleep(stopAfter)
		w.shows(wrapsUpAtLimit(hooks, w.id+".txt", leave, reset)(w))
		time.Sleep(time.Until(reset.Add(goOn)))
		hooks.report(w.wt, "PreToolUse")
		w.shows(StateWorking)
		w.claim()
		time.Sleep(took)
		w.commit(w.id + "-2.txt")
		w.close()
		time.Sleep(time.Minute) // sums up its work: the tickets it blocks are ready meanwhile
		hooks.report(w.wt, "Stop")
		return StateIdle
	}
}

// settlesOnceHeld is a worker that ends its turn once the run holds for the usage limit, its ticket
// open and nothing done: it is set aside, and its triage is refused for the limit.
func settlesOnceHeld(h *harness, hooks *hookReporter) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		for start := time.Now(); len(h.sink.eventsOf(EvLimited)) == 0 && time.Since(start) < time.Hour; {
			time.Sleep(time.Second)
		}
		hooks.report(w.wt, "Stop")
		return StateIdle
	}
}

// openTabs is the tabs the terminal has open, by label.
func openTabs(h *harness) []string {
	h.herdr.mu.Lock()
	defer h.herdr.mu.Unlock()
	var labels []string
	for _, label := range h.herdr.labels {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return labels
}

// checkMergedInTurn checks that each tutti ticket was rebased onto main, checked and merged, and that
// each ticket they block was dispatched once, after its blocker merged, on a main with its code.
func checkMergedInTurn(t *testing.T, h *harness, merged []string) {
	t.Helper()
	log := h.logged()
	for _, id := range merged {
		for _, want := range []string{"rebased wt/" + id + " onto main, which moved on while it ran",
			"'true' passes on the rebased wt/" + id} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %q:\n%s", want, log)
			}
		}
		if !strings.Contains(h.mainLog(), id+": add "+id+"-2.txt") {
			t.Errorf("main lacks %s's last commit:\n%s", id, h.mainLog())
		}
	}
	ev := h.sink.text()
	for id, blocker := range tuttiBlocked() {
		if want := "  " + id + " waits: waiting for " + blocker + " to merge"; !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
		if n := dispatches(h, id); n != 1 {
			t.Errorf("%s dispatched %d times, want once", id, n)
		}
		if mergedAt, started := eventIndex(h, EvClosed, blocker), eventIndex(h, EvDispatch, id); mergedAt < 0 ||
			started < mergedAt {
			t.Errorf("%s dispatched (event %d) before %s merged (event %d):\n%s", id, started, blocker, mergedAt, ev)
		}
	}
	if h.sink.heldFor(stopEnvironment) {
		t.Errorf("the run held for the environment:\n%s", ev)
	}
	if tabs := openTabs(h); len(tabs) != 0 {
		t.Errorf("tabs left open at the end: %v", tabs)
	}
}

// The tutti run as it should have gone. Three workers wrap up after Claude Code's grace note, within a
// minute of each other, and end their turns normally, two deferring their own tickets; the triage of a
// fourth, set aside meanwhile, is refused for the limit. None of the three is set aside: the run holds
// for the limit from the first of them, and once it resets Claude Code continues each by itself, and
// each closes its ticket. Each is then rebased, checked and merged, and the tickets they block start
// only after their blocker merges.
func TestTuttiWorkersContinuedAfterTheLimitMergeBeforeTheirDependents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.UsageLimitWait = 6 * time.Hour
		tuttiBacklog(h)
		start := time.Now()
		reset := start.Add(time.Hour)
		leaves := map[string]func(w *fakeWorker){tuttiDefers: (*fakeWorker).deferIt, tuttiBlocker: (*fakeWorker).deferIt}
		for i, id := range tuttiLimited {
			h.worker(id, continuedAtReset(hooks, leaves[id], time.Duration(20+15*i)*time.Second, reset,
				time.Duration(20+15*i)*time.Second, time.Duration(2+4*i)*time.Minute))
		}
		h.worker(tuttiFirst, busyFor(10*time.Second, tuttiFirst+".txt"))
		h.worker(tuttiBeside, settlesOnceHeld(h, hooks))

		o := h.loop()
		o.organ = organ.Client{Bin: limitedClaude(t)}
		o.StartTriage()
		code := o.Run(t.Context())
		o.FinishTriage(t.Context())

		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev, log := h.sink.text(), h.logged()
		for _, id := range tuttiLimited {
			for _, not := range []string{id + " deferred by worker", id + " still open", id + " set aside"} {
				if strings.Contains(ev, not) {
					t.Errorf("events say %q:\n%s", not, ev)
				}
			}
			if got := h.herdr.typedTo(id); len(got) != 0 {
				t.Errorf("typed to %s: %q, want nothing: Claude Code continued it", id, got)
			}
		}
		if got := o.asideList(); !equal(got, []string{tuttiBeside}) {
			t.Errorf("set aside: %v, want %s alone", got, tuttiBeside)
		}
		held := h.sink.eventsOf(EvLimited)
		if len(held) != 1 || held[0].Ticket != tuttiOpen || !held[0].Reset.Equal(reset) {
			t.Errorf("usage limit holds: %+v, want one, from %s's worker, to %s", held, tuttiOpen, reset)
		}
		if lifted := h.sink.eventsOf(EvLimitLift); len(lifted) != 1 || lifted[0].Time.Before(reset) {
			t.Errorf("usage limit lifts: %+v, want one, after %s", lifted, reset)
		}
		if want := "triage of " + tuttiBeside + " skipped: claude is at the account's usage limit"; !strings.Contains(
			log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
		if at := h.sink.dispatchedAt(tuttiBeside); at.IsZero() || len(held) > 0 && at.After(held[0].Time) {
			t.Errorf("%s started at %s, want before the hold", tuttiBeside, at)
		}
		for _, id := range []string{tuttiDefers, tuttiBlocker} {
			if !strings.Contains(h.beads.notesOf(id), "brought back in progress") {
				t.Errorf("%s's notes don't say it was brought back: %q", id, h.beads.notesOf(id))
			}
		}
		checkMergedInTurn(t, h, tuttiLimited)
	})
}

// defersThenCloses is a worker that commits its work in progress, defers its ticket and ends its turn
// after stopAfter, then carries on in its tab at closeAt: it takes its ticket up again, works for a
// while, commits and closes it, and ends its turn a minute later.
func defersThenCloses(hooks *hookReporter, stopAfter time.Duration, closeAt time.Time) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		w.claim()
		time.Sleep(stopAfter)
		w.commit(w.id + ".txt")
		w.deferIt()
		hooks.report(w.wt, "Stop")
		w.shows(StateIdle)
		time.Sleep(time.Until(closeAt))
		hooks.report(w.wt, "PreToolUse")
		w.shows(StateWorking)
		w.claim()
		time.Sleep(2 * time.Minute)
		w.commit(w.id + "-2.txt")
		w.close()
		time.Sleep(time.Minute) // sums up its work: the tickets it blocks are ready meanwhile
		hooks.report(w.wt, "Stop")
		return StateIdle
	}
}

// The tutti backlog with the three tickets set aside the old way, each worker deferring its ticket with
// no usage limit and left in its tab, then closing it there later: each is merged as a late close,
// rebased and checked, and the tickets it blocks wait for that merge.
func TestTuttiTicketsClosedInTheirLeftTabsMergeBeforeTheirDependents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		tuttiBacklog(h)
		start := time.Now()
		for i, id := range tuttiLimited {
			h.worker(id, defersThenCloses(hooks, time.Duration(20+15*i)*time.Second,
				start.Add(time.Hour+time.Duration(4*i)*time.Minute)))
		}
		h.worker(tuttiFirst, busyFor(10*time.Second, tuttiFirst+".txt"))
		h.worker(tuttiBeside, busyFor(2*time.Hour, tuttiBeside+".txt")) // keeps the run going

		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, id := range tuttiLimited {
			tab := tabOf(h, id)
			for _, want := range []string{
				"  " + id + " deferred by worker; worktree " + h.worktree(id) + " and tab " + tab + " left open",
				"  LATE_CLOSE: " + id + " was closed in tab " + tab + " after it was set aside; merging it now",
			} {
				if strings.Count(ev, want) != 1 {
					t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
				}
			}
			for blocked, blocker := range tuttiBlocked() {
				want := "  " + blocked + " waits: " + id + " closed but not merged (its worker was left in tab " + tab + ")"
				if got := strings.Count(ev, want); blocker == id && got != 1 {
					t.Errorf("events say %q %d times, want once:\n%s", want, got, ev)
				}
			}
			if a, _ := h.beads.Show(t.Context(), id); HasLabel(a, UnmergedLabel) {
				t.Errorf("%s, merged, still has its '%s' label", id, UnmergedLabel)
			}
		}
		if got := o.leftOpenList(); len(got) != 0 {
			t.Errorf("still followed as left open: %v", got)
		}
		checkMergedInTurn(t, h, tuttiLimited)
	})
}

// The tutti backlog with tuttiBlocker closed by hand before the run, its work only on wt/nrg.4.14: the
// tickets it blocks wait, each saying why, with one CLOSED_UNMERGED warning for the three; the rest of
// the backlog runs and merges.
func TestTuttiBlockerClosedByHandOffMainHoldsItsDependents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		tuttiBacklog(h)
		h.beads.set(tuttiBlocker, StatusClosed)
		cutBranch(h.mem, branchOf(tuttiBlocker))
		h.mem.commit(branchOf(tuttiBlocker), tuttiBlocker+": add "+tuttiBlocker+"-2.txt", tuttiBlocker+"-2.txt")
		h.worker(tuttiOpen, finishes(tuttiOpen+".txt"))
		h.worker(tuttiDefers, func(w *fakeWorker) AgentState {
			w.claim()
			w.commit(w.id + "-2.txt")
			w.close()
			return StateIdle
		})
		h.worker(tuttiFirst, finishes(tuttiFirst+".txt"))
		h.worker(tuttiBeside, finishes(tuttiBeside+".txt"))

		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		merged := slices.Sorted(slices.Values(closedIDs(h)))
		if want := []string{tuttiFirst, tuttiBeside, tuttiOpen, tuttiDefers, tuttiAfter310}; !equal(merged,
			slices.Sorted(slices.Values(want))) {
			t.Errorf("merged %v, want %v", merged, want)
		}
		ev := h.sink.text()
		for _, id := range tuttiAfter414 {
			if n := dispatches(h, id); n != 0 {
				t.Errorf("%s dispatched %d times, want none: %s isn't on main", id, n, tuttiBlocker)
			}
			want := "  " + id + " waits: " + tuttiBlocker + " closed but not merged (wt/" + tuttiBlocker + " is not on main)"
			if strings.Count(ev, want) != 1 {
				t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
			}
		}
		if n := len(h.sink.matching(EvWarn, "CLOSED_UNMERGED", "", "")); n != 1 {
			t.Errorf("%d CLOSED_UNMERGED warnings, want one:\n%s", n, ev)
		}
		if h.sink.heldFor(stopEnvironment) {
			t.Errorf("the run held for the environment:\n%s", ev)
		}
		if tabs := openTabs(h); len(tabs) != 0 {
			t.Errorf("tabs left open at the end: %v", tabs)
		}
	})
}
