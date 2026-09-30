package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
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
	// The Charm purple pill from the Bubble Tea and Lip Gloss examples.
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).Padding(0, 1).MarginTop(1)
	home, _ = os.UserHomeDir()
)

// tildify shortens paths under the home directory for display; the log keeps full paths.
func tildify(s string) string {
	if home == "" {
		return s
	}
	return strings.ReplaceAll(s, home+"/", "~/")
}

// renderEvent formats one event as a permanent line above the live status area.
func renderEvent(ev Event) string {
	ts := dimStyle.Render(ev.Time.Format("15:04:05"))
	switch ev.Kind {
	case EvDispatch:
		return fmt.Sprintf("%s %s %s  %s", ts, dimStyle.Render(fmt.Sprintf("▶ [%d/%d]", ev.N, ev.Limit)),
			pickedStyle.Render(ev.Ticket), ev.Title)
	case EvClosed:
		return fmt.Sprintf("%s %s  %s", ts, closedStyle.Render("✓ "+ev.Ticket+" completed"), dimStyle.Render(ev.Detail))
	case EvDeferred:
		return fmt.Sprintf("%s %s  %s", ts, deferredStyle.Render("↷ "+ev.Ticket+" deferred"), dimStyle.Render(ev.Detail))
	case EvTriage:
		return fmt.Sprintf("%s %s  %s", ts, organStyle.Render("◆ "+ev.Ticket+" triage: "+ev.Detail), dimStyle.Render(ev.Title))
	case EvAsked:
		return fmt.Sprintf("%s %s  %s", ts, stopStyle.Render("? "+ev.Ticket+" needs your answer"), dimStyle.Render(ev.Detail))
	case EvHold:
		return fmt.Sprintf("%s %s", ts, stopStyle.Render("■ "+tildify(ev.Text)))
	case EvWarn:
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("! "+tildify(strings.TrimSpace(ev.Text))))
	case EvStop:
		return fmt.Sprintf("%s %s", ts, stopStyle.Render("■ "+tildify(ev.Text)))
	case EvDone:
		return fmt.Sprintf("%s %s", ts, doneStyle.Render("■ "+ev.Text))
	}
	return fmt.Sprintf("%s %s", ts, dimStyle.Render(tildify(ev.Text)))
}

// ---- Bubble Tea model ----------------------------------------------------------------

type eventMsg Event
type statusMsg Status
type finishedMsg struct{}

type model struct {
	cfg         Config
	spin        spinner.Model
	active      map[string]Status // running workers, by ticket
	stopping    bool              // a ticket stopped the run; the running ones are finishing
	n           int
	closed      int
	deferred    int
	triaged     int
	asked       int
	width       int
	height      int
	rows        []ticketRow // every ticket picked up in this run, oldest first
	quitting    bool
	interrupted bool
	final       *Event // the stop or done event, printed by main after exit
	queued      int    // ready tickets behind the current one; -1 until the first pickup
	began       time.Time
	cancel      func()
}

func newModel(cfg Config, cancel func()) model {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(pickedStyle))
	return model{cfg: cfg, spin: s, n: cfg.DoneSoFar, width: 80, queued: -1, began: time.Now(), cancel: cancel}
}

func (m model) Init() tea.Cmd { return m.spin.Tick }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.interrupted, m.quitting = true, true
			m.cancel()
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case statusMsg:
		st := Status(msg)
		if m.active == nil {
			m.active = map[string]Status{}
		}
		if st.Gone {
			delete(m.active, st.Ticket)
		} else {
			m.active[st.Ticket] = st
		}
	case eventMsg:
		// Events update the dashboard in place; nothing is printed above it. The full lines are
		// in the log file.
		ev := Event(msg)
		switch ev.Kind {
		case EvDispatch:
			m.n, m.queued = ev.N, ev.Queued
			m.rows = append(m.rows, ticketRow{id: ev.Ticket, title: ev.Title, state: rowWorking})
		case EvClosed:
			m.closed++
			m.setRow(ev.Ticket, rowDone, ev.Detail)
		case EvDeferred:
			m.deferred++
			m.setRow(ev.Ticket, rowDeferred, ev.Detail)
		case EvWarn:
			if ev.Ticket != "" {
				m.setRow(ev.Ticket, rowReview, "left for review, see the log")
			}
		case EvAsked:
			m.asked++
			m.setRow(ev.Ticket, rowAsked, "answer "+ev.Detail)
		case EvTriage:
			m.triaged++
			if i := m.rowIndex(ev.Ticket); i >= 0 {
				m.rows[i].triage = ev.Detail + " · " + ev.Title
			}
		case EvHold:
			m.stopping = true
			m.setRow(ev.Ticket, rowStopped, "")
		case EvStop, EvDone:
			if ev.Kind == EvStop {
				for i := range m.rows {
					if m.rows[i].state == rowWorking {
						m.rows[i].state = rowStopped
					}
				}
			}
			// main prints the last line after the program exits, below the final dashboard.
			m.final, m.quitting = &ev, true
			return m, tea.Quit
		}
	case finishedMsg:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// View is the whole display: title, totals, the tickets of this run, and the active ticket. When
