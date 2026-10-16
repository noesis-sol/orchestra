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
	limited     bool                       // the run holds for the account's usage limit
	limitReset  time.Time                  // when that limit resets; zero when not known
	draining    bool                       // the maintainer asked to stop after the running tickets
	drainFrom   []string                   // the tickets running when the run began to wind down, oldest first
	asking      bool                       // the question whether to stop after them, or to go on, is open
	closed      int
	triaged     int
	width       int
	height      int
	rows        []ticketRow // every ticket picked up in this run, oldest first
	interrupted bool
	final       *dispatch.Event   // the stop or done event, printed by main after exit
	received    int               // events received before it quit, for ProgramSink.Handoff
	quitting    bool              // it has asked to quit: the events still coming are for the printer
	queued      int               // ready tickets waiting for a slot; -1 until the loop first says
	held        dispatch.HeldBack // ready tickets held back, waiting for something besides a slot
	unclosed    int               // the tickets not closed the run draws from; -1 until the loop says
	solo        dispatch.SoloState
	began       time.Time
	now         func() time.Time // the clock the times shown are read from
	cancel      func()
	drain       func(on bool)    // asks the loop to stop after the running tickets, or with false to go on
	focus       func(tab string) // switches the terminal to a worker's tab; it must not block; nil for none
}

