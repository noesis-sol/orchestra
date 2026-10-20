package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// rowsOf is m's rows as "id state: note", for comparing; the state is its word in the tickets table.
func rowsOf(m Dashboard) []string {
	var rows []string
	for _, r := range m.rows() {
		rows = append(rows, fmt.Sprintf("%s %s: %s", r.id, rowLooks[r.state].word, r.note))
	}
	return rows
}

// The loop's events set the rows and totals, with nothing rendered.
func TestApplySetsRowsAndTotals(t *testing.T) {
	dispatched := func(id string) dispatch.Event {
		return dispatch.Event{Kind: dispatch.EvDispatch, Ticket: id, Title: "Title " + id,
			QueueState: dispatch.QueueState{Queued: 2, Unclosed: 5}}
	}
	ev := func(k dispatch.Kind, id, detail string) dispatch.Event {
		return dispatch.Event{Kind: k, Ticket: id, Detail: detail}
	}
	for _, c := range []struct {
		name                       string
		events                     []dispatch.Event
		rows                       []string
		closed, setAside, needsYou int
		quit                       bool
	}{
		{name: "completed", events: []dispatch.Event{dispatched("k-1"), ev(dispatch.EvClosed, "k-1", "abc1234")},
			rows: []string{"k-1 done: abc1234"}, closed: 1},
		{name: "deferred, then picked up again",
			events: []dispatch.Event{dispatched("k-1"), ev(dispatch.EvDeferred, "k-1", "no change"), dispatched("k-1")},
			rows:   []string{"k-1 working: "}},
		{name: "deferred and asked",
			events: []dispatch.Event{dispatched("k-1"), dispatched("k-2"), ev(dispatch.EvDeferred, "k-1", "stuck"),
				ev(dispatch.EvAsked, "k-2", "q-1: Which?")},
			rows: []string{"k-1 deferred: stuck", "k-2 for you: answer q-1: Which?"}, setAside: 1, needsYou: 1},
		{name: "waits and reopened",
			events: []dispatch.Event{dispatched("k-1"), ev(dispatch.EvWaits, "k-1", "k-0"),
				ev(dispatch.EvReopened, "k-2", "the machine works again")},
			rows: []string{"k-1 waiting: for k-0 to merge", "k-2 waiting: back in the queue: the machine works again"}},
		{name: "a warning that sets aside",
			events: []dispatch.Event{dispatched("k-1"), {Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true}},
			rows:   []string{"k-1 review: left for review, see the log"}, setAside: 1},
		{name: "a warning about a running ticket",
			events: []dispatch.Event{dispatched("k-1"), {Kind: dispatch.EvWarn, Ticket: "k-1", Detail: "long"}},
			rows:   []string{"k-1 working: "}},
		{name: "a merge conflict blocks",
			events: []dispatch.Event{dispatched("k-1"),
				{Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true, Blocked: "merge conflict in a.go"}},
			rows: []string{"k-1 blocked: merge conflict in a.go"}, needsYou: 1},
		{name: "a hold over a ticket",
			events: []dispatch.Event{dispatched("k-1"), {Kind: dispatch.EvHold, Ticket: "k-1"}},
			rows:   []string{"k-1 stopped: "}},
		{name: "a stop stops the working rows",
			events: []dispatch.Event{dispatched("k-1"), dispatched("k-2"), ev(dispatch.EvClosed, "k-1", "abc1234"),
				{Kind: dispatch.EvStop}},
			rows: []string{"k-1 done: abc1234", "k-2 stopped: "}, closed: 1, quit: true},
		{name: "done", events: []dispatch.Event{{Kind: dispatch.EvDone}}, quit: true},
	} {
		m := NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, nil)
		quit := false
		for _, e := range c.events {
			quit = m.apply(e)
		}
		if got := rowsOf(m); !slices.Equal(got, c.rows) {
			t.Errorf("%s: rows %q, want %q", c.name, got, c.rows)
		}
		if m.closed != c.closed || m.setAside() != c.setAside || m.needsYou() != c.needsYou {
			t.Errorf("%s: completed %d, set aside %d, needs you %d; want %d, %d, %d", c.name,
				m.closed, m.setAside(), m.needsYou(), c.closed, c.setAside, c.needsYou)
		}
		if quit != c.quit || m.received != len(c.events) {
			t.Errorf("%s: quit %v after %d events, want %v after %d", c.name, quit, m.received, c.quit, len(c.events))
		}
	}
}

// Once the dashboard has quit, the events still coming are the printer's: not taken in or counted.
func TestApplyAfterQuittingTakesNothingIn(t *testing.T) {
	m := NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, nil)
	if !m.apply(dispatch.Event{Kind: dispatch.EvDone}) {
		t.Fatal("the run's end should quit the dashboard")
	}
	if m.apply(dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-1"}) || m.received != 1 || len(m.rows()) != 0 {
		t.Errorf("an event after quitting was taken in: received %d, rows %q", m.received, rowsOf(m))
	}
}

