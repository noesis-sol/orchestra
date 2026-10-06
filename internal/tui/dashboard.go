// Package tui is orchestra's terminal interface: the run's dashboard (Bubble Tea), plain output
// for pipes, and orchestra init's form and summary.
package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

type eventMsg dispatch.Event
type statusMsg dispatch.Status

// Finished tells the dashboard the run has returned, so it quits.
type Finished struct{}

// Dashboard is the live view of a run, a Bubble Tea model.
type Dashboard struct {
	cfg         dispatch.Config
	spin        spinner.Model
	active      map[string]dispatch.Status // running workers, by ticket
	stopping    bool                       // something stopped the run; the running ones are finishing
	draining    bool                       // the maintainer asked to stop after the running tickets
	asking      bool                       // the question whether to stop after them, or to go on, is open
	closed      int
	deferred    int
	triaged     int
	width       int
	height      int
	rows        []ticketRow // every ticket picked up in this run, oldest first
	interrupted bool
	final       *dispatch.Event // the stop or done event, printed by main after exit
	received    int             // events received, for ProgramSink.Handoff
	queued      int             // ready tickets waiting for a slot; -1 until the loop first says
	solo        dispatch.SoloState
	began       time.Time
	cancel      func()
	drain       func(on bool)    // asks the loop to stop after the running tickets, or with false to go on
	focus       func(tab string) // switches Herdr to a worker's tab; it must not block
}

// NewDashboard returns the run's dashboard. Ctrl+C calls cancel; s, once confirmed, calls drain;
// a worker's number calls focus with its tab.
func NewDashboard(cfg dispatch.Config, cancel func(), drain func(on bool), focus func(tab string)) Dashboard {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(pickedStyle))
	return Dashboard{cfg: cfg, spin: s, width: 80, queued: -1, began: time.Now(), cancel: cancel, drain: drain,
		focus: focus}
}

// Init starts the spinner.
func (m Dashboard) Init() tea.Cmd { return m.spin.Tick }

// Update handles the loop's events and statuses, keys and the window's size.
func (m Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Nothing to clear: on the alternate screen, which has no scrollback, a terminal that reflows
		// the frame as it resizes has nowhere to push its rows, and Bubble Tea draws each frame from
		// the top.
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch key := msg.String(); {
		case key == "ctrl+c": // at any time, the question open or not
			m.interrupted = true
			m.cancel()
			return m, tea.Quit
		case m.asking && key == "y":
			m.asking, m.draining = false, !m.draining
			m.drain(m.draining)
		case m.asking && (key == "n" || key == "esc"):
			m.asking = false
		case !m.asking && len(key) == 1 && key >= "1" && key <= "9":
			m.goTo(int(key[0] - '0'))
		case key == "s" && (m.draining || !m.stopping): // a run already stopping has nothing to drain
			m.asking = true
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case statusMsg:
		st := dispatch.Status(msg)
		if m.active == nil {
			m.active = map[string]dispatch.Status{}
		}
		if st.Gone {
			delete(m.active, st.Ticket)
		} else {
			m.active[st.Ticket] = st
		}
	case eventMsg:
		// Events update the dashboard in place; nothing is printed above it. The full lines are
		// in the log file.
		ev := dispatch.Event(msg)
		m.received++
		switch ev.Kind {
		case dispatch.EvDispatch:
			m.queued, m.solo = ev.Queued, ev.Solo
			m.working(ev.Ticket, ev.Title)
		case dispatch.EvQueue:
			m.queued, m.solo = ev.Queued, ev.Solo
		case dispatch.EvClosed:
			m.closed++
			m.setRow(ev.Ticket, rowDone, ev.Detail)
		case dispatch.EvDeferred:
			m.deferred++
			m.setRow(ev.Ticket, rowDeferred, ev.Detail)
		case dispatch.EvWarn:
			// Only a ticket the warning sets aside is for review, its row saying why as a deferred
			// one does, or blocked, when only its merge needs the maintainer (MERGE_CONFLICT). The
			// rest are about a ticket still running (LONG_RUNNING) or already deferred
			// (TRIAGE_FAILED), whose row stays as it is.
			if ev.Ticket != "" && ev.Aside && !m.blocked(ev) {
				why := ev.Detail
				if why == "" {
					why = "left for review, see the log"
				}
				m.setRow(ev.Ticket, rowReview, why)
			}
		case dispatch.EvAsked:
			m.setRow(ev.Ticket, rowAsked, "answer "+ev.Detail)
		case dispatch.EvAnswered: // its title for a ticket carried over from the last run, with no row yet
			m.working(ev.Ticket, ev.Title)
		case dispatch.EvTriage:
			m.triaged++
			// A verdict on an earlier deferral, in after the ticket came back, would outlast the next.
			if i := m.rowIndex(ev.Ticket); i >= 0 && m.rows[i].state == rowDeferred {
				m.rows[i].triage = ev.Detail + " · " + ev.Title
			}
		case dispatch.EvDrain, dispatch.EvResume: // as asked here, or with SIGUSR1
			m.draining = ev.Kind == dispatch.EvDrain
		case dispatch.EvProbed: // the machine works again after an environment hold
			m.stopping = false
		case dispatch.EvHold:
			m.stopping = true
			// A stop found before dispatching belongs to no ticket; one met as a finished ticket
			// merged (DIRTY_TREE, say) blocks it.
			if ev.Ticket != "" && !m.blocked(ev) {
				m.setRow(ev.Ticket, rowStopped, "")
			}
		case dispatch.EvStop, dispatch.EvDone:
			if ev.Kind == dispatch.EvStop {
				m.blocked(ev) // the ticket the run stopped over, as it merged, with nothing else running
				for i := range m.rows {
					if m.rows[i].state == rowWorking {
						m.rows[i].state = rowStopped
					}
				}
			}
			// main prints the last line after the program exits, below the run's summary.
			m.final, m.solo = &ev, dispatch.SoloState{}
			return m, tea.Quit
		}
	case Finished:
		return m, tea.Quit
	}
	return m, nil
}

