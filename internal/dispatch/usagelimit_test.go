package dispatch

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// limitWords is what Claude Code 2.1.301 says of the usage limit.
const limitWords = "You've hit your session limit · resets 7pm (Europe/Athens)"

// limit reports, as the hooks of the worker in wt do, that its turn ended on the usage limit just now,
// which resets at reset (zero: not known).
func (r *hookReporter) limit(wt string, reset time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]ToolUse{}
	}
	now := time.Now()
	r.last[wt] = ToolUse{Event: "StopFailure", At: now, Limit: UsageLimit{At: now, Reset: reset, Message: limitWords}}
}

// reachesLimit is a worker turn that claims its ticket, if claims, and ends on the usage limit, which
// resets at reset (zero: not known).
func reachesLimit(hooks *hookReporter, claims bool, reset time.Time) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		if claims {
			w.claim()
		}
		hooks.limit(w.wt, reset)
		return StateLimited
	}
}

// readyOnceHeld adds ticket id to h's bd once the run holds for the usage limit (its EvLimited event),
// a second after: a ticket that becomes ready while the run holds. One added as a worker reaches the
// limit may be taken before any poll sees the worker limited, by the run filling a free slot at once.
func readyOnceHeld(h *harness, id, title string, prio int) {
	go func() {
		for start := time.Now(); len(h.sink.eventsOf(EvLimited)) == 0 && time.Since(start) < time.Hour; {
			time.Sleep(time.Second)
		}
		time.Sleep(time.Second) // the run, parked meanwhile, has seen the hold
		h.beads.add(id, title, prio)
	}()
}

// carriesOn is a worker turn that goes on with its ticket for took, then claims it, commits file and
// closes it.
func carriesOn(hooks *hookReporter, file string, took time.Duration) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		time.Sleep(took)
		w.claim()
		w.commit(file)
		w.close()
		hooks.report(w.wt, "Stop")
		return "idle"
	}
}

// eventsOf returns the events of kind k the sink got, in order.
func (s *runSink) eventsOf(k Kind) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []Event
	for _, ev := range s.events {
		if ev.Kind == k {
			l = append(l, ev)
		}
	}
	return l
}

// dispatchedAt is when ticket id was dispatched, zero if it wasn't.
func (s *runSink) dispatchedAt(id string) time.Time {
	for _, ev := range s.eventsOf(EvDispatch) {
		if ev.Ticket == id {
			return ev.Time
		}
	}
	return time.Time{}
}

// pasteTimes records when each prompt was pasted to a worker, by ticket.
type pasteTimes struct {
	mu sync.Mutex
	at map[string][]time.Time
}

// on is the fake terminal's onPrompt, recording the time of each paste.
func (p *pasteTimes) on(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.at == nil {
		p.at = map[string][]time.Time{}
	}
	p.at[id] = append(p.at[id], time.Now())
}

// all is the times of the prompts pasted to any worker, in order.
func (p *pasteTimes) all() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	var l []time.Time
	for _, ts := range p.at {
		l = append(l, ts...)
	}
	slices.SortFunc(l, func(a, b time.Time) int { return a.Compare(b) })
	return l
}

