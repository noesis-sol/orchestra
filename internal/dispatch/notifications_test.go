package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// notifyingLoop is a loop that only emits, for the project named project, recording the
// notifications its log shows.
func notifyingLoop(t *testing.T, project string) (*Loop, *alerts) {
	t.Helper()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return &Loop{log: log, sink: &recordSink{}}, recordAlerts(log)
}

// A notification is titled with the project alone and says in a few words what happened to which
// ticket: closed with its title, set aside with why, waiting on a question with the question's
// title, or that the run stopped (over which ticket) or finished, with what it closed and set aside.
func TestNotificationsSayWhichTicketDidWhat(t *testing.T) {
	t.Parallel()
	long := "Add a --json flag to list, and make the table output wrap at the terminal width"
	for _, tc := range []struct {
		name   string
		events []Event
		want   []string
	}{
		{"merged", []Event{{Kind: EvClosed, Ticket: "jswallet-12", Title: "Add a --json flag to list",
			Detail: "ffd6ce4 merged into main",
			Text:   "  jswallet-12 closed (ffd6ce4 jswallet-12: Add a --json flag); merged into main, worktree, branch and tab removed"}},
			[]string{"Closed jswallet-12 · Add a --json flag to list"}},
		{"a long title", []Event{{Kind: EvClosed, Ticket: "jswallet-12", Title: long, Text: "  jswallet-12 closed"}},
			[]string{"Closed jswallet-12 · Add a --json flag to list, and make the table output wrap a…"}},
		{"deferred", []Event{{Kind: EvDeferred, Ticket: "jswallet-12", Title: "Add a --json flag to list",
			Detail: "by the worker", Text: "  jswallet-12 deferred by worker; worktree wt and tab 1 left open"}},
			[]string{"Set aside jswallet-12 · by the worker"}},
		{"left for review", []Event{{Kind: EvWarn, Ticket: "jswallet-12", Aside: true, Detail: "checks failed",
			Text: "  CHECKS_FAILED: jswallet-12 closed, but 'scripts/check.sh' fails on wt/jswallet-12 rebased onto main"}},
			[]string{"Set aside jswallet-12 · checks failed"}},
		{"asked", []Event{{Kind: EvAsked, Ticket: "jswallet-12", Title: "Add a --json flag to list",
			Detail: "jswallet-40: Which name should the flag have?",
			Text:   "  ASKED: jswallet-12 waits on your answer to jswallet-40 (Which name should the flag have?); …"}},
			[]string{"jswallet-12 needs your answer · Which name should the flag have?"}},
		{"stopped over a ticket", []Event{{Kind: EvStop, Ticket: "jswallet-12", Detail: "PAUSED",
			Text: "PAUSED: jswallet-12 still in_progress in tab 1 (worktree wt); stopping so it can be answered"}},
			[]string{"Stopped: PAUSED on jswallet-12"}},
		{"stopped", []Event{{Kind: EvStop, Detail: Interrupted,
			Text: "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree"}},
			[]string{"Stopped: INTERRUPTED"}},
		{"finished with nothing", []Event{{Kind: EvDone, Text: "READY_EMPTY after 0 tickets"}},
			[]string{"Finished the run · no tickets closed"}},
		{"finished", []Event{
			{Kind: EvClosed, Ticket: "a", Title: "first", Text: "  a closed"},
			{Kind: EvClosed, Ticket: "b", Title: "second", Text: "  b closed"},
			{Kind: EvDeferred, Ticket: "c", Title: "third", Detail: "by the worker", Text: "  c deferred by worker"},
			{Kind: EvWarn, Ticket: "d", Aside: true, Detail: "closed without a commit", Text: "  CLOSED_WITHOUT_COMMIT: …"},
			{Kind: EvWarn, Ticket: "e", Text: "  LONG_RUNNING: e still working after 2h"}, // not set aside: not counted
			{Kind: EvAsked, Ticket: "e", Title: "fifth", Detail: "q: which?", Text: "  ASKED: e waits on q"},
			{Kind: EvDone, N: 5, Text: "READY_EMPTY after 5 tickets"},
		}, []string{"Closed a · first", "Closed b · second", "Set aside c · by the worker",
			"Set aside d · closed without a commit", "e needs your answer · which?",
			"Finished the run · 2 tickets closed · 2 set aside"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, shown := notifyingLoop(t, "jswallet")
			for _, ev := range tc.events {
				o.emit(ev)
			}
			if got := shown.list(); !equal(got, tc.want) {
				t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			for _, s := range shown.all() {
				if s.title != "jswallet" {
					t.Errorf("%q titled %q, want the project alone", s.body, s.title)
				}
			}
			// The log has each line in full, as before.
			lines := strings.Join(o.log.RunLines(), "\n")
			for _, ev := range tc.events {
				if !strings.Contains(lines, ev.Text) {
					t.Errorf("log lacks %q:\n%s", ev.Text, lines)
				}
			}
		})
	}
}

// Holds, probes, warnings that set no ticket aside, triage and progress don't notify: they need
// nothing from the maintainer, or the stop that follows says it. They stay in the log as they were.
func TestNotificationsLeaveOutWhatNeedsNothing(t *testing.T) {
	t.Parallel()
	o, shown := notifyingLoop(t, "jswallet")
	events := []Event{
		{Kind: EvInfo, Text: "START orchestra v1 in /repo on main"},
		{Kind: EvDispatch, Ticket: "a", Title: "first", N: 1, Limit: 10, Text: "[1/10] a dispatching: first"},
		{Kind: EvQueue, Queued: 3},
		{Kind: EvHold, Ticket: "a", Text: "HOLD: PAUSED: a still in_progress; no new tickets while the 1 running finish"},
		{Kind: EvHold, Text: "PROBE: the run holds for the environment; in 10m one worker without a ticket runs a command"},
		{Kind: EvProbed, Text: "PROBE_OK: a worker without a ticket ran a command 10m after the hold; taking tickets again"},
		{Kind: EvWarn, Text: "  LIKELY_CONFLICT: a and b both edit x.go; the second to merge may conflict"},
		{Kind: EvWarn, Ticket: "a", Text: "  LONG_RUNNING: a still working after 2h in tab 1"},
		{Kind: EvWarn, Ticket: "a", Text: "  REBASE_SKIPPED: wt/a is behind main"},
		{Kind: EvWarn, Ticket: "a", Text: "  TRIAGE_FAILED for a: claude failed"},
		{Kind: EvTriage, Ticket: "a", Title: "flaky test", Detail: "flaky · high", Text: "  triage a: flaky (high confidence)"},
		{Kind: EvDrain, Text: "DRAIN: stopping after the 1 running ticket, as asked"},
		{Kind: EvResume, Text: "RESUME: taking tickets again"},
		{Kind: EvAnswered, Ticket: "a", Detail: "q: which?", Text: "  ANSWERED: q (which?) is answered, so a comes back"},
	}
	for _, ev := range events {
		o.emit(ev)
	}
	if got := shown.list(); len(got) != 0 {
		t.Errorf("notified:\n%s", strings.Join(got, "\n"))
	}
	lines := strings.Join(o.log.RunLines(), "\n")
	for _, ev := range events {
		if ev.Kind != EvQueue && !strings.Contains(lines, ev.Text) {
			t.Errorf("log lacks %q:\n%s", ev.Text, lines)
		}
	}
}

// In a run, the loop gives the events the tickets' titles, and the notifications say them, or the
// question's title for a ticket waiting on one.
func TestRunNotifiesWhatHappenedToEachTicket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "Add a --json flag to list", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.beads.add("D", "fourth", 4)
		h.worker("A", finishes("a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })
		h.worker("C", func(w *fakeWorker) AgentState {
			w.claim()
			w.ask("Q", "Which name should the flag have?")
			return "idle"
		})
		h.worker("D", closesUnnamed("d.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []string{"Closed A · Add a --json flag to list", "Set aside B · by the worker",
			"C needs your answer · Which name should the flag have?", "Set aside D · closed without a commit",
			"Finished the run · 1 ticket closed · 2 set aside"}
		if got := h.alerts.list(); !equal(got, want) {
			t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		for _, s := range h.alerts.all() {
			if s.title != "t" { // the project the harness opens the log for
				t.Errorf("%q titled %q, want the project alone", s.body, s.title)
			}
		}
		titles := map[string]string{"A": "Add a --json flag to list", "B": "second", "C": "third"}
		for _, ev := range h.sink.events {
			switch ev.Kind {
			case EvClosed, EvDeferred, EvAsked:
				if ev.Title != titles[ev.Ticket] {
					t.Errorf("%s event for %s titled %q, want %q", ev.Kind, ev.Ticket, ev.Title, titles[ev.Ticket])
				}
			}
		}
	})
}

// A run that stops over a ticket says which, once: the hold before it, while another ticket
// finishes, doesn't notify.
func TestRunNotifiesTheStopNotTheHold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" }) // pauses the run
		h.worker("B", busyFor(time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if len(h.sink.of(EvHold)) == 0 {
			t.Fatalf("no hold while B finished:\n%s", h.sink.text())
		}
		want := []string{"Closed B · second", "Stopped: PAUSED on A"}
		if got := h.alerts.list(); !equal(got, want) {
			t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

// Ctrl+C, as the loop reports it, notifies that the run stopped, over no ticket.
func TestRunNotifiesAnInterruption(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	o := h.loop()
	o.ReportInterrupt = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := o.Run(ctx); code != ExitInterrupted {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if got := h.alerts.list(); !equal(got, []string{"Stopped: INTERRUPTED"}) {
		t.Errorf("notified %q", got)
	}
}