// View is the whole display: title, totals, the tickets of this run, and the current tickets. It is
// drawn on the alternate screen, which closes with the program; main then prints the run's summary
// on the normal screen (Printer.Summary).
func (m Dashboard) View() string {
	if m.height == 0 {
		return "" // not sized yet: a frame drawn for a guessed size can outgrow the pane and leave scraps
	}
	v := m.frame(max(m.width, 30))
	if m.width >= 30 {
		return v
	}
	// A pane narrower than the frame can be drawn in shows its left edge.
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n")
}

// frame is the view at width w.
func (m Dashboard) frame(w int) string {
	title := m.titleLine(w)
	hint := m.hintLine(w) // none with no key to name
	if m.draining {
		hint = stack(m.windDownLine(w), hint)
	}
	if warn := m.cfg.MCPWarning(); warn != "" { // in the log once; here for the whole run
		hint = stack(deferredStyle.Render(ansi.Truncate(" ! "+warn, w, "…")), hint)
	}
	if !m.asking {
		return m.layout(w, title, hint)
	}
	// The question goes in a box over the middle of the dashboard, or, where the pane is too
	// small for one, on a line above the hint.
	modal := m.modal(w)
	if v := m.layout(w, title, hint); w >= 36 && lipgloss.Height(v) >= lipgloss.Height(modal)+2 {
		return overlay(v, modal, w)
	}
	return m.layout(w, title, stack(m.promptLine(w), hint))
}

// summary is the run's summary, printed on the normal screen once the dashboard has closed: the
// title, the totals and every ticket of the run, at width w and however many lines that takes.
func (m Dashboard) summary(w int) string {
	if len(m.rows) == 0 {
		return stack(m.titleLine(w), m.statsTable(w))
	}
	// Every row: the tickets table's header, rule and borders take 4 lines.
	return stack(m.titleLine(w), m.statsTable(w), m.ticketsTable(w, len(m.rows)+4))
}

// tableLines is how many lines the tickets table takes in a pane h lines tall: about a third of
// it, for 3 to 10 tickets besides its header, rule and borders. It changes only on a resize.
func tableLines(h int) int { return min(max(h/3-4, 3), 10) + 4 }

// lineCount is how many lines s takes on screen: none when it is empty.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return lipgloss.Height(s)
}

// footLines is how many lines the foot takes below the workers with a winding-down line drain lines
// tall, there or not: the MCP warning, if any, that line and the hint.
func (m Dashboard) footLines(drain int) int {
	n := drain + 1
	if m.cfg.MCPWarning() != "" {
		n++
	}
	return n
}

