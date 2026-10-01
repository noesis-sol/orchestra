// Package tui is orchestra's terminal interface: the run's dashboard (Bubble Tea), plain output
// for pipes, and orchestra init's form and summary.
package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Exact colours rather than the 16 ANSI palette slots, which terminal themes remap (one theme
// draws "cyan" as near-white). Each has a variant for light and for dark backgrounds.
var (
	cyan   = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#22D3EE"}
	green  = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	yellow = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FACC15"}
	red    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	grey   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"}
	purple = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
)

var (
	dimStyle      = lipgloss.NewStyle().Faint(true)
	pickedStyle   = lipgloss.NewStyle().Foreground(cyan).Bold(true)  // picked up
	closedStyle   = lipgloss.NewStyle().Foreground(green).Bold(true) // completed
	deferredStyle = lipgloss.NewStyle().Foreground(yellow)           // set aside
	stopStyle     = lipgloss.NewStyle().Foreground(red).Bold(true)   // needs you
	doneStyle     = lipgloss.NewStyle().Foreground(green)
	organStyle    = lipgloss.NewStyle().Foreground(purple).Bold(true) // an organ's output
	testingStyle  = lipgloss.NewStyle().Foreground(yellow).Bold(true) // a worker running checks
	// The Charm purple pill from the Bubble Tea and Lip Gloss examples.
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).Padding(0, 1).MarginTop(1)
	home, _ = os.UserHomeDir()
)

// Tildify shortens paths under the home directory for display; the log keeps full paths.
func Tildify(s string) string {
	if home == "" {
		return s
	}
	return strings.ReplaceAll(s, home+"/", "~/")
}

// renderEvent formats one event as a permanent line above the live status area.
func renderEvent(ev dispatch.Event) string {
	ts := dimStyle.Render(ev.Time.Format("15:04:05"))
	switch ev.Kind {
	case dispatch.EvDispatch:
		solo := ""
		if ev.Solo.Ticket == ev.Ticket && !ev.Solo.Next {
			solo = dimStyle.Render(" solo")
		}
		return fmt.Sprintf("%s %s %s%s  %s", ts, dimStyle.Render(fmt.Sprintf("▶ [%d/%d]", ev.N, ev.Limit)),
			pickedStyle.Render(ev.Ticket), solo, ev.Title)
	case dispatch.EvClosed:
		return fmt.Sprintf("%s %s  %s", ts, closedStyle.Render("✓ "+ev.Ticket+" completed"), dimStyle.Render(ev.Detail))
	case dispatch.EvDeferred:
		return fmt.Sprintf("%s %s  %s", ts, deferredStyle.Render("↷ "+ev.Ticket+" deferred"), dimStyle.Render(ev.Detail))
	case dispatch.EvTriage:
		return fmt.Sprintf("%s %s  %s", ts, organStyle.Render("◆ "+ev.Ticket+" triage: "+ev.Detail), dimStyle.Render(ev.Title))
	case dispatch.EvAsked:
		return fmt.Sprintf("%s %s  %s", ts, stopStyle.Render("? "+ev.Ticket+" needs your answer"), dimStyle.Render(ev.Detail))
	case dispatch.EvHold:
		return fmt.Sprintf("%s %s", ts, stopStyle.Render("■ "+Tildify(ev.Text)))
	case dispatch.EvWarn:
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("! "+Tildify(strings.TrimSpace(ev.Text))))
	case dispatch.EvStop:
		return fmt.Sprintf("%s %s", ts, stopStyle.Render("■ "+Tildify(ev.Text)))
	case dispatch.EvDone:
		return fmt.Sprintf("%s %s", ts, doneStyle.Render("■ "+ev.Text))
	case dispatch.EvDrain, dispatch.EvResume:
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("■ "+ev.Text))
	case dispatch.EvProbed:
		return fmt.Sprintf("%s %s", ts, closedStyle.Render("■ "+ev.Text))
	}
	return fmt.Sprintf("%s %s", ts, dimStyle.Render(Tildify(ev.Text)))
}

