package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Claude Code may let a turn run a little past the account's usage limit, telling the worker so (its grace
// note): the worker wraps up and ends its turn normally, with a Stop, and its transcript says Claude Code
// continues by itself once the limit resets. Such a worker waits in the run's hold as one the limit cut off.

// graceWords is Claude Code's notice, in the transcript, that it continues by itself once the limit resets.
const graceWords = "Usage limit reached · continuing automatically at 5:30am · esc to cancel"

// graceStop reports, as the hooks of the worker in wt do, a turn that ended just now with a Stop after
// Claude Code's grace note, its transcript saying Claude Code continues by itself at reset.
func (r *hookReporter) graceStop(wt string, reset time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]ToolUse{}
	}
	now := time.Now()
	r.last[wt] = ToolUse{Event: "Stop", At: now, Limit: UsageLimit{At: now, Reset: reset, Message: graceWords}}
}

// wrapsUpAtLimit is a worker turn that claims its ticket and commits file, then, told by Claude Code that
// the usage limit is reached, leaves its ticket as leave does (nil: in progress) and ends its turn with a
// Stop, Claude Code to continue by itself at reset.
func wrapsUpAtLimit(hooks *hookReporter, file string, leave func(w *fakeWorker), reset time.Time) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		w.claim()
		w.commit(file)
		if leave != nil {
			leave(w)
		}
		hooks.graceStop(w.wt, reset)
		return StateLimited
	}
}

// statusWhenTold is a worker turn that records its ticket's status as it is told to carry on, then does b.
func statusWhenTold(status *TicketStatus, b behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		*status, _ = w.beads.Status(context.Background(), w.id)
		return b(w)
	}
}

// A worker that ends its turn normally at the usage limit, after Claude Code's grace note, with its ticket
// left in progress or deferred by itself, isn't set aside: the run holds for the limit until the reset, the
// ticket ready meanwhile waiting, and the same worker is told to carry on afterwards and finishes. A ticket
// it deferred is brought back in progress, with a note, before it is told.
func TestAWorkerStoppedAfterTheGraceNoteWaitsForTheReset(t *testing.T) {
	t.Parallel()
	for name, leave := range map[string]func(w *fakeWorker){"in progress": nil, "deferred": (*fakeWorker).deferIt} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.cfg.Concurrency = 2
				h.cfg.UsageLimitWait = 6 * time.Hour
				h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = 1, 2*time.Minute // one worker set aside would hold the run
				start := time.Now()
				reset := start.Add(time.Hour)
				h.beads.add("A", "wraps up at the limit", 1)
				var told TicketStatus
				h.worker("A", func(w *fakeWorker) AgentState {
					readyOnceHeld(h, "B", "ready while the run holds", 2)
					return wrapsUpAtLimit(hooks, "a.txt", leave, reset)(w)
				}, statusWhenTold(&told, carriesOn(hooks, "a2.txt", 2*time.Minute)))
				h.worker("B", finishes("b.txt"))
				o, code := h.run()
				if code != ExitOK {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				for _, f := range []string{"A: add a.txt", "A: add a2.txt", "B: add b.txt"} {
					if !strings.Contains(h.mainLog(), f) {
						t.Errorf("main lacks %q:\n%s", f, h.mainLog())
					}
				}
				if b := h.sink.dispatchedAt("B"); b.Before(reset.Add(resetGrace)) {
					t.Errorf("B started at %s, before the reset and its minute (%s)", b, reset.Add(resetGrace))
				}
				if got := h.herdr.typedTo("A"); !equal(got, []string{"esc", "prompt"}) {
					t.Errorf("typed to A: %q, want Escape and then one message", got)
				}
				if told != StatusInProgress {
					t.Errorf("A was %s as its worker was told to carry on, want in progress", told)
				}
				held := h.sink.eventsOf(EvLimited)
				if len(held) != 1 || held[0].Ticket != "A" || !held[0].Reset.Equal(reset) || held[0].Detail != graceWords {
					t.Errorf("usage limit events: %+v", held)
				}
				logged := h.logged()
				if want := "A's worker reached the usage limit; it resets at " + ResetLabel(reset, start); !strings.Contains(
					logged, want) {
					t.Errorf("the log lacks %q:\n%s", want, logged)
				}
				for _, not := range []string{"deferred by worker", "noted and deferred", "set aside", "ENVIRONMENT",
					"told it to continue", "triage"} {
					if strings.Contains(logged, not) {
						t.Errorf("the log has %q:\n%s", not, logged)
					}
				}
				brought := "A's worker deferred its ticket as it stopped at the usage limit; brought back in progress"
				if got := strings.Contains(logged, brought); got != (leave != nil) {
					t.Errorf("the log has %q: %v, want %v:\n%s", brought, got, leave != nil, logged)
				}
				if got := strings.Contains(h.beads.notesOf("A"), "brought back in progress"); got != (leave != nil) {
					t.Errorf("A's notes say it was brought back: %v, want %v: %q", got, leave != nil, h.beads.notesOf("A"))
				}
			})
		})
	}
}