// Workers that reach the usage limit hold the run: no new ticket starts, they are neither told to
// continue nor settled, nor counted toward the hold for the environment or their ticket limit. A
// minute after the reset each is told to carry on, Escape first, and the hold lifts: the ticket that
// waited starts, and all three merge. The hold is said once, notified once, and recorded in the
// event stream with its lifting.
func TestALimitedWorkerHoldsTheRunUntilTheReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 3
		h.cfg.UsageLimitWait = 6 * time.Hour
		h.cfg.TicketLimit = time.Hour                              // the limit's 2h don't count toward it
		h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = 1, 2*time.Minute // one worker failing at once would hold the run
		start := time.Now()
		reset := start.Add(2 * time.Hour)
		h.beads.add("A", "claimed, then limited", 1)
		h.beads.add("C", "limited before its claim", 2)
		h.worker("A", func(w *fakeWorker) AgentState {
			readyOnceHeld(h, "B", "ready while the run holds", 3)
			return reachesLimit(hooks, true, reset)(w)
		}, carriesOn(hooks, "a.txt", 2*time.Minute))
		h.worker("C", reachesLimit(hooks, false, reset), carriesOn(hooks, "c.txt", 2*time.Minute))
		h.worker("B", finishes("b.txt"))
		o := h.loop()
		o.log.Begin(h.repo, RunStart{Started: start, Version: "v1", Repo: h.repo, Branch: "main", Concurrency: 3})
		code := o.Run(context.Background())
		o.log.End(code)
		if err := o.log.Close(); err != nil {
			t.Fatal(err)
		}
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, f := range []string{"A: add a.txt", "B: add b.txt", "C: add c.txt"} {
			if !strings.Contains(h.mainLog(), f) {
				t.Errorf("main lacks %q:\n%s", f, h.mainLog())
			}
		}
		if b := h.sink.dispatchedAt("B"); b.Before(reset.Add(resetGrace)) {
			t.Errorf("B started at %s, before the reset and its minute (%s)", b, reset.Add(resetGrace))
		}
		for _, id := range []string{"A", "C"} {
			if got := h.herdr.typedTo(id); !equal(got, []string{"esc", "prompt"}) {
				t.Errorf("typed to %s: %q, want Escape and then one message", id, got)
			}
			if got := h.herdr.pastedText(id); len(got) != 1 ||
				!strings.Contains(got[0], "usage limit has reset. Carry on with "+id) {
				t.Errorf("pasted to %s: %q", id, got)
			}
		}

		logged := h.logged()
		for _, want := range []string{
			"'s worker reached the usage limit; it resets at " + ResetLabel(reset, start) +
				". No new tickets start until then, for up to 6h",
			"USAGE_LIMIT lifted: the limit reset at " + ResetLabel(reset, start),
			"A's worker was told to carry on after the usage limit", "C's worker was told to carry on after the usage limit",
		} {
			if !strings.Contains(logged, want) {
				t.Errorf("the log lacks %q:\n%s", want, logged)
			}
		}
		for _, not := range []string{"told it to continue", "TICKET_LIMIT", "ENVIRONMENT", "settled: idle for", "deferred"} {
			if strings.Contains(logged, not) {
				t.Errorf("the log has %q:\n%s", not, logged)
			}
		}
		held, lifted := h.sink.eventsOf(EvLimited), h.sink.eventsOf(EvLimitLift)
		if len(held) != 1 || !held[0].Reset.Equal(reset) || held[0].Detail != limitWords {
			t.Errorf("usage limit events: %+v", held)
		}
		if len(lifted) != 1 || lifted[0].Time.Before(reset.Add(resetGrace)) {
			t.Errorf("lifted events: %+v", lifted)
		}
		notice := "Usage limit reached · resets " + ResetLabel(reset, start)
		if n := strings.Count(strings.Join(h.alerts.list(), "\n"), notice); n != 1 {
			t.Errorf("notified %q %d times: %q", notice, n, h.alerts.list())
		}

		recs := records(t, h.repo)
		var limitRec, liftRec map[string]any
		for _, r := range recs {
			switch r["kind"] {
			case "usage_limit":
				limitRec = r
			case "usage_limit_lifted":
				liftRec = r
			}
		}
		if limitRec == nil || liftRec == nil {
			t.Fatalf("no usage_limit or usage_limit_lifted record: %v", recs)
		}
		has(t, limitRec, map[string]any{"reset": timeString(t, reset), "detail": limitWords, "text": held[0].Text})
		has(t, liftRec, map[string]any{"text": lifted[0].Text}, "reset", "ticket")
	})
}

// With no reset time known, one limited worker is told to carry on 15 minutes into the hold, then
// every 15 minutes while it reaches the limit again. Once one goes on past it, the hold lifts and the
// other is told too.
func TestAnUnknownResetIsTriedEvery15Minutes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		h.cfg.UsageLimitWait = 6 * time.Hour
		var paste pasteTimes
		h.herdr.onPrompt = paste.on
		var tells atomic.Int32
		afterTell := func(w *fakeWorker) AgentState {
			if tells.Add(1) == 1 { // the first try: the limit holds still
				hooks.limit(w.wt, time.Time{})
				return StateLimited
			}
			return carriesOn(hooks, w.id+".txt", 2*time.Minute)(w)
		}
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", reachesLimit(hooks, true, time.Time{}), afterTell, afterTell)
		h.worker("B", reachesLimit(hooks, true, time.Time{}), afterTell, afterTell)
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		held := h.sink.eventsOf(EvLimited)
		if len(held) != 1 || !held[0].Reset.IsZero() {
			t.Fatalf("usage limit events: %+v", held)
		}
		pastes := paste.all()
		if len(pastes) != 3 {
			t.Fatalf("%d messages pasted, want a try that fails, one that works, and the other worker's: %v",
				len(pastes), pastes)
		}
		tries := []time.Duration{pastes[0].Sub(held[0].Time), pastes[1].Sub(pastes[0])}
		for i, d := range tries {
			if d < limitRetry || d > limitRetry+time.Minute {
				t.Errorf("try %d came %s after the one before (or the hold), want 15m", i+1, d)
			}
		}
		if d := pastes[2].Sub(pastes[1]); d < limitCarried || d > limitCarried+time.Minute {
			t.Errorf("the other worker was told %s after the try that worked, want once it had gone on for a minute", d)
		}
		logged := h.logged()
		for _, want := range []string{
			"'s worker reached the usage limit; when it resets is not known. No new tickets start, and one limited " +
				"worker is told to carry on every 15m, for up to 6h",
			"to carry on, to see whether the limit has lifted",
			"'s worker reached the usage limit again; the next try is at",
			"'s worker went on past the limit; telling the workers still limited to carry on, and taking tickets again",
		} {
			if !strings.Contains(logged, want) {
				t.Errorf("the log lacks %q:\n%s", want, logged)
			}
		}
		if got := h.alerts.list(); !slices.Contains(got, "Usage limit reached · the run waits") {
			t.Errorf("notifications: %q", got)
		}
		if strings.Contains(h.sink.text(), "told it to continue") {
			t.Errorf("a limited worker was nudged:\n%s", h.sink.text())
		}
	})
}