// NewDashboard returns the run's dashboard. Ctrl+C calls cancel; s, once confirmed, calls drain;
// a worker's number calls focus with its tab, unless focus is nil, as when the workers' tabs are out
// of orchestra's sight.
func NewDashboard(cfg dispatch.Config, cancel func(), drain func(on bool), focus func(tab string)) Dashboard {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(pickedStyle))
	return Dashboard{cfg: cfg, spin: s, width: 80, queued: -1, unclosed: -1, began: time.Now(), now: time.Now,
		cancel: cancel, drain: drain, focus: focus}
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
		switch a := m.press(msg.String()); {
		case a.quit:
			m.cancel()
			return m, tea.Quit
		case a.drain != nil:
			m.drain(*a.drain)
		case a.focusTab != "":
			m.focus(a.focusTab) // the callback runs the terminal in the background
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case statusMsg:
		m.applyStatus(dispatch.Status(msg))
	case eventMsg:
		if m.apply(dispatch.Event(msg)) {
			return m, tea.Quit
		}
	case Finished:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// setDraining turns the wind-down on or off. Turned on, it remembers the tickets running then, whose
// squares the winding-down line fills in as they finish; asked again, as y and then the loop's event
// do, it keeps them.
func (m *Dashboard) setDraining(on bool) {
	switch {
	case !on:
		m.drainFrom = nil
	case !m.draining:
		m.drainFrom = m.runningIDs()
	}
	m.draining = on
}

// minFrameWidth is the narrowest the dashboard is drawn: a narrower pane shows the left edge of a
// frame this wide, and the run's summary is printed at least this wide.
const minFrameWidth = 30

// View is the whole display: title, totals, the tickets of this run, and the current tickets. It is
// drawn on the alternate screen, which closes with the program; main then prints the run's summary
// on the normal screen (Printer.Summary).
func (m Dashboard) View() string {
	if m.height == 0 {
		return "" // not sized yet: a frame drawn for a guessed size can outgrow the pane and leave scraps
	}
	v := m.frame(max(m.width, minFrameWidth))
	if m.width >= minFrameWidth {
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
	if m.cfg.Watch != "" { // the workers' tabs are out of sight: the number keys can't go to them
		hint = stack(dimStyle.Render(ansi.Truncate(" to see the workers' tabs: "+m.cfg.Watch, w, "…")), hint)
	}
	if !m.asking {
		return m.layout(w, title, hint)
	}
	// The question goes in a box over the middle of the dashboard, or, where the pane is too
	// small for one, on a line above the hint.
	modal := m.modal(w)
	if v := m.layout(w, title, hint); w >= modalMinWidth && lipgloss.Height(v) >= lipgloss.Height(modal)+modalRoom {
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
	// Every row, and the Tickets label and the table's borders.
	return stack(m.titleLine(w), m.statsTable(w), m.tickets(w, len(m.rows)+sectionChrome))
}

// The lines a section of the dashboard, Tickets or Current, takes besides its rows: its label, and
// its box's top and bottom borders.
const (
	labelLines    = 1
	borderLines   = 2
	sectionChrome = labelLines + borderLines
)

// How many tickets the tickets table has rows for, in a pane short or tall.
const (
	tableMinRows = 3
	tableMaxRows = 10
)

// tableLines is how many lines the tickets table takes in a pane h lines tall: about a third of
// it, less a line, its Tickets label and borders included, for tableMinRows to tableMaxRows
// tickets. It changes only on a resize.
func tableLines(h int) int {
	return min(max(h/3-1-sectionChrome, tableMinRows), tableMaxRows) + sectionChrome
}

// lineCount is how many lines s takes on screen: none when it is empty.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return lipgloss.Height(s)
}

// footFixedLines is the lines the foot always has room for: the winding-down line, there or not,
// and the hint.
const footFixedLines = 2

// footLines is how many lines the foot takes below the workers, the winding-down line there or not:
// the MCP warning and the way to see the workers' tabs, if any, that line and the hint.
func (m Dashboard) footLines() int {
	n := footFixedLines
	if m.cfg.MCPWarning() != "" {
		n++
	}
	if m.cfg.Watch != "" {
		n++
	}
	return n
}

// slots is how many workers the run may have at once: its concurrency, one at the least.
func (m Dashboard) slots() int { return max(m.cfg.Concurrency, 1) }

// layout fits the title, totals, tickets and workers above footer into the pane: Bubble Tea
// can't redraw a view taller than the terminal. The tickets table's height, its Tickets label
// included, comes from the pane's alone (tableLines), and blank rows fill it, so it stays put while
// workers come, report and go; it is left out of a pane too short for it and one line per worker the
// run may have. The workers get what is left: a box each, or one line each, chosen with room for the
// winding-down line so that line doesn't change it. Below that the layout gives way step by step: the
// totals on one line, then the Current label and the Tickets label with it, then cut, keeping footer.
// An empty footer takes no line.
func (m Dashboard) layout(w int, title, footer string) string {
	var foot []string
	if footer != "" {
		foot = strings.Split(footer, "\n")
	}
	// The room the workers' form is chosen for, the winding-down line there or not; its height changes
	// only as workers start and finish.
	steady := m.height - max(len(foot), m.footLines())
	fits := func(room int, parts ...string) bool { return lineCount(stack(parts...)) <= room }
	with := func(parts ...string) string { return stack(append(slices.Clone(parts), footer)...) }
	stats := m.statsTable(w)
	panels, list := withCurrentLabel(m.workerPanels(w)), withCurrentLabel(m.workerList(w))
	// One line per worker the run may have, in a box headed Current, and a one-line winding-down line.
	need := lineCount(title) + lineCount(stats) + m.slots() + sectionChrome + m.footLines()
	if lines := tableLines(m.height); need+lines <= m.height {
		top := stack(title, stats, m.tickets(w, lines))
		for _, workers := range []string{panels, list} {
			if fits(steady, top, workers) {
				return with(top, workers)
			}
		}
		// More workers than the run may have: the labels go, then the workers give way.
		return m.cut(stack(title, stats, m.ticketsTable(w, lines-labelLines), m.workerList(w)), foot)
	}
	for _, workers := range []string{panels, list} {
		if fits(steady, title, stats, workers) {
			return with(title, stats, workers)
		}
	}
	top := stack(title, m.statsLine(w))
	// A pane too short for a steady form: the workers get every line the foot leaves as it is now,
	// the winding-down line's included while there is none.
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

// titleLine is the Orchestra pill, the version, whether the run is stopping, whether it holds for the
// usage limit and when that resets, the solo ticket running alone or next, the branch, the ticket a
// scoped run works on and how long the run has gone. The states come first so a narrow pane cuts the
// branch and time instead. A run winding down says so on its own line above the hint.
func (m Dashboard) titleLine(w int) string {
	line := titleStyle.Render("Orchestra") + " " + dimStyle.Render(shortVersion(m.cfg.Version))
	if m.stopping {
		line += stopStyle.Render("  · stopping")
	}
	if m.limited {
		limit := "  · usage limit"
		if !m.limitReset.IsZero() {
			limit += " · resets " + dispatch.ResetLabel(m.limitReset, m.clock())
		}
		line += deferredStyle.Render(limit)
	}
	switch {
	case m.solo.Next:
		line += deferredStyle.Render("  · solo " + oneLine(m.solo.Ticket) + " next")
	case m.solo.Ticket != "":
		line += pickedStyle.Render("  · solo " + oneLine(m.solo.Ticket) + " running")
	}
	line += "   " + dimStyle.Render(fmt.Sprintf("%s%s · %s",
		m.cfg.Base, dispatch.ScopeLabel(m.cfg), runTime(m.since(m.began))))
	return ansi.Truncate(line, w, "…")
}

// since is how long ago t was by the dashboard's clock; a dashboard made without NewDashboard reads
// the time.
func (m Dashboard) since(t time.Time) time.Duration {
	return m.clock().Sub(t)
}

// clock is the time by the dashboard's clock; a dashboard made without NewDashboard reads the time.
func (m Dashboard) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

// Interrupted reports whether the run was stopped with Ctrl+C.
func (m Dashboard) Interrupted() bool { return m.interrupted }

// Final is the event the run ended with, or nil.
func (m Dashboard) Final() *dispatch.Event { return m.final }

// Received is the number of the loop's events the dashboard received before it asked to quit.
func (m Dashboard) Received() int { return m.received }
