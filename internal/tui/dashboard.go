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
	"github.com/noesis-sol/orchestra/internal/load"
)

type eventMsg dispatch.Event
type statusMsg dispatch.Status
type loadMsg load.Reading

// Finished tells the dashboard the run has returned, so it quits.
type Finished struct{}

// Dashboard is the live view of a run, a Bubble Tea model.
type Dashboard struct {
	cfg  dispatch.Config
	spin spinner.Model
	// records: every ticket of the run, its row and its worker's status, in the rows' order (oldest
	// first, those that came to an end in the order they did)
	records     []record
	stopping    bool          // something stopped the run; the running ones are finishing
	limited     bool          // the run holds for the account's usage limit
	limitReset  time.Time     // when that limit resets; zero when not known
	shared      int           // the other orchestra runs going on the machine
	load        *load.Reading // the machine's load as last read; nil until the loop first reads it
	draining    bool          // the maintainer asked to stop after the running tickets
	drainFrom   []string      // the tickets running when the run began to wind down, oldest first
	asking      bool          // the question whether to stop after them, or to go on, is open
	closed      int
	triaged     int
	width       int
	height      int
	interrupted bool
	final       *dispatch.Event     // the stop or done event, printed by main after exit
	received    int                 // events received before it quit, for ProgramSink.Handoff
	quitting    bool                // it has asked to quit: the events still coming are for the printer
	queue       dispatch.QueueState // as the loop last said; its Queued and Unclosed -1 until it first says
	began       time.Time
	now         func() time.Time // the clock the times shown are read from
	cancel      func()
	drain       func(on bool)    // asks the loop to stop after the running tickets, or with false to go on
	focus       func(tab string) // switches the terminal to a worker's tab; it must not block; nil for none
	// The answer talk, where the maintainer answers the questions tickets wait on (WithAnswer):
	answer       *AnswerTalk // nil where it can't be opened
	answerAuto   bool        // it opens by itself, unfocused, as soon as a question waits
	answering    bool        // it is open: AnswerOver says when it is over
	offered      []string    // the questions it has been opened or queued for
	queued       []string    // those queued into it while open
	answered     []string    // the questions it answered, whose tickets the loop hasn't taken back yet
	answerFailed string      // why it last failed, shown until it opens again
	// idleQuestions are those the loop, with nothing else ready, asked the talk for (AskQuestions), and
	// talkOver what it waits on until the talk is over
	idleQuestions []string
	talkOver      []chan<- struct{}
}

// NewDashboard returns the run's dashboard. Ctrl+C calls cancel; s, once confirmed, calls drain;
// a worker's number calls focus with its tab, unless focus is nil, as when the workers' tabs are out
// of orchestra's sight.
func NewDashboard(cfg dispatch.Config, cancel func(), drain func(on bool), focus func(tab string)) Dashboard {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(pickedStyle))
	return Dashboard{cfg: cfg, spin: s, width: 80, queue: dispatch.QueueState{Queued: -1, Unclosed: -1},
		began: time.Now(), now: time.Now, cancel: cancel, drain: drain, focus: focus}
}

// StartedAt is the dashboard of a run whose loop started at t, before the dashboard opened (a spec's
// breakdown went on as the loop started): its run time counts from t.
func (m Dashboard) StartedAt(t time.Time) Dashboard {
	m.began = t
	return m
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
		case a.answer:
			m.answer.Open(true) // the callback opens the talk in the background
		case a.focusAnswer:
			m.answer.Focus() // the callback runs the terminal in the background
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case statusMsg:
		m.applyStatus(dispatch.Status(msg))
	case loadMsg:
		r := load.Reading(msg)
		m.load = &r
	case eventMsg:
		ev := dispatch.Event(msg)
		if m.apply(ev) {
			return m, tea.Quit
		}
		m.takeQuestions()
	case AnswerOver:
		m.answerOver(msg)
	case AskQuestions:
		m.askQuestions(msg)
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

// View is the whole display: title, the load line and totals, the tickets of this run, and the
// current tickets. It is
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
	foot := m.footer(w)
	if !m.asking {
		return m.layout(w, title, foot)
	}
	// The question goes in a box over the middle of the dashboard, or, where the pane is too
	// small for one, on a line above the hint.
	modal := m.modal(w)
	if v := m.layout(w, title, foot); w >= modalMinWidth && lipgloss.Height(v) >= lipgloss.Height(modal)+modalRoom {
		return overlay(v, modal, w)
	}
	return m.layout(w, title, foot.below(m.promptLine(w)))
}