// A limit that holds past usage_limit_wait ends the run with USAGE_LIMIT and exit code 8, leaving
// the worker in its tab and its ticket noted, without telling it anything or starting the ticket
// waiting.
func TestTheUsageLimitWaitGivesUpWithExit8(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		h.cfg.UsageLimitWait = time.Hour
		start := time.Now()
		reset := start.Add(5 * time.Hour)
		h.beads.add("A", "a", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			readyOnceHeld(h, "B", "b", 2) // no behaviour: a worker on it fails the test
			return reachesLimit(hooks, true, reset)(w)
		})
		o, code := h.run()
		if code != 8 { // ExitUsageLimit, which scripts read
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "USAGE_LIMIT: A's worker reached the usage limit (it resets at " + ResetLabel(reset, start) +
			"), still waiting after 1h; stopping"
		if !strings.HasPrefix(o.Final(), want) {
			t.Errorf("final %q, want it to start %q", o.Final(), want)
		}
		if took := time.Since(start); took < time.Hour || took > time.Hour+time.Minute {
			t.Errorf("stopped after %s, want the hour usage_limit_wait gives", took)
		}
		if got := h.herdr.typedTo("A"); len(got) != 0 {
			t.Errorf("typed to A: %q", got)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "reached the account's usage limit") {
			t.Errorf("A's notes: %q", notes)
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("left running: %v", got)
		}
		if !h.alerts.has("Stopped: USAGE_LIMIT on A") {
			t.Errorf("notifications: %q", h.alerts.list())
		}
	})
}

// With nothing left running while the run holds (the limited worker gone from its tab), the run
// waits for the reset and then takes tickets again; with usage_limit_wait short of the reset, it
// stops with USAGE_LIMIT instead, the ticket waiting never started.
func TestWithNothingRunningTheRunWaitsForTheReset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		wait time.Duration
		code int
	}{
		{"reset", 6 * time.Hour, ExitOK},
		{"gives up", 30 * time.Minute, ExitUsageLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.cfg.UsageLimitWait = tc.wait
				start := time.Now()
				reset := start.Add(time.Hour)
				h.beads.add("A", "a", 1)
				h.beads.add("B", "b", 2)
				h.worker("A", func(w *fakeWorker) AgentState {
					hooks.limit(w.wt, reset)
					w.shows(StateLimited)
					time.Sleep(10 * time.Minute)
					return StateGone // its tab closed
				})
				if tc.code == ExitOK {
					h.worker("B", finishes("b.txt"))
				}
				o, code := h.run()
				if code != tc.code {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				b := h.sink.dispatchedAt("B")
				if tc.code != ExitOK {
					want := "USAGE_LIMIT: the run held for the usage limit for 30m (it resets at " +
						ResetLabel(reset, start) + "), with nothing running; stopping"
					if o.Final() != want || !b.IsZero() {
						t.Errorf("final %q, want %q; B dispatched at %s", o.Final(), want, b)
					}
					return
				}
				if b.Before(reset.Add(resetGrace)) || !strings.Contains(h.mainLog(), "B: add b.txt") {
					t.Errorf("B dispatched at %s, before the reset's minute (%s); main:\n%s",
						b, reset.Add(resetGrace), h.mainLog())
				}
				if want := "USAGE_LIMIT lifted: the limit reset at " + ResetLabel(reset, start) +
					"; taking tickets again"; !strings.Contains(h.logged(), want) {
					t.Errorf("the log lacks %q:\n%s", want, h.logged())
				}
			})
		})
	}
}