// A worker that closed its ticket within Claude Code's grace allowance, then ended its turn at the limit, is
// done with it: it settles and merges at once, and the run, holding for nothing, goes on to the next ticket.
func TestAWorkerThatClosedWithinTheGraceAllowanceMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.UsageLimitWait = 6 * time.Hour
		start := time.Now()
		reset := start.Add(time.Hour)
		h.beads.add("A", "finished within the allowance", 1)
		h.beads.add("B", "next", 2)
		h.worker("A", wrapsUpAtLimit(hooks, "a.txt", (*fakeWorker).close, reset))
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, f := range []string{"A: add a.txt", "B: add b.txt"} {
			if !strings.Contains(h.mainLog(), f) {
				t.Errorf("main lacks %q:\n%s", f, h.mainLog())
			}
		}
		if took := time.Since(start); took > 10*time.Minute {
			t.Errorf("the run took %s, waiting on a limit its closed ticket has no need of", took)
		}
		if got := h.sink.eventsOf(EvLimited); len(got) != 0 {
			t.Errorf("the run held: %+v", got)
		}
		if got := h.herdr.typedTo("A"); len(got) != 0 {
			t.Errorf("typed to A: %q", got)
		}
	})
}

// continuesItself is a worker turn that wraps up at the usage limit as wrapsUpAtLimit does, then, Claude
// Code continuing it by itself after the reset, goes on, its ticket's status then in seen: it claims its
// ticket, commits a file and closes it.
func continuesItself(hooks *hookReporter, leave func(w *fakeWorker), reset time.Time, seen *TicketStatus) behaviour {
	return func(w *fakeWorker) AgentState {
		w.shows(wrapsUpAtLimit(hooks, w.id+".txt", leave, reset)(w))
		time.Sleep(time.Until(reset) + 30*time.Second) // Claude Code's continue fires a little after the reset
		*seen, _ = w.beads.Status(context.Background(), w.id)
		hooks.report(w.wt, "PreToolUse")
		w.shows(StateWorking)
		w.claim()
		time.Sleep(2 * time.Minute)
		w.commit(w.id + "-2.txt")
		w.close()
		hooks.report(w.wt, "Stop")
		return StateIdle
	}
}

// A worker Claude Code continues by itself after the reset is at work, and isn't told to carry on, nor sent
// the Escape that would cancel it: the hold lifts at the reset, and it finishes and merges. A ticket it
// deferred as it stopped is in progress again by then.
func TestAWorkerContinuedByClaudeCodeIsNotToldAgain(t *testing.T) {
	t.Parallel()
	for name, leave := range map[string]func(w *fakeWorker){"in progress": nil, "deferred": (*fakeWorker).deferIt} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.cfg.Concurrency = 2
				h.cfg.UsageLimitWait = 6 * time.Hour
				start := time.Now()
				reset := start.Add(time.Hour)
				h.beads.add("A", "continued by Claude Code", 1)
				var seen TicketStatus
				h.worker("A", func(w *fakeWorker) AgentState {
					readyOnceHeld(h, "B", "ready while the run holds", 2)
					return continuesItself(hooks, leave, reset, &seen)(w)
				})
				h.worker("B", finishes("b.txt"))
				o, code := h.run()
				if code != ExitOK {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				for _, f := range []string{"A: add A.txt", "A: add A-2.txt", "B: add b.txt"} {
					if !strings.Contains(h.mainLog(), f) {
						t.Errorf("main lacks %q:\n%s", f, h.mainLog())
					}
				}
				if got := h.herdr.typedTo("A"); len(got) != 0 {
					t.Errorf("typed to A: %q, want nothing", got)
				}
				if seen != StatusInProgress {
					t.Errorf("A was %s as Claude Code continued its worker, want in progress", seen)
				}
				lifted := h.sink.eventsOf(EvLimitLift)
				if len(lifted) != 1 || !strings.Contains(lifted[0].Text, "the limit reset at "+ResetLabel(reset, start)) {
					t.Errorf("lifted: %+v", lifted)
				}
				if b := h.sink.dispatchedAt("B"); b.Before(reset) {
					t.Errorf("B started at %s, before the reset (%s)", b, reset)
				}
				if h.sink.setAside("A") {
					t.Errorf("A was set aside:\n%s", h.logged())
				}
			})
		})
	}
}

// The organs share the account with the workers: one refused for the usage limit starts the run's hold,
// with the reset its words give, when the run holds for nothing; while it holds, another refusal starts no
// other hold.
func TestAnOrganRefusedForTheLimitStartsTheHold(t *testing.T) {
	t.Parallel()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{UsageLimitWait: 6 * time.Hour}, log, "",
		Deps{Advisor: organ.Client{Bin: limitedClaude(t)}, AdviceCtx: context.Background()})
	sink := &recordSink{}
	o.sink = sink
	o.StartTriage()
	before := time.Now()
	o.queueTriage(context.Background(), organ.Deferral{ID: "A"})
	o.queueTriage(context.Background(), organ.Deferral{ID: "B"})
	o.FinishTriage(context.Background())
	if !o.usage.holds() {
		t.Fatalf("the run doesn't hold:\n%s", sink.text())
	}
	o.usage.mu.Lock()
	reset := o.usage.reset
	o.usage.mu.Unlock()
	if want := LimitReset(limitWords, before); !reset.Equal(want) {
		t.Errorf("the hold's reset is %s, want %s, as Claude Code's words say", reset, want)
	}
	var held []Event
	sink.mu.Lock()
	for _, ev := range sink.events {
		if ev.Kind == EvLimited {
			held = append(held, ev)
		}
	}
	sink.mu.Unlock()
	want := "USAGE_LIMIT: triage of A was refused for the usage limit; it resets at " + ResetLabel(reset, before) +
		". No new tickets start until then, for up to 6h"
	if len(held) != 1 || held[0].Text != want || held[0].Ticket != "" || !held[0].Reset.Equal(reset) ||
		held[0].Detail != limitWords {
		t.Errorf("usage limit events: %+v, want one saying %q", held, want)
	}
}