// the program quits it renders once more without the active ticket, which stays on screen as the
// run's summary.
func (m model) View() string {
	w := max(m.width, 30)
	title := titleStyle.Render("Orchestra") + " " + dimStyle.Render(buildVersion())
	if m.quitting {
		return lipgloss.JoinVertical(lipgloss.Left, title, m.statsTable(w), m.ticketsTable(w, 1000)) + "\n"
	}
	if m.height == 0 {
		return "" // not sized yet: a frame drawn for a guessed size can outgrow the pane and leave scraps
	}
	hint := "  ctrl+c stops · the worker keeps running"
	if m.cfg.Concurrency > 1 {
		hint = "  ctrl+c stops · the workers keep running"
	}
	hint = dimStyle.Render(ansi.Truncate(hint, w, "…"))
	stats := m.statsTable(w)

	// The view must fit the pane: Bubble Tea can't redraw one taller than the terminal. Give
	// way step by step: one line per worker instead of a box each, then no tickets table, then
	// the totals on one line, then cut.
	fits := func(v string) bool { return lipgloss.Height(v) <= m.height }
	compose := func(stats, panels string) string {
		// Show as many recent tickets as fit around the rest; none if that's fewer than three.
		room := m.height - lipgloss.Height(title) - 1 - lipgloss.Height(stats) - lipgloss.Height(panels) - 1
		parts := []string{title, stats}
		if room >= 7 {
			parts = append(parts, m.ticketsTable(w, room))
		}
		return lipgloss.JoinVertical(lipgloss.Left, append(parts, panels, hint)...)
	}
	if v := compose(stats, m.workerPanels(w)); fits(v) {
		return v
	}
	if v := compose(stats, m.workerList(w)); fits(v) {
		return v
	}
	v := lipgloss.JoinVertical(lipgloss.Left, title, m.statsLine(w), m.workerList(w), hint)
	if lines := strings.Split(v, "\n"); len(lines) > m.height && m.height > 0 {
		v = strings.Join(lines[:m.height], "\n")
	}
	return v
}

// workerList is the compact form of the worker boxes: one box, one line per running worker.
func (m model) workerList(w int) string {
	running := m.activeList()
	if len(running) == 0 {
		return m.workerPanels(w)
	}
	inner := w - 4
	var lines []string
	for _, st := range running {
		elapsed := time.Since(st.Started).Truncate(time.Second)
		lines = append(lines, ansi.Truncate(fmt.Sprintf("%s %s  %s  %s  %s", m.spin.View(), pickedStyle.Render(st.Ticket),
			agentStyle(st.Agent), dimStyle.Render(elapsed.String()), st.Title), inner, "…"))
	}
	border := lipgloss.TerminalColor(cyan)
	for _, st := range running {
		if st.Agent == "blocked" {
			border = red
		}
	}
	return box(w, border, strings.Join(lines, "\n"))
}

// statsLine is the totals on one line, for a pane too short for the table.
func (m model) statsLine(w int) string {
	parts := []string{
		closedStyle.Render(fmt.Sprintf("✓ %d", m.closed)),
		deferredStyle.Render(fmt.Sprintf("↷ %d", m.deferred)),
		stopStyle.Render(fmt.Sprintf("? %d", m.asked)),
		m.workersCell(),
		dimStyle.Render(fmt.Sprintf("%d of %d · %s · %s", m.n, m.cfg.Limit, m.cfg.Base, time.Since(m.began).Truncate(time.Second))),
	}
	return ansi.Truncate(" "+strings.Join(parts, dimStyle.Render("  ·  ")), w, "…")
}

// activeList returns the running workers, oldest first.
func (m model) activeList() []Status {
	var l []Status
	for _, st := range m.active {
		l = append(l, st)
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Started.Before(l[j].Started) })
	return l
}

// workerPanels stacks a box per running worker, or one box saying what the loop is doing.
func (m model) workerPanels(w int) string {
	running := m.activeList()
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
func (m model) workerPanel(w int, st Status, titleMax int) string {
	inner := w - 4 // rounded border and one space of padding on each side
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	border := lipgloss.TerminalColor(cyan)
	if st.Agent == "blocked" {
		border = red
	}
	elapsed := time.Since(st.Started).Truncate(time.Second)
	lines := []string{fit(fmt.Sprintf("%s %s  %s  %s", m.spin.View(), pickedStyle.Render(st.Ticket),
		agentStyle(st.Agent), dimStyle.Render(elapsed.String())))}
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

func (m *model) rowIndex(id string) int {
	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].id == id {
			return i
		}
	}
	return -1
}