// summary is the run's summary, printed on the normal screen once the dashboard has closed: the
// title, the totals and every ticket of the run, at width w and however many lines that takes.
func (m Dashboard) summary(w int) string {
	rows := len(m.rows())
	if rows == 0 {
		return stack(m.titleLine(w), m.statsTable(w))
	}
	// Every row, and the Tickets label and the table's borders.
	return stack(m.titleLine(w), m.statsTable(w), m.tickets(w, rows+sectionChrome))
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

// footer is the foot of the dashboard, below the workers, and the room the layout keeps for it.
type footer struct {
	text string // its lines, stacked; empty for none
	// room is the lines the workers' form is chosen around: the foot's own, with the winding-down line's
	// and the hint's counted whether they show or not, so that neither changes the form as it comes and goes.
	room int
}

// footFixedLines is the lines the foot always has room for: the winding-down line, there or not,
// and the hint.
const footFixedLines = 2

// footer is the foot at width w: the way to see the workers' tabs, the MCP warning and why the answer
// talk failed, if any, the winding-down line while the run winds down, and the hint, if it has a key
// to name.
func (m Dashboard) footer(w int) footer {
	var watch, warn, windDown string
	room := footFixedLines
	if m.cfg.Watch != "" { // the workers' tabs are out of sight: the number keys can't go to them
		watch = dimStyle.Render(ansi.Truncate(" to see the workers' tabs: "+m.cfg.Watch, w, "…"))
		room++
	}
	if msg := m.cfg.MCPWarning(); msg != "" { // in the log once; here for the whole run
		warn = deferredStyle.Render(ansi.Truncate(" ! "+msg, w, "…"))
		room++
	}
	failed := m.answerFailedLine(w) // until the talk opens again
	if failed != "" {
		room++
	}
	if m.draining {
		windDown = m.windDownLine(w)
	}
	return footer{text: stack(watch, warn, failed, windDown, m.hintLine(w)), room: room}
}

// below is the foot below line, which the room kept for the foot doesn't count.
func (f footer) below(line string) footer {
	f.text = stack(line, f.text)
	return f
}

// steadyLines is the lines the foot takes from the workers' form: its room, or its lines where more.
func (f footer) steadyLines() int { return max(len(f.lines()), f.room) }

// lines is the foot's lines, top first: none when it is empty.
func (f footer) lines() []string {
	if f.text == "" {
		return nil
	}
	return strings.Split(f.text, "\n")
}

// slots is how many workers the run may have at once: its concurrency, one at the least.
func (m Dashboard) slots() int { return max(m.cfg.Concurrency, 1) }

// layout fits the title, totals, tickets and workers above the foot into the pane: Bubble Tea
// can't redraw a view taller than the terminal. The tickets table's height, its Tickets label
// included, comes from the pane's alone (tableLines), and blank rows fill it, so it stays put while
// workers come, report and go; it is left out of a pane too short for it and one line per worker the
// run may have. A blank line sets the title apart from the load line where the pane has room for it
// too, and is the first to go. The workers get what is left: a box each, or one line each, chosen with
// room for the winding-down line so that line doesn't change it. Below that the layout gives way step
// by step: the totals on one line, without the load line, which has the workers they then carry, then
// the Current label and the Tickets label with it, then cut, keeping the foot.
// An empty foot takes no line.
func (m Dashboard) layout(w int, title string, f footer) string {
	foot := f.lines()
	// The room the workers' form is chosen for, the winding-down line there or not; its height changes
	// only as workers start and finish.
	steady := m.height - f.steadyLines()
	// The parts' lines are counted rather than stacked: stacking measures every line's width.
	fits := func(room int, parts ...string) bool {
		n := 0
		for _, p := range parts {
			n += lineCount(p)
		}
		return n <= room
	}
	with := func(parts ...string) string { return stack(append(slices.Clone(parts), f.text)...) }
	stats := stack(m.loadLine(w), m.statsTable(w))
	// The one-line form is drawn only once the boxes don't fit.
	panels, list := withCurrentLabel(m.workerPanels(w)), ""
	listed := false
	compact := func() string {
		if !listed {
			list, listed = withCurrentLabel(m.workerList(w)), true
		}
		return list
	}
	// Current in its one-line form with every worker the run may have, and the foot at its steady height.
	need := lineCount(title) + lineCount(stats) + m.currentListLines() + f.room
	lines := tableLines(m.height)
	var tickets string
	if need+lines <= m.height {
		// Current is drawn below it, whole: the table leaves out the tickets it shows.
		tickets, need = m.ticketsBesideCurrent(w, lines), need+lines
	}
	// The title with the blank line below it, in a pane with a line to spare for it beside the rest
	// at its steady height, so that it doesn't come and go as workers do; then the title alone.
	heads := []string{title}
	if need < m.height {
		heads = []string{title + "\n", title}
	}
	for _, head := range heads {
		for _, workers := range []func() string{func() string { return panels }, compact} {
			if fits(steady, head, stats, tickets, workers()) {
				return with(head, stats, tickets, workers())
			}
		}
	}
	if tickets != "" {
		// More workers than the run may have: the labels go, then the workers give way.
		return m.cut(stack(title, stats, m.rowsTable(w, lines-labelLines, m.rowsBesideCurrent()), m.workerList(w)), foot)
	}
	top := stack(title, m.statsLine(w))
	// A pane too short for a steady form: the workers get every line the foot leaves as it is now,
	// the winding-down line's included while there is none.
	if list := compact(); fits(m.height-len(foot), top, list) {
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

// titleLine is the Orchestra pill, the version, an overnight run's badge, whether the run is stopping,
// whether it holds for the usage limit and when that resets, whether other runs share the machine and
// whether the run holds for their cap on workers, the solo ticket running alone or next, the branch, the
// ticket a scoped run works on and how long the run has gone. The states come first so a narrow pane cuts
// the branch and time instead; the badge, which says nothing that changes during the run, gives way
// before them. A run winding down says so on its own line above the hint.
func (m Dashboard) titleLine(w int) string {
	if m.cfg.Overnight {
		if line := m.titleLineWith(" " + overnightStyle.Render("overnight")); ansi.StringWidth(line) <= w {
			return line
		}
	}
	return ansi.Truncate(m.titleLineWith(""), w, "…")
}

// titleLineWith is the title line in full, with badge (an overnight run's, or "") after the version.
func (m Dashboard) titleLineWith(badge string) string {
	line := titleStyle.Render("Orchestra") + " " + dimStyle.Render(shortVersion(m.cfg.Version)) + badge
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
	if m.shared > 0 {
		line += deferredStyle.Render(sharedLabel(m.shared))
	}
	if m.queue.MachineFull {
		line += deferredStyle.Render("  · machine full")
	}
	switch {
	case m.queue.Solo.Next:
		line += deferredStyle.Render("  · solo " + oneLine(m.queue.Solo.Ticket) + " next")
	case m.queue.Solo.Ticket != "":
		line += pickedStyle.Render("  · solo " + oneLine(m.queue.Solo.Ticket) + " running")
	}
	line += "   " + dimStyle.Render(fmt.Sprintf("%s%s · %s",
		m.cfg.Base, dispatch.ScopeLabel(m.cfg), runTime(m.since(m.began))))
	return line
}

// sharedLabel is the title line's note that n other orchestra runs share the machine.
func sharedLabel(n int) string {
	if n == 1 {
		return "  · shared with 1 run"
	}
	return fmt.Sprintf("  · shared with %d runs", n)
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