// A triage verdict goes on a deferred row, not on one picked up again since.
func TestApplyTriageGoesOnDeferredRows(t *testing.T) {
	m := NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, nil)
	m.apply(dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "stuck"})
	m.apply(dispatch.Event{Kind: dispatch.EvTriage, Ticket: "k-1", Detail: "split", Title: "too big"})
	m.apply(dispatch.Event{Kind: dispatch.EvDispatch, Ticket: "k-2"})
	m.apply(dispatch.Event{Kind: dispatch.EvTriage, Ticket: "k-2", Detail: "retry", Title: "flaky"})
	if m.rows()[0].triage != "split · too big" || m.rows()[1].triage != "" || m.triaged != 2 {
		t.Errorf("triage %q and %q, %d verdicts; want it on the deferred row only, 2 verdicts",
			m.rows()[0].triage, m.rows()[1].triage, m.triaged)
	}
}

// Statuses come and go; while the run winds down, a worker it didn't know of joins the line.
func TestApplyStatusTracksTheRunningWorkers(t *testing.T) {
	m := NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, nil)
	m.applyStatus(dispatch.Status{Ticket: "k-1"})
	m.apply(dispatch.Event{Kind: dispatch.EvDrain})
	m.applyStatus(dispatch.Status{Ticket: "k-2"})
	m.applyStatus(dispatch.Status{Ticket: "k-1", Gone: true})
	if ids := m.runningIDs(); !slices.Equal(ids, []string{"k-2"}) {
		t.Errorf("running %q, want k-2 alone", ids)
	}
	if !m.draining || !slices.Equal(m.drainFrom, []string{"k-1", "k-2"}) {
		t.Errorf("draining %v from %q, want true from k-1 and k-2", m.draining, m.drainFrom)
	}
	m.apply(dispatch.Event{Kind: dispatch.EvResume})
	if m.draining || m.drainFrom != nil {
		t.Errorf("resumed, but draining %v from %q", m.draining, m.drainFrom)
	}
}

// press changes the dashboard's own state and asks Update for the rest, calling nothing itself.
func TestPress(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		name                     string
		asking, draining, stop   bool
		key                      string
		want                     keyAction
		wantAsking, wantDraining bool
		wantQuitting             bool
	}{
		{name: "ctrl+c quits", key: "ctrl+c", want: keyAction{quit: true}, wantQuitting: true},
		{name: "ctrl+c quits with the question open", asking: true, key: "ctrl+c", want: keyAction{quit: true},
			wantAsking: true, wantQuitting: true},
		{name: "s asks", key: "s", wantAsking: true},
		{name: "s asks nothing of a stopping run", stop: true, key: "s"},
		{name: "s asks to go on while winding down", stop: true, draining: true, key: "s", wantAsking: true,
			wantDraining: true},
		{name: "y winds down", asking: true, key: "y", want: keyAction{drain: &on}, wantDraining: true},
		{name: "y goes on", asking: true, draining: true, key: "y", want: keyAction{drain: &off}},
		{name: "n keeps on", asking: true, key: "n"},
		{name: "esc keeps winding down", asking: true, draining: true, key: "esc", wantDraining: true},
		{name: "a number goes to its tab", key: "2", want: keyAction{focusTab: "tab-2"}},
		{name: "a number with no worker", key: "3"},
		{name: "a number while asking", asking: true, key: "1", wantAsking: true},
		{name: "another key", key: "x"},
	} {
		now := time.Now()
		m := run(t).config(dispatch.Config{}).
			running(dispatch.Status{Ticket: "k-1", Tab: "tab-1", Started: now.Add(-time.Minute)},
				dispatch.Status{Ticket: "k-2", Tab: "tab-2", Started: now}).dash()
		m.asking, m.draining, m.stopping = c.asking, c.draining, c.stop
		got := m.press(c.key)
		if got.quit != c.want.quit || got.focusTab != c.want.focusTab ||
			(got.drain == nil) != (c.want.drain == nil) || got.drain != nil && *got.drain != *c.want.drain {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if m.asking != c.wantAsking || m.draining != c.wantDraining || m.quitting != c.wantQuitting ||
			m.interrupted != c.wantQuitting {
			t.Errorf("%s: asking %v, draining %v, quitting %v; want %v, %v, %v", c.name,
				m.asking, m.draining, m.quitting, c.wantAsking, c.wantDraining, c.wantQuitting)
		}
	}
}

// With the workers' tabs out of sight, a number goes nowhere.
func TestPressANumberWithTheTabsOutOfSight(t *testing.T) {
	m := run(t).config(dispatch.Config{}).onFocus(nil).running(dispatch.Status{Ticket: "k-1", Tab: "tab-1"}).dash()
	if got := m.press("1"); got.focusTab != "" {
		t.Errorf("went to %q with no focus callback", got.focusTab)
	}
}