// layout fits the title, totals, tickets and workers above footer into the pane: Bubble Tea
// can't redraw a view taller than the terminal. The tickets table's height comes from the pane's
// alone (tableLines), and blank rows fill it, so it stays put while workers come, report and go; it
// is left out of a pane too short for it and one line per worker the run may have. The workers get
// what is left: a box each, or one line each, chosen with room for the winding-down line so that
// line doesn't change it. Below that the layout gives way step by step: the totals on one line, then
// the Current label, then cut, keeping footer. An empty footer takes no line.
func (m Dashboard) layout(w int, title, footer string) string {
	var foot []string
	if footer != "" {
		foot = strings.Split(footer, "\n")
	}
	// The room the workers' form is chosen for, the winding-down line there or not; its height changes
	// only as workers start and finish.
	steady := m.height - max(len(foot), m.footLines(lineCount(m.windDownLine(w))))
	fits := func(room int, parts ...string) bool { return lineCount(stack(parts...)) <= room }
	with := func(parts ...string) string { return stack(append(slices.Clone(parts), footer)...) }
	stats := m.statsTable(w)
	panels, list := current(m.workerPanels(w)), current(m.workerList(w))
	// One line per worker the run may have, in a box headed Current, and a one-line winding-down line.
	need := lineCount(title) + lineCount(stats) + max(m.cfg.Concurrency, 1) + 3 + m.footLines(1)
	if lines := tableLines(m.height); need+lines <= m.height {
		top := stack(title, stats, m.ticketsTable(w, lines))
		for _, workers := range []string{panels, list} {
			if fits(steady, top, workers) {
				return with(top, workers)
			}
		}
		// More workers than the run may have, or a winding-down line that wraps: the workers give way.
		return m.cut(stack(top, m.workerList(w)), foot)
	}
	for _, workers := range []string{panels, list} {
		if fits(steady, title, stats, workers) {
			return with(title, stats, workers)
		}
	}
	top := stack(title, m.statsLine(w))
	if fits(m.height-len(foot), top, list) {
		return with(top, list)
	}
	return m.cut(stack(top, m.workerList(w)), foot)
}

// cut keeps as much of body from the top as fits above foot, and of foot what fits in the pane.
func (m Dashboard) cut(body string, foot []string) string {
	lines := strings.Split(body, "\n")
	if keep := max(m.height-len(foot), 0); len(lines) > keep {
		lines = lines[:keep]
	}
	lines = append(lines, foot...)
	if len(lines) > m.height {
		lines = lines[len(lines)-m.height:]
	}
	return strings.Join(lines, "\n")
}

// shortVersion shortens a Go pseudo-version for display: v0.1.2-0.20260930072042-09ffc8431bb5+dirty
// becomes "v0.1.2-dev 09ffc84+dirty". Tags and other versions are shown as they are.
func shortVersion(v string) string {
	base, rest, ok := strings.Cut(v, "-0.")
	if !ok {
		return v
	}
	stamp, hash, ok := strings.Cut(rest, "-")
	if !ok || len(stamp) != 14 {
		return v
	}
	dirty := ""
	if h, d, found := strings.Cut(hash, "+"); found {
		hash, dirty = h, "+"+d
	}
	if len(hash) > 7 {
		hash = hash[:7]
	}
	return base + "-dev " + hash + dirty
}

// titleLine is the Orchestra pill, the version, whether the run is stopping, the solo ticket
// running alone or next, the branch, the ticket a scoped run works on and how long the run has
// gone. The states come first so a narrow pane cuts the branch and time instead. A run winding
// down says so on its own line above the hint.
func (m Dashboard) titleLine(w int) string {
	line := titleStyle.Render("Orchestra") + " " + dimStyle.Render(shortVersion(m.cfg.Version))
	if m.stopping {
		line += stopStyle.Render("  · stopping")
	}
	switch {
	case m.solo.Next:
		line += deferredStyle.Render("  · solo " + m.solo.Ticket + " next")
	case m.solo.Ticket != "":
		line += pickedStyle.Render("  · solo " + m.solo.Ticket + " running")
	}
	line += "   " + dimStyle.Render(fmt.Sprintf("%s%s · %s",
		m.cfg.Base, dispatch.ScopeLabel(m.cfg), time.Since(m.began).Truncate(time.Second)))
	return ansi.Truncate(line, w, "…")
}

// Interrupted reports whether the run was stopped with Ctrl+C.
func (m Dashboard) Interrupted() bool { return m.interrupted }

// Final is the event the run ended with, or nil.
func (m Dashboard) Final() *dispatch.Event { return m.final }

// Received is the number of the loop's events the dashboard received.
func (m Dashboard) Received() int { return m.received }