// ---- Bubble Tea model ----------------------------------------------------------------

type eventMsg dispatch.Event
type statusMsg dispatch.Status
type Finished struct{}

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
	asked       int
	width       int
	height      int
	rows        []ticketRow // every ticket picked up in this run, oldest first
	quitting    bool
	interrupted bool
	final       *dispatch.Event // the stop or done event, printed by main after exit
	received    int             // events received, for ProgramSink.Handoff
	queued      int             // ready tickets waiting for a slot; -1 until the loop first says
	solo        dispatch.SoloState
	began       time.Time
	cancel      func()
	drain       func(on bool) // asks the loop to stop after the running tickets, or with false to go on
}

// NewDashboard returns the run's dashboard. Ctrl+C calls cancel; s, once confirmed, calls drain.
func NewDashboard(cfg dispatch.Config, cancel func(), drain func(on bool)) Dashboard {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(pickedStyle))
	return Dashboard{cfg: cfg, spin: s, width: 80, queued: -1, began: time.Now(), cancel: cancel, drain: drain}
}

func (m Dashboard) Init() tea.Cmd { return m.spin.Tick }

func (m Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch key := msg.String(); {
		case key == "ctrl+c": // at any time, the question open or not
			m.interrupted, m.quitting = true, true
			m.cancel()
			return m, tea.Quit
		case m.asking && key == "y":
			m.asking, m.draining = false, !m.draining
			m.drain(m.draining)
		case m.asking && (key == "n" || key == "esc"):
			m.asking = false
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
			m.rows = append(m.rows, ticketRow{id: ev.Ticket, title: ev.Title, state: rowWorking})
		case dispatch.EvQueue:
			m.queued, m.solo = ev.Queued, ev.Solo
		case dispatch.EvClosed:
			m.closed++
			m.setRow(ev.Ticket, rowDone, ev.Detail)
		case dispatch.EvDeferred:
			m.deferred++
			m.setRow(ev.Ticket, rowDeferred, ev.Detail)
		case dispatch.EvWarn:
			if ev.Ticket != "" {
				m.setRow(ev.Ticket, rowReview, "left for review, see the log")
			}
		case dispatch.EvAsked:
			m.asked++
			m.setRow(ev.Ticket, rowAsked, "answer "+ev.Detail)
		case dispatch.EvTriage:
			m.triaged++
			if i := m.rowIndex(ev.Ticket); i >= 0 {
				m.rows[i].triage = ev.Detail + " · " + ev.Title
			}
		case dispatch.EvDrain, dispatch.EvResume: // as asked here, or with SIGUSR1
			m.draining = ev.Kind == dispatch.EvDrain
		case dispatch.EvProbed: // the machine works again after an environment hold
			m.stopping = false
		case dispatch.EvHold:
			m.stopping = true
			if ev.Ticket != "" { // a stop found before dispatching belongs to no ticket
				m.setRow(ev.Ticket, rowStopped, "")
			}
		case dispatch.EvStop, dispatch.EvDone:
			if ev.Kind == dispatch.EvStop {
				for i := range m.rows {
					if m.rows[i].state == rowWorking {
						m.rows[i].state = rowStopped
					}
				}
			}
			// main prints the last line after the program exits, below the final dashboard.
			m.final, m.quitting, m.solo = &ev, true, dispatch.SoloState{}
			return m, tea.Quit
		}
	case Finished:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// View is the whole display: title, totals, the tickets of this run, and the active ticket. When
// the program quits it renders once more without the active ticket, which stays on screen as the
// run's summary.
func (m Dashboard) View() string {
	w := max(m.width, 30)
	title := m.titleLine(w)
	if m.quitting {
		return lipgloss.JoinVertical(lipgloss.Left, title, m.statsTable(w), m.ticketsTable(w, 1000)) + "\n"
	}
	if m.height == 0 {
		return "" // not sized yet: a frame drawn for a guessed size can outgrow the pane and leave scraps
	}
	hint := m.hintLine(w)
	if !m.asking {
		return m.layout(w, title, hint)
	}
	// The question goes in a box over the middle of the dashboard, or, where the pane is too
	// small for one, on a line above the hint.
	modal := m.modal(w)
	if v := m.layout(w, title, hint); w >= 36 && lipgloss.Height(v) >= lipgloss.Height(modal)+2 {
		return overlay(v, modal, w)
	}
	return m.layout(w, title, lipgloss.JoinVertical(lipgloss.Left, m.promptLine(w), hint))
}

// layout fits the title, totals, tickets and workers above footer into the pane: Bubble Tea
// can't redraw a view taller than the terminal. It gives way step by step: one line per worker
// instead of a box each, then no tickets table, then the totals on one line, then cut, keeping
// footer.
func (m Dashboard) layout(w int, title, footer string) string {
	stats := m.statsTable(w)
	fits := func(v string) bool { return lipgloss.Height(v) <= m.height }
	compose := func(stats, panels string) string {
		// Show as many recent tickets as fit around the rest; none if that's fewer than three.
		room := m.height - lipgloss.Height(title) - 1 - lipgloss.Height(stats) - lipgloss.Height(panels) - lipgloss.Height(footer)
		parts := []string{title, stats}
		if room >= 7 {
			parts = append(parts, m.ticketsTable(w, room))
		}
		return lipgloss.JoinVertical(lipgloss.Left, append(parts, panels, footer)...)
	}
	if v := compose(stats, m.workerPanels(w)); fits(v) {
		return v
	}
	if v := compose(stats, m.workerList(w)); fits(v) {
		return v
	}
	body := strings.Split(lipgloss.JoinVertical(lipgloss.Left, title, m.statsLine(w), m.workerList(w)), "\n")
	foot := strings.Split(footer, "\n")
	if keep := max(m.height-len(foot), 0); len(body) > keep {
		body = body[:keep]
	}
	lines := append(body, foot...)
	if len(lines) > m.height {
		lines = lines[len(lines)-m.height:]
	}
	return strings.Join(lines, "\n")
}

// hintLine says which keys do what: s stops after the running tickets, or once asked goes on;
// ctrl+c stops at once. A narrow pane gets a shorter form.
func (m Dashboard) hintLine(w int) string {
	long, short := "  s stops after current · ctrl+c stops now", " s: stop after · ctrl+c: now"
	switch {
	case m.draining:
		long, short = "  s keeps going · ctrl+c stops now", " s: go on · ctrl+c: now"
	case m.stopping:
		long, short = "  ctrl+c stops now", " ctrl+c: now"
	}
	hint := long
	if ansi.StringWidth(long) > w {
		hint = short
	}
	return dimStyle.Render(ansi.Truncate(hint, w, "…"))
}

// runningIDs names the running tickets, oldest first.
func (m Dashboard) runningIDs() string {
	var ids []string
	for _, st := range m.Running() {
		ids = append(ids, st.Ticket)
	}
	return strings.Join(ids, ", ")
}

// modal is the question s asks, in a box: whether to stop after the running tickets, or, while
// the run winds down, whether to take tickets again.
func (m Dashboard) modal(w int) string {
	var question, about, keys string
	switch n := len(m.active); {
	case m.draining:
		question, about = "Keep taking tickets?", "New tickets start again as slots free up."
		keys = "y keep going · n keep stopping"
	case n == 0:
		question, about = "Stop after the running tickets?", "No new tickets will start. Nothing is running, so the run ends now."
		keys = "y stop · n keep going"
	default:
		question = "Stop after the running tickets?"
		about = fmt.Sprintf("No new tickets will start. %d running (%s) will finish and merge, then the run ends.", n, m.runningIDs())
		keys = "y stop after current · n keep going"
	}
	body := lipgloss.JoinVertical(lipgloss.Left, deferredStyle.Bold(true).Render(question), "", about, "", dimStyle.Render(keys))
	return box(min(w-4, 64), yellow, body)
}

// promptLine is the question on one line, for a pane too small for the box; the keys come first
// so a narrow pane keeps them.
func (m Dashboard) promptLine(w int) string {
	line := " Stop after current? y/n · "
	switch n := len(m.active); {
	case m.draining:
		line = " Keep taking tickets? y/n · new tickets start again as slots free up"
	case n == 0:
		line += "nothing is running, so the run ends now"
	default:
		line += fmt.Sprintf("%d running (%s) finish and merge, then the run ends", n, m.runningIDs())
	}
	return deferredStyle.Bold(true).Render(ansi.Truncate(line, w, "…"))
}

// overlay draws fg over the middle of bg, a view w wide.
func overlay(bg, fg string, w int) string {
	lines, over := strings.Split(bg, "\n"), strings.Split(fg, "\n")
	fw := lipgloss.Width(fg)
	left, top := max((w-fw)/2, 0), max((len(lines)-len(over))/2, 0)
	for i, l := range over {
		if top+i >= len(lines) {
			break
		}
		b := lines[top+i]
		head := ansi.Truncate(b, left, "")
		head += strings.Repeat(" ", max(left-ansi.StringWidth(head), 0))
		lines[top+i] = head + "\x1b[0m" + l + "\x1b[0m" + ansi.TruncateLeft(b, left+fw, "")
	}
	return strings.Join(lines, "\n")
}

// workerList is the compact form of the worker boxes: one box, one line per running worker.
func (m Dashboard) workerList(w int) string {
	running := m.Running()
	if len(running) == 0 {
		return m.workerPanels(w)
	}
	inner := w - 4
	var lines []string
	for _, st := range running {
		elapsed := time.Since(st.Started).Truncate(time.Second)
		lines = append(lines, ansi.Truncate(fmt.Sprintf("%s %s  %s  %s  %s", m.spin.View(), pickedStyle.Render(st.Ticket),
			agentStyle(doingLabel(st)), dimStyle.Render(elapsed.String()), st.Title), inner, "…"))
	}
	border := lipgloss.TerminalColor(cyan)
	for _, st := range running {
		if st.Agent == "blocked" {
			border = red
		}
	}
	return box(w, border, strings.Join(lines, "\n"))
}

// statsLine is the totals on one line, for a pane too narrow or too short for the strip. The
// branch, running time and stopping state are on the title line.
func (m Dashboard) statsLine(w int) string {
	queued := "—"
	if m.queued >= 0 {
		queued = fmt.Sprint(m.queued)
	}
	parts := []string{
		closedStyle.Render(fmt.Sprintf("✓ %d", m.closed)),
		deferredStyle.Render(fmt.Sprintf("↷ %d", m.deferred)),
		stopStyle.Render(fmt.Sprintf("? %d", m.asked)),
		dimStyle.Render("workers ") + pickedStyle.Render(fmt.Sprint(len(m.active))) + dimStyle.Render(fmt.Sprintf("/%d", max(m.cfg.Concurrency, 1))),
		dimStyle.Render("queue " + queued),
	}
	return ansi.Truncate(" "+strings.Join(parts, dimStyle.Render(" · ")), w, "…")
}

// Running returns the running workers, oldest first.
func (m Dashboard) Running() []dispatch.Status {
	var l []dispatch.Status
	for _, st := range m.active {
		l = append(l, st)
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Started.Before(l[j].Started) })
	return l
}

