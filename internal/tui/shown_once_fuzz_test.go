package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// shownTickets are the tickets of FuzzEachTicketShowsOnce: no ID is part of another, nor of anything
// else a frame says.
var shownTickets = []string{"tk1", "tk2", "tk3", "tk4", "tk5", "tk6"}

// shownStep sends m the step op (an op byte and a ticket byte) of FuzzEachTicketShowsOnce: one of the
// loop's events or a worker's status, about the ticket its second byte picks, its fields from the op.
// It returns the event it sent, if it sent one.
func shownStep(m Dashboard, op, ticket byte, began time.Time) (Dashboard, *dispatch.Event) {
	k := int(ticket) % len(shownTickets)
	id := shownTickets[k]
	ev := func(kind dispatch.Kind) dispatch.Event { return dispatch.Event{Kind: kind, Ticket: id} }
	var e dispatch.Event
	switch op % 16 {
	case 0, 1:
		e = dispatch.Event{Kind: dispatch.EvDispatch, Ticket: id, Title: "Title " + fmt.Sprint(k),
			QueueState: dispatch.QueueState{Queued: int(op % 5), Unclosed: -1}}
	case 2, 3, 4:
		agents := []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle, dispatch.StateDone,
			dispatch.StateBlocked, ""}
		st := dispatch.Status{Ticket: id, Title: "Title " + fmt.Sprint(k), Tab: "w" + fmt.Sprint(k),
			Started: began.Add(time.Duration(k) * time.Minute), Agent: agents[int(op/16)%len(agents)],
			Activity: "⏺ Bash(go test)", Doing: dispatch.DoingTesting, Permission: op&0x80 != 0}
		switch (op / 16) % 5 {
		case 1:
			st.Resolving = true
		case 2:
			st.Fixing = true
		case 3:
			st.Merge, st.Place = dispatch.MergeQueued, 1
		case 4:
			st.Merge = dispatch.MergeRunning
		}
		return receive(m, statusMsg(st)), nil
	case 5, 6:
		return receive(m, statusMsg(dispatch.Status{Ticket: id, Gone: true})), nil
	case 7:
		e = ev(dispatch.EvClosed)
		e.Detail = "abc1234"
	case 8:
		e = ev(dispatch.EvDeferred)
		e.Detail = "stuck"
	case 9:
		e = ev(dispatch.EvAsked)
		e.Detail = "q-1: Which?"
	case 10:
		e = ev(dispatch.EvWaits)
		e.Detail = "x-0"
	case 11:
		e = ev(dispatch.EvWarn)
		e.Aside = true
		if op&0x10 != 0 {
			e.Blocked = "merge conflict"
		}
	case 12:
		e = ev(dispatch.EvReopened)
		e.Detail = "the machine works again"
		if op&0x10 != 0 {
			e = ev(dispatch.EvRequeued)
			e.Detail = "its setup failed"
		}
	case 13:
		e = ev(dispatch.EvTriage)
		e.Detail, e.Title = "split", "too big"
	case 14:
		e = ev(dispatch.EvAnswered)
		e.Title = "Title " + fmt.Sprint(k)
	default:
		kinds := []dispatch.Kind{dispatch.EvHold, dispatch.EvDrain, dispatch.EvResume, dispatch.EvProbed}
		e = ev(kinds[int(op/16)%len(kinds)])
	}
	return runEvents(m, e), &e
}

// partEnds are the kinds of the events that end their ticket's part in the run, for now, as the loop
// sends them, not as the dashboard reads them: they leave it done, set aside, asked, stopped or back in
// the queue. EvDispatch and EvAnswered start it again.
var partEnds = []dispatch.Kind{dispatch.EvClosed, dispatch.EvDeferred, dispatch.EvAsked, dispatch.EvWaits,
	dispatch.EvReopened, dispatch.EvRequeued, dispatch.EvHold}

// endsPart reports whether ev ends its ticket's part in the run: one of partEnds, or a warning that
// sets it aside.
func endsPart(ev dispatch.Event) bool {
	return ev.Ticket != "" && (slices.Contains(partEnds, ev.Kind) || ev.Kind == dispatch.EvWarn && ev.Aside)
}

