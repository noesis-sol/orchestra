package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// fixture builds a test's dashboard as a run does: each step is a message through Update, the loop's
// events and its workers' statuses as dispatch sends them, so a test can't make a state no run makes,
// such as a worker in Current whose ticket has no row.
type fixture struct {
	t *testing.T
	m Dashboard
	n int // the tickets picked up so far, which number the dispatch events
}

// run is a fixture for a run of up to 40 tickets on batch, 80 columns wide and not yet sized, its
// callbacks doing nothing.
func run(t *testing.T) *fixture {
	return &fixture{t: t, m: NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {},
		func(string) {})}
}

// config sets the run's settings.
func (f *fixture) config(cfg dispatch.Config) *fixture {
	f.m.cfg = cfg
	return f
}

// onCancel sets what Ctrl+C calls.
func (f *fixture) onCancel(cancel func()) *fixture {
	f.m.cancel = cancel
	return f
}

// onDrain sets what a confirmed s calls.
func (f *fixture) onDrain(drain func(on bool)) *fixture {
	f.m.drain = drain
	return f
}

// onFocus sets what a worker's number calls; nil for the workers' tabs out of sight.
func (f *fixture) onFocus(focus func(tab string)) *fixture {
	f.m.focus = focus
	return f
}

// clock sets the clock the dashboard reads the times it shows from.
func (f *fixture) clock(now func() time.Time) *fixture {
	f.m.now = now
	return f
}

// send passes msgs to the dashboard's Update in turn.
func (f *fixture) send(msgs ...tea.Msg) *fixture {
	f.m = receive(f.m, msgs...)
	return f
}

// event sends the loop's events.
func (f *fixture) event(evs ...dispatch.Event) *fixture {
	f.m = runEvents(f.m, evs...)
	return f
}

// picked sends the dispatch event that picks up ticket id, the queue's counts as they were.
func (f *fixture) picked(id, title string) *fixture {
	f.n++
	m := f.m
	return f.event(dispatch.Event{Kind: dispatch.EvDispatch, N: f.n, Limit: m.cfg.Limit, Ticket: id, Title: title,
		Queued: m.queued, Held: m.held, Unclosed: m.unclosed, Solo: m.solo, MachineFull: m.machineFull})
}

// status sends workers' statuses. Each but a Gone one is of a ticket picked up, as a worker runs only
// once its ticket is dispatched.
func (f *fixture) status(sts ...dispatch.Status) *fixture {
	f.t.Helper()
	for _, st := range sts {
		if !st.Gone && f.m.rowIndex(st.Ticket) < 0 {
			f.t.Fatalf("a status for %s, which no dispatch event picked up", st.Ticket)
		}
		f.send(statusMsg(st))
	}
	return f
}

// running picks up each status's ticket with its title and sends the status: workers starting.
func (f *fixture) running(sts ...dispatch.Status) *fixture {
	f.t.Helper()
	for _, st := range sts {
		f.picked(st.Ticket, st.Title).status(st)
	}
	return f
}

// gone sends the statuses that take tickets out of Current, as their workers finish.
func (f *fixture) gone(ids ...string) *fixture {
	for _, id := range ids {
		f.send(statusMsg(dispatch.Status{Ticket: id, Gone: true}))
	}
	return f
}

// closed sends the event that closes ticket id, its merge as detail says.
func (f *fixture) closed(id, detail string) *fixture {
	return f.event(dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: detail})
}

// done picks up ticket id and closes it: a ticket the run finished before the test looks.
func (f *fixture) done(id, title, detail string) *fixture {
	return f.picked(id, title).closed(id, detail)
}

// queue sends the loop's count of ready tickets waiting for a slot, the rest of the queue as it was.
func (f *fixture) queue(n int) *fixture {
	m := f.m
	return f.event(dispatch.Event{Kind: dispatch.EvQueue, Queued: n, Held: m.held, Unclosed: m.unclosed,
		Solo: m.solo, MachineFull: m.machineFull})
}

// sized sends the pane's size.
func (f *fixture) sized(w, h int) *fixture {
	return f.send(tea.WindowSizeMsg{Width: w, Height: h})
}

// keys presses keys in turn.
func (f *fixture) keys(keys ...string) *fixture {
	f.m = press(f.m, keys...)
	return f
}

// dash is the dashboard built.
func (f *fixture) dash() Dashboard { return f.m }

// setStatus sends m a worker's status.
func setStatus(m Dashboard, st dispatch.Status) Dashboard {
	next, _ := m.Update(statusMsg(st))
	return next.(Dashboard)
}

// reported sends m each running worker's status with change made to it, as the workers report. The
// dashboard m was built from takes the change too: their workers are one map.
func reported(m Dashboard, change func(st *dispatch.Status)) Dashboard {
	for _, st := range m.Running() {
		change(&st)
		m = setStatus(m, st)
	}
	return m
}