// workerPanels stacks a box per running worker, or one box saying what the loop is doing.
func (m Dashboard) workerPanels(w int) string {
	running := m.Running()
	if len(running) == 0 {
		msg := "picking the next ticket…"
		if m.stopping {
			msg = "stopping…"
		}
		return box(w, grey, m.spin.View()+" "+dimStyle.Render(msg))
	}
	lines := titleLines
	if len(running) > 1 {
		lines = 2 // keep several boxes within the pane
	}
	var boxes []string
	for _, st := range running {
		boxes = append(boxes, m.workerPanel(w, st, lines))
	}
	return lipgloss.JoinVertical(lipgloss.Left, boxes...)
}

// workerPanel boxes one running ticket: ID, worker status and time, title, latest action.
func (m Dashboard) workerPanel(w int, st dispatch.Status, titleMax int) string {
	inner := w - 4 // rounded border and one space of padding on each side
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	border := lipgloss.TerminalColor(cyan)
	if st.Agent == "blocked" {
		border = red
	}
	elapsed := time.Since(st.Started).Truncate(time.Second)
	lines := []string{fit(fmt.Sprintf("%s %s  %s  %s", m.spin.View(), pickedStyle.Render(st.Ticket),
		agentStyle(doingLabel(st)), dimStyle.Render(elapsed.String())))}
	for _, l := range wrapLines(st.Title, inner-2, titleMax) {
		lines = append(lines, "  "+l)
	}
	if st.Activity != "" {
		lines = append(lines, fit("  "+dimStyle.Render(st.Activity)))
	}
	return box(w, border, strings.Join(lines, "\n"))
}