func (m *model) setRow(id string, state rowState, note string) {
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
func (m model) ticketsTable(w, maxLines int) string {
	if len(m.rows) == 0 {
		return ""
	}
	idWidth := 7
	for _, r := range m.rows {
		idWidth = max(idWidth, len(r.id))
	}
	aboutWidth := w - 2 - 12 - (idWidth + 2) - 2 - 2 // borders, state and ID columns, separators, padding
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
				return s.Width(12)
			case col == 1:
				return s.Width(idWidth + 2)
			}
			return s
		}).
		Rows(data...).
		Width(w).
		Render()
}

// statsTable lists the run's totals, one per row, with the counts in their event colours.
func (m model) statsTable(w int) string {
	count := func(n int, mark string, style lipgloss.Style) string {
		if n == 0 {
			return dimStyle.Render("0")
		}
		return style.Render(fmt.Sprintf("%s %d", mark, n))
	}
	queued := dimStyle.Render("—")
	if m.queued >= 0 {
		queued = fmt.Sprintf("%d ready", m.queued)
	}
	rows := [][]string{
		{"Completed", count(m.closed, "✓", closedStyle)},
		{"Deferred", count(m.deferred, "↷", deferredStyle) + triagedNote(m.triaged)},
		{"Needs you", count(m.asked, "?", stopStyle)},
		{"Workers", m.workersCell()},
		{"Picked up", pickedStyle.Render(fmt.Sprint(m.n)) + dimStyle.Render(fmt.Sprintf(" of %d max", m.cfg.Limit))},
		{"In queue", queued},
		{"Branch", m.cfg.Base},
		{"Running", time.Since(m.began).Truncate(time.Second).String()},
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if col == 0 {
				return s.Faint(true).Width(12) // lines up with the Tickets table's first column
			}
			return s
		}).
		Rows(rows...).
		Width(w).
		Render()
}

// titleLines is how many lines the active ticket's title may take before it is cut short.
const titleLines = 3

// wrapLines word-wraps s to width and keeps at most max lines, ending the last with … if cut.
func wrapLines(s string, width, max int) []string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || width < 1 {
		return nil
	}
	lines := strings.Split(ansi.Wrap(s, width, ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if len(lines) > max {
		rest := strings.Join(lines[max-1:], " ")
		lines = append(lines[:max-1], ansi.Truncate(rest, width, "…"))
	}
	return lines
}

// workersCell says how many workers run out of how many may, and whether the run is stopping.
func (m model) workersCell() string {
	cell := pickedStyle.Render(fmt.Sprint(len(m.active))) + dimStyle.Render(fmt.Sprintf(" running of %d", max(m.cfg.Concurrency, 1)))
	if m.stopping {
		cell += stopStyle.Render(" · stopping")
	}
	return cell
}

func triagedNote(n int) string {
	if n == 0 {
		return ""
	}
	return organStyle.Render(fmt.Sprintf(" · ◆ %d triaged", n))
}

func agentStyle(s string) string {
	switch s {
	case "working":
		return pickedStyle.Render(s)
	case "blocked":
		return stopStyle.Render(s + " — waiting for you")
	case "":
		return ""
	}
	return deferredStyle.Render(s)
}

// ---- Sinks ---------------------------------------------------------------------------

type teaSink struct{ p *tea.Program }

func (s teaSink) Event(ev Event)   { s.p.Send(eventMsg(ev)) }
func (s teaSink) Status(st Status) { s.p.Send(statusMsg(st)) }

// printSink prints each event as a line: styled for a terminal (after the live view has closed),
// or as the plain log line for pipes and -plain.
type printSink struct {
	styled bool
	width  int
}

func (p printSink) Event(ev Event) {
	if p.styled {
		fmt.Println(ansi.Wrap(renderEvent(ev), max(p.width, 20), ""))
		return
	}
	fmt.Printf("%s %s\n", ev.Time.Format("2006-01-02 15:04:05"), ev.Text)
}
func (printSink) Status(Status) {}

// say prints a line of the orchestrator's own progress outside the event stream.
func (p printSink) say(text string) {
	if p.styled {
		fmt.Println(organStyle.Render("◆ ") + dimStyle.Render(text))
		return
	}
	fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05"), text)
}

// report prints the reviewer's Markdown, rendered with Glamour on a terminal.
func (p printSink) report(md string) {
	if p.styled {
		r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(max(p.width-4, 40)))
		if err == nil {
			if out, err := r.Render(md); err == nil {
				fmt.Print(out)
				return
			}
		}
	}
	fmt.Println(md)
}