// shownSizes are the panes FuzzEachTicketShowsOnce draws its frames in, one after another: Current in
// boxes, in a list and cut short, with the tickets table beside it and without.
var shownSizes = [][2]int{{140, 50}, {90, 30}, {100, 20}, {70, 16}, {60, 10}}

// FuzzEachTicketShowsOnce sends the dashboard any sequence of the loop's events and workers' statuses
// (ops, two bytes a step: what, and which ticket) and checks the frame after each, the pane's size
// changing from step to step: each ticket has one record, and shows at most once, in Current or the
// tickets table, never both; and once a dispatched ticket's part ends, its row isn't live (working)
// unless its worker runs. go test runs the seeds, fewer with -short; docs/development.md says how
// to fuzz.
func FuzzEachTicketShowsOnce(f *testing.F) {
	// A ticket picked up, running, closed as its worker finishes, then gone; one deferred while it
	// runs; one handed back to fix its check and merged; one with a status before any event.
	f.Add([]byte{0, 0, 2, 0, 7, 0, 5, 0, 0, 1, 2, 1, 8, 1, 0x22, 0, 0x32, 0, 0x42, 0, 7, 0, 5, 0, 2, 2})
	// A ticket picked up whose setup failed, requeued before its worker ever showed, as one beside it
	// runs; then picked up again.
	f.Add([]byte{0, 0, 0, 1, 2, 1, 0x1c, 0, 0, 0, 2, 0})
	r := rand.New(rand.NewPCG(14, 14))
	seeds := 12
	if testing.Short() {
		seeds = 3
	}
	for range seeds {
		ops := make([]byte, 2*(20+r.IntN(40)))
		for i := range ops {
			ops[i] = byte(r.IntN(256))
		}
		f.Add(ops)
	}
	began := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, ops []byte) {
		ops = ops[:min(len(ops), 400)] // a frame a step: a longer run finds no more, only slower
		m := NewDashboard(dispatch.Config{Base: "main", Concurrency: 1}, func() {}, func(bool) {}, func(string) {})
		m.began, m.now = began, func() time.Time { return began.Add(time.Hour) }
		ended := map[string]bool{} // the tickets whose part ended, since they were last picked up
		for i := 0; i+1 < len(ops); i += 2 {
			var ev *dispatch.Event
			m, ev = shownStep(m, ops[i], ops[i+1], began)
			if t.Failed() {
				return
			}
			switch {
			case ev == nil:
			case ev.Kind == dispatch.EvDispatch || ev.Kind == dispatch.EvAnswered:
				ended[ev.Ticket] = false
			case endsPart(*ev):
				ended[ev.Ticket] = true
			}
			step := fmt.Sprintf("step %d (%d %d)", i/2, ops[i], ops[i+1])
			checkShownOnce(t, m, shownSizes[i/2%len(shownSizes)], step)
			checkEndedNotLive(t, m, ended, step)
		}
	})
}

// checkEndedNotLive checks that no ticket of m whose part ended has a live row, unless its worker runs.
func checkEndedNotLive(t *testing.T, m Dashboard, ended map[string]bool, step string) {
	t.Helper()
	for _, r := range m.records {
		if r.listed && !r.running && ended[r.row.id] && rowLooks[r.row.state].live {
			t.Errorf("%s: %s's part ended, but its row shows it %s", step, r.row.id, rowLooks[r.row.state].word)
		}
	}
}

// checkShownOnce checks that each ticket of m has one record, and shows at most once in its frame in a
// pane of size.
func checkShownOnce(t *testing.T, m Dashboard, size [2]int, step string) {
	t.Helper()
	var ids []string
	for _, r := range m.records {
		if slices.Contains(ids, r.row.id) {
			t.Errorf("%s: %s has two records", step, r.row.id)
		}
		ids = append(ids, r.row.id)
	}
	m.width, m.height = size[0], size[1]
	frame := ansi.Strip(m.View())
	for _, id := range shownTickets {
		if n := strings.Count(frame, id); n > 1 {
			t.Errorf("%s, %dx%d: %s shows %d times:\n%s", step, size[0], size[1], id, n, frame)
		}
	}
}