func box(w int, border lipgloss.TerminalColor, content string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(w - 2).Render(content)
}

// ---- Tickets table -------------------------------------------------------------------

type rowState int

const (
	rowWorking rowState = iota
	rowDone
	rowDeferred
	rowReview
	rowStopped
	rowAsked
)

type ticketRow struct {
	id, title string
	state     rowState
	note      string // the merged commit, or why it was set aside
	triage    string // the triage organ's verdict
}

func (m *Dashboard) rowIndex(id string) int {
	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].id == id {
			return i
		}
	}
	return -1
}

func (m *Dashboard) setRow(id string, state rowState, note string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id})
		i = len(m.rows) - 1
	}
	m.rows[i].state, m.rows[i].note = state, note
}

// cells renders one row: state, ticket ID, and what to say about it. A picked-up ticket shows
// its title; a completed one only its merged commit; a set-aside one why, with triage's verdict.
func (r ticketRow) cells(width int) [3]string {
	fit := func(s string) string { return ansi.Truncate(s, max(width, 8), "…") }
	switch r.state {
	case rowDone:
		return [3]string{closedStyle.Render("✓ done"), closedStyle.Render(r.id), dimStyle.Render(fit(r.note))}
	case rowDeferred, rowReview:
		label := "↷ deferred"
		if r.state == rowReview {
			label = "! review"
		}
		about := dimStyle.Render(fit(r.note))
		if r.triage != "" {
			about = organStyle.Render(fit("◆ " + r.triage))
		}
		return [3]string{deferredStyle.Render(label), deferredStyle.Render(r.id), about}
	case rowStopped:
		return [3]string{stopStyle.Render("■ stopped"), stopStyle.Render(r.id), fit(r.title)}
	case rowAsked:
		return [3]string{stopStyle.Render("? for you"), stopStyle.Render(r.id), fit(r.note)}
	}
	return [3]string{pickedStyle.Render("▶ working"), pickedStyle.Render(r.id), fit(r.title)}
}