// workerLook names a worker's status, its ticket's state and its mark, from one table.
func TestWorkerLook(t *testing.T) {
	for _, c := range []struct {
		st                dispatch.Status
		status, row, mark string // row "": working's; mark "": the spinner
	}{
		{dispatch.Status{}, "", "", ""},
		{dispatch.Status{Agent: dispatch.StateWorking}, "working", "", ""},
		{dispatch.Status{Agent: dispatch.StateWorking, Doing: dispatch.DoingTesting}, "testing", "▶ testing", ""},
		{dispatch.Status{Agent: dispatch.StateWorking, Doing: dispatch.DoingSubagent}, "subagent", "▶ subagent", ""},
		{dispatch.Status{Agent: dispatch.StateIdle, Doing: dispatch.DoingEditing}, "idle", "", ""},
		{dispatch.Status{Agent: dispatch.StateBlocked}, "blocked — waiting for you", "", ""},
		{dispatch.Status{Agent: dispatch.StateIdle, Permission: true}, "permission — waiting for you", "■ allow?", ""},
		{dispatch.Status{Agent: dispatch.StateDone, Permission: true}, "permission — waiting for you", "■ allow?", "•"},
		{dispatch.Status{Agent: dispatch.StateDone}, "done", "", "•"},
		{dispatch.Status{Agent: dispatch.StateDone, Unreadable: true}, "unreadable", "", "•"},
		{dispatch.Status{Agent: dispatch.StateDone, Resolving: true}, "resolving", "⟳ resolving", ""},
		{dispatch.Status{Agent: dispatch.StateDone, Fixing: true}, "fixing check", "⟳ fixing", ""},
		{dispatch.Status{Agent: dispatch.StateDone, Merge: dispatch.MergeQueued}, "to merge", "◼ to merge", "◼"},
		{dispatch.Status{Agent: dispatch.StateDone, Merge: dispatch.MergeRunning}, "merging", "◆ merging", "◆"},
		{dispatch.Status{Agent: "rebooting"}, "rebooting", "", ""}, // a state with no look of its own
	} {
		l := workerLook(c.st)
		if got := ansi.Strip(l.status.render()); got != c.status {
			t.Errorf("%+v: status %q, want %q", c.st, got, c.status)
		}
		if got := ansi.Strip(l.row.render()); got != c.row {
			t.Errorf("%+v: row %q, want %q", c.st, got, c.row)
		}
		if l.mark.text != c.mark {
			t.Errorf("%+v: mark %q, want %q", c.st, l.mark.text, c.mark)
		}
	}
}

// Every row state has a look, and the working one is the one a stop stops.
func TestRowLooksCoverEveryRowState(t *testing.T) {
	if len(rowLooks) != int(rowWaiting)+1 {
		t.Fatalf("%d row looks for %d row states", len(rowLooks), rowWaiting+1)
	}
	for s, l := range rowLooks {
		if l.glyph == "" || l.word == "" || l.live != (rowState(s) == rowWorking) {
			t.Errorf("row state %d: %+v", s, l)
		}
	}
}

// A row event's line says what its row says, but an asked ticket's line has only its question.
func TestRowEventsLines(t *testing.T) {
	for _, c := range []struct {
		kind       dispatch.Kind
		note, line string
	}{
		{dispatch.EvClosed, "abc1234", "abc1234"},
		{dispatch.EvDeferred, "abc1234", "abc1234"},
		{dispatch.EvAsked, "answer abc1234", "abc1234"},
		{dispatch.EvWaits, "for abc1234 to merge", "for abc1234 to merge"},
		{dispatch.EvReopened, "back in the queue: abc1234", "back in the queue: abc1234"},
	} {
		re := rowEvents[c.kind]
		if note, line := re.note("abc1234"), re.line("abc1234"); note != c.note || line != c.line {
			t.Errorf("%v: note %q and line %q, want %q and %q", c.kind, note, line, c.note, c.line)
		}
		got := ansi.Strip(renderEvent(dispatch.Event{Kind: c.kind, Ticket: "k-1", Detail: "abc1234"}))
		if want := rowLooks[re.state].glyph + " k-1 " + re.says + "  " + c.line; !strings.HasSuffix(got, want) {
			t.Errorf("%v: line %q, want it to end %q", c.kind, got, want)
		}
	}
}

// The times shown are read from the dashboard's clock.
func TestTimesShownComeFromTheClock(t *testing.T) {
	m := NewDashboard(dispatch.Config{Base: "main"}, func() {}, func(bool) {}, nil)
	at := m.began.Add(12*time.Minute + 500*time.Millisecond)
	m.now = func() time.Time { return at }
	if got := ansi.Strip(m.titleLine(80)); !strings.HasSuffix(got, "main · 12m") {
		t.Errorf("title line %q should say the run has gone 12m", got)
	}
	head := m.workerHead(1, dispatch.Status{Ticket: "k-1", Started: at.Add(-90 * time.Second)})
	if got := ansi.Strip(head); !strings.HasSuffix(got, "1m30s") {
		t.Errorf("worker line %q should say it has run 1m30s", got)
	}
}