// A worker whose limit has reset already (as one an earlier run left may be) is told to carry on at
// once: the run doesn't hold, nor notify.
func TestALimitAlreadyResetIsToldAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.UsageLimitWait = 6 * time.Hour
		start := time.Now()
		h.beads.add("A", "a", 1)
		h.worker("A", reachesLimit(hooks, true, start.Add(-time.Hour)), carriesOn(hooks, "a.txt", time.Minute))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took > 3*time.Minute {
			t.Errorf("merged after %s", took)
		}
		if got := h.sink.eventsOf(EvLimited); len(got) != 0 {
			t.Errorf("the run held: %+v", got)
		}
		if got := h.herdr.typedTo("A"); !equal(got, []string{"esc", "prompt"}) {
			t.Errorf("typed to A: %q", got)
		}
		if got := h.alerts.list(); slices.ContainsFunc(got, func(n string) bool { return strings.Contains(n, "Usage limit") }) {
			t.Errorf("notifications: %q", got)
		}
	})
}

// A worker told to carry on at the reset that reaches the limit again (the reset misread, say) starts
// another hold, which tells it again no sooner than 15 minutes after the first hold lifted.
func TestALimitReachedAgainAfterTheResetHoldsAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.UsageLimitWait = 6 * time.Hour
		var paste pasteTimes
		h.herdr.onPrompt = paste.on
		start := time.Now()
		reset := start.Add(time.Hour)
		h.beads.add("A", "a", 1)
		h.worker("A", reachesLimit(hooks, true, reset), reachesLimit(hooks, true, reset),
			carriesOn(hooks, "a.txt", time.Minute))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.eventsOf(EvLimited); len(got) != 2 {
			t.Errorf("holds: %+v", got)
		}
		lifts := h.sink.eventsOf(EvLimitLift)
		pastes := paste.all()
		if len(lifts) != 2 || len(pastes) != 2 {
			t.Fatalf("lifted %d times, told %d times:\n%s", len(lifts), len(pastes), h.logged())
		}
		if d := lifts[1].Time.Sub(lifts[0].Time); d < limitRetry || d > limitRetry+time.Minute {
			t.Errorf("the second hold lifted %s after the first, want 15m", d)
		}
		if d := pastes[1].Sub(pastes[0]); d < limitRetry || d > limitRetry+time.Minute {
			t.Errorf("told again %s after the first time, want 15m", d)
		}
	})
}

// A reset time is said as its time on the day it is said, and with its day otherwise.
func TestResetLabel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 16, 23, 30, 0, 0, time.Local)
	for reset, want := range map[time.Time]string{
		time.Date(2026, 10, 16, 23, 45, 0, 0, time.Local): "23:45",
		time.Date(2026, 10, 17, 0, 5, 0, 0, time.Local):   "Sat Oct 17 00:05",
	} {
		if got := ResetLabel(reset, now); got != want {
			t.Errorf("ResetLabel(%s) = %q, want %q", reset, got, want)
		}
	}
}

// limitedClaude is a fake claude that answers as the real one does at the usage limit.
func limitedClaude(t *testing.T) string {
	t.Helper()
	return faketool.Write(t, t.TempDir(), "claude", "#!/bin/sh\ncat >/dev/null\ncat <<'JSON'\n"+
		`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,`+
		`"api_error":"usage_limit_reached","result":"`+limitWords+`","total_cost_usd":0,"num_turns":1}`+
		"\nJSON\nexit 1\n")
}

// Triage refused for the usage limit is skipped with a line saying so, not a TRIAGE_FAILED warning.
func TestTriageRefusedForTheUsageLimitIsSkipped(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{}, log, "", Deps{Advisor: organ.Client{Bin: limitedClaude(t)}, AdviceCtx: context.Background()})
	o.sink = &recordSink{}
	o.StartTriage()
	o.queueTriage(context.Background(), organ.Deferral{ID: "A"})
	o.FinishTriage(context.Background())
	got := o.sink.(*recordSink).text()
	if want := "triage of A skipped: claude is at the account's usage limit (" + limitWords + ")"; !strings.Contains(got, want) ||
		strings.Contains(got, "TRIAGE_FAILED") {
		t.Errorf("logged:\n%s\nwant %q", got, want)
	}
}