// ticketsTable lists this run's tickets, newest last, in at most maxLines lines of screen.
func (m Dashboard) ticketsTable(w, maxLines int) string {
	if len(m.rows) == 0 {
		return ""
	}
	idWidth := 7
	for _, r := range m.rows {
		idWidth = max(idWidth, len(r.id))
	}
	aboutWidth := w - 2 - 13 - (idWidth + 2) - 2 - 2 // borders, state and ID columns, separators, padding
	rows := m.rows
	hidden := 0
	if limit := max(maxLines-4, 3); len(rows) > limit { // header, rule and borders take 4 lines
		hidden = len(rows) - (limit - 1)
		rows = rows[hidden:]
	}
	var data [][]string
	if hidden > 0 {
		more := ansi.Truncate("earlier tickets, see the log", max(aboutWidth, 8), "…")
		data = append(data, []string{"", dimStyle.Render(fmt.Sprintf("+%d", hidden)), dimStyle.Render(more)})
	}
	for _, r := range rows {
		c := r.cells(aboutWidth)
		if st, ok := m.active[r.id]; ok && r.state == rowWorking {
			if d := doingLabel(st); d == "resolving" {
				c[0] = deferredStyle.Render("⟳ resolving")
			} else if d == "testing" {
				c[0] = testingStyle.Render("▶ testing")
			} else if d == "editing" || d == "reading" {
				c[0] = pickedStyle.Render("▶ " + d)
			}
		}
		data = append(data, c[:])
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		Headers("Tickets", "", "").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return s.Faint(true)
			case col == 0:
				return s.Width(13)
			case col == 1:
				return s.Width(idWidth + 2)
			}
			return s
		}).
		Rows(data...).
		Width(w).
		Render()
}

// statsTable is the run's totals as one strip: a column per total, label over value, in the
// events' colours. Too narrow a pane gets the one-line form instead.
func (m Dashboard) statsTable(w int) string {
	count := func(n int, mark string, style lipgloss.Style) string {
		if n == 0 {
			return dimStyle.Render("0")
		}
		return style.Render(fmt.Sprintf("%s %d", mark, n))
	}
	deferred := count(m.deferred, "↷", deferredStyle)
	if m.triaged > 0 {
		deferred += organStyle.Render(fmt.Sprintf(" ◆ %d", m.triaged))
	}
	queued := dimStyle.Render("—")
	if m.queued >= 0 {
		queued = fmt.Sprint(m.queued)
	}
	labels := []string{"Completed", "Deferred", "Needs you", "Workers", "In queue"}
	values := []string{
		count(m.closed, "✓", closedStyle),
		deferred,
		count(m.asked, "?", stopStyle),
		pickedStyle.Render(fmt.Sprint(len(m.active))) + dimStyle.Render(fmt.Sprintf(" of %d", max(m.cfg.Concurrency, 1))),
		queued,
	}
	need := len(labels) + 1 // borders
	for i := range labels {
		need += max(ansi.StringWidth(labels[i]), ansi.StringWidth(values[i])) + 2
	}
	if w < need {
		return m.statsLine(w)
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		BorderHeader(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Faint(true)
			}
			return s
		}).
		Headers(labels...).
		Rows(values).
		Width(w).
		Render()
}

// titleLines is how many lines the active ticket's title may take before it is cut short.
const titleLines = 3

// wordWrap breaks s between words only (not at hyphens, as in worker-prompt.md), cutting a word
// longer than the width.
func wordWrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		for ansi.StringWidth(word) > width { // a word too long for any line
			if line != "" {
				lines, line = append(lines, line), ""
			}
			cut := ansi.Truncate(word, width, "")
			lines, word = append(lines, cut), word[len(cut):]
		}
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// wrapLines word-wraps s to width and keeps at most max lines, ending the last with … if cut.
func wrapLines(s string, width, max int) []string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || width < 1 {
		return nil
	}
	lines := wordWrap(s, width)
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if len(lines) > max {
		rest := strings.Join(lines[max-1:], " ")
		lines = append(lines[:max-1], ansi.Truncate(rest, width, "…"))
	}
	return lines
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

// titleLine is the Orchestra pill, the version, the branch, the ticket a scoped run works on and
// how long the run has gone, the solo ticket running alone or next, and whether the run is
// stopping.
func (m Dashboard) titleLine(w int) string {
	line := titleStyle.Render("Orchestra") + " " + dimStyle.Render(shortVersion(m.cfg.Version)) + "   " +
		dimStyle.Render(fmt.Sprintf("%s%s · %s", m.cfg.Base, dispatch.ScopeLabel(m.cfg.Ticket), time.Since(m.began).Truncate(time.Second)))
	switch {
	case m.solo.Next:
		line += deferredStyle.Render("  · solo " + m.solo.Ticket + " next")
	case m.solo.Ticket != "":
		line += pickedStyle.Render("  · solo " + m.solo.Ticket + " running")
	}
	switch {
	case m.stopping:
		line += stopStyle.Render("  · stopping")
	case m.draining:
		line += stopStyle.Render("  · stopping after current")
	}
	return ansi.Truncate(line, w, "…")
}

// doingLabel is the worker's status, made precise by what it reported doing when it is working.
func doingLabel(st dispatch.Status) string {
	if st.Resolving {
		return "resolving"
	}
	if st.Agent == "working" && st.Doing != "" {
		return st.Doing
	}
	return st.Agent
}

func agentStyle(s string) string {
	switch s {
	case "working", "editing", "reading":
		return pickedStyle.Render(s)
	case "testing":
		return testingStyle.Render(s)
	case "blocked":
		return stopStyle.Render(s + " — waiting for you")
	case "":
		return ""
	}
	return deferredStyle.Render(s)
}

// ---- Sinks ---------------------------------------------------------------------------

// ProgramSink sends the loop's events and statuses to the dashboard until Handoff passes them to
// another sink. A program that has exited drops what it is sent, so the sink keeps every event it
// sent: Handoff passes on those the dashboard never received.
type ProgramSink struct {
	p    *tea.Program
	mu   sync.Mutex
	sent []dispatch.Event
	next dispatch.Sink // set by Handoff
}

// NewProgramSink returns a sink for the dashboard program p.
func NewProgramSink(p *tea.Program) *ProgramSink { return &ProgramSink{p: p} }

func (s *ProgramSink) Event(ev dispatch.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next != nil {
		s.next.Event(ev)
		return
	}
	s.sent = append(s.sent, ev)
	s.p.Send(eventMsg(ev)) // returns once received, or once the program has exited
}

func (s *ProgramSink) Status(st dispatch.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next != nil {
		s.next.Status(st)
		return
	}
	s.p.Send(statusMsg(st))
}

// Handoff sends next the events after the first received (the final dashboard's Received) and,
// from then on, everything. Call it once the program has exited.
func (s *ProgramSink) Handoff(next dispatch.Sink, received int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.sent[min(received, len(s.sent)):] {
		next.Event(ev)
	}
	s.sent, s.next = nil, next
}

// Printer prints each event as a line to Out: styled for a terminal (after the live view has
// closed), or as the plain log line for pipes and -plain.
type Printer struct {
	Out    io.Writer
	Styled bool // colours and the rendered report, for a terminal
	Width  int
}

func (p Printer) Event(ev dispatch.Event) {
	if ev.Kind == dispatch.EvQueue {
		return // the dashboard's count, not a line
	}
	if p.Styled {
		fmt.Fprintln(p.Out, ansi.Wrap(renderEvent(ev), max(p.Width, 20), ""))
		return
	}
	fmt.Fprintf(p.Out, "%s %s\n", ev.Time.Format("2006-01-02 15:04:05"), ev.Text)
}
func (Printer) Status(dispatch.Status) {}

// Say prints a line of the orchestrator's own progress outside the event stream.
func (p Printer) Say(text string) {
	if p.Styled {
		fmt.Fprintln(p.Out, organStyle.Render("◆ ")+dimStyle.Render(text))
		return
	}
	fmt.Fprintf(p.Out, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), text)
}

// Report prints the reviewer's Markdown after a blank line, rendered with Glamour on a terminal.
func (p Printer) Report(md string) {
	fmt.Fprintln(p.Out)
	if p.Styled {
		r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(max(p.Width-4, 40)))
		if err == nil {
			if out, err := r.Render(md); err == nil {
				fmt.Fprint(p.Out, out)
				return
			}
		}
	}
	fmt.Fprintln(p.Out, md)
}

// Interrupted reports whether the run was stopped with Ctrl+C.
func (m Dashboard) Interrupted() bool { return m.interrupted }

// Final is the event the run ended with, or nil.
func (m Dashboard) Final() *dispatch.Event { return m.final }

// Received is the number of the loop's events the dashboard received.
func (m Dashboard) Received() int { return m.received }
