// Package tui is orchestra's terminal interface: the run's dashboard (Bubble Tea), plain output
// for pipes, and orchestra init's form and summary.
package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
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
	lilac  = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#C4B5FD"} // a lighter purple, for text
)

var (
	dimStyle      = lipgloss.NewStyle().Faint(true)
	keyStyle      = lipgloss.NewStyle().Bold(true)                   // a key to press
	pickedStyle   = lipgloss.NewStyle().Foreground(cyan).Bold(true)  // picked up
	closedStyle   = lipgloss.NewStyle().Foreground(green).Bold(true) // completed
	deferredStyle = lipgloss.NewStyle().Foreground(yellow)           // set aside
	stopStyle     = lipgloss.NewStyle().Foreground(red).Bold(true)   // needs you
	doneStyle     = lipgloss.NewStyle().Foreground(green)
	organStyle    = lipgloss.NewStyle().Foreground(purple).Bold(true) // an organ's output
	sayStyle      = lipgloss.NewStyle().Foreground(lilac)             // the organ phase's progress
	testingStyle  = lipgloss.NewStyle().Foreground(yellow).Bold(true) // a worker running checks
	// The Charm purple pill from the Bubble Tea and Lip Gloss examples.
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).Padding(0, 1).MarginTop(1)
	home, _ = os.UserHomeDir() // none known: paths are shown in full
)

// Tildify shortens paths under the home directory for display; the log keeps full paths.
func Tildify(s string) string {
	if home == "" {
		return s
	}
	return strings.ReplaceAll(s, home+"/", "~/")
}

// ---- Bubble Tea model ----------------------------------------------------------------

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
			// Only a ticket the warning sets aside is for review. The rest are about a ticket still
			// running (LONG_RUNNING) or already deferred (TRIAGE_FAILED), whose row stays as it is.
			if ev.Ticket != "" && ev.Aside {
				m.setRow(ev.Ticket, rowReview, "left for review, see the log")
			}
		case dispatch.EvAsked:
			m.asked++
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

// View is the whole display: title, totals, the tickets of this run, and the current tickets. When
// the program quits it renders once more without the active ticket, which stays on screen as the
// run's summary.
func (m Dashboard) View() string {
	w := max(m.width, 30)
	title := m.titleLine(w)
	if m.quitting {
		return m.summary(w, title)
	}
	if m.height == 0 {
		return "" // not sized yet: a frame drawn for a guessed size can outgrow the pane and leave scraps
	}
	hint := m.hintLine(w)
	if m.draining {
		hint = lipgloss.JoinVertical(lipgloss.Left, m.windDownLine(w), hint)
	}
	if warn := m.cfg.MCPWarning(); warn != "" { // in the log once; here for the whole run
		hint = lipgloss.JoinVertical(lipgloss.Left, deferredStyle.Render(ansi.Truncate(" ! "+warn, w, "…")), hint)
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
	return m.layout(w, title, lipgloss.JoinVertical(lipgloss.Left, m.promptLine(w), hint))
}

// summary is the last frame, which stays on screen as the run's summary: the title, the totals and the
// tickets, ending with a newline for the line main prints below it. Bubble Tea drops the top lines of a
// frame taller than the pane, never writing them, so in a pane of known height the tickets table shows
// only its latest rows, or none, the totals take one line where their strip doesn't fit, and anything
// still too tall is cut at the bottom: the title and the totals are what the summary is read for.
func (m Dashboard) summary(w int, title string) string {
	if m.height == 0 { // no pane to fit
		return lipgloss.JoinVertical(lipgloss.Left, title, m.statsTable(w), m.ticketsTable(w, 1000)) + "\n"
	}
	keep := max(m.height-1, 1) // the frame's last line is the cursor's
	stats := m.statsTable(w)
	if lipgloss.Height(title)+lipgloss.Height(stats) > keep {
		stats = m.statsLine(w)
	}
	parts := []string{title, stats}
	room := keep - lipgloss.Height(title) - lipgloss.Height(stats)
	if tickets := m.ticketsTable(w, room); lipgloss.Height(tickets) <= room {
		parts = append(parts, tickets)
	}
	lines := strings.Split(lipgloss.JoinVertical(lipgloss.Left, parts...), "\n")
	return strings.Join(lines[:min(len(lines), keep)], "\n") + "\n"
}

// layout fits the title, totals, tickets and workers above footer into the pane: Bubble Tea
// can't redraw a view taller than the terminal. It gives way step by step: one line per worker
// instead of a box each, then no tickets table, then the totals on one line, then the Current
// label, then cut, keeping footer.
func (m Dashboard) layout(w int, title, footer string) string {
	stats := m.statsTable(w)
	fits := func(v string) bool { return lipgloss.Height(v) <= m.height }
	compose := func(stats, panels string) string {
		// Show as many recent tickets as fit around the rest; none if that's fewer than three.
		room := m.height - lipgloss.Height(title) - 1 - lipgloss.Height(stats) - lipgloss.Height(panels) -
			lipgloss.Height(footer)
		parts := []string{title, stats}
		if room >= 7 {
			parts = append(parts, m.ticketsTable(w, room))
		}
		if panels != "" {
			parts = append(parts, panels)
		}
		return lipgloss.JoinVertical(lipgloss.Left, append(parts, footer)...)
	}
	if v := compose(stats, current(m.workerPanels(w))); fits(v) {
		return v
	}
	if v := compose(stats, current(m.workerList(w))); fits(v) {
		return v
	}
	top := []string{title, m.statsLine(w)}
	if list := m.workerList(w); list != "" {
		if v := lipgloss.JoinVertical(lipgloss.Left, append(top, current(list), footer)...); fits(v) {
			return v
		}
		top = append(top, list)
	}
	body := strings.Split(lipgloss.JoinVertical(lipgloss.Left, top...), "\n")
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

// current heads the worker boxes with a Current label, indented like the tickets table's Tickets
// header but bold in the working colour, where the eye should land; nothing when there are no boxes.
func current(panels string) string {
	if panels == "" {
		return ""
	}
	return lipgloss.JoinVertical(lipgloss.Left, "  "+pickedStyle.Render("Current"), panels)
}

// hintLine says which keys do what: the workers' numbers go to their tabs (not while the stop
// question is open), s stops after the running tickets, or once asked cancels that; ctrl+c stops at
// once. A narrow pane gets a shorter form, and then one without the numbers.
func (m Dashboard) hintLine(w int) string {
	long, short := "s stops after current · ctrl+c stops now", "s: stop after · ctrl+c: now"
	switch {
	case m.draining:
		long, short = "s cancels the stop · ctrl+c stops now", "s: cancel stop · ctrl+c: now"
	case m.stopping:
		long, short = "ctrl+c stops now", "ctrl+c: now"
	}
	hints := []string{"  " + long, " " + short}
	if n := min(len(m.active), numbered); n > 0 && !m.asking {
		keys, goes, worker := "1", "goes to the worker", "worker"
		if n > 1 {
			keys, goes = fmt.Sprintf("1–%d", n), "go to a worker"
		}
		hints = append([]string{"  " + keys + " " + goes + " · " + long, " " + keys + ": " + worker + " · " + short},
			hints...)
	}
	hint := hints[len(hints)-1]
	for _, h := range hints {
		if ansi.StringWidth(h) <= w {
			hint = h
			break
		}
	}
	return dimStyle.Render(ansi.Truncate(hint, w, "…"))
}

// windDownLine says, while the run winds down, after which tickets it ends, in the DRAIN line's
// words and as tickets finish. A narrow pane wraps it rather than lose words: only the ticket IDs
// are shortened.
func (m Dashboard) windDownLine(w int) string {
	lead, list, tail := dispatch.DrainWords(m.runningIDs())
	lines := wrapAround(" ■ "+capitalize(lead), list, tail, w, "   ")
	for i := range lines {
		lines[i] = stopStyle.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

// wrapAround word-wraps lead + list + tail to width, continuation lines starting with indent,
// and shortens list, kept on one line with the punctuation touching it, where it doesn't fit.
func wrapAround(lead, list, tail string, width int, indent string) []string {
	var lines []string
	line, fresh := "", true // fresh: nothing on the line but its indent
	add := func(word string) {
		switch {
		case fresh:
			line += word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), indent+word
		}
		fresh = false
	}
	if strings.HasPrefix(lead, " ") { // keep a leading margin, which Fields drops
		line = lead[:len(lead)-len(strings.TrimLeft(lead, " "))]
	}
	before, after := strings.Fields(lead), strings.Fields(tail)
	left, right := "", ""
	if list != "" && len(before) > 0 && !strings.HasSuffix(lead, " ") {
		left, before = before[len(before)-1], before[:len(before)-1]
	}
	if list != "" && len(after) > 0 && !strings.HasPrefix(tail, " ") {
		right, after = after[0], after[1:]
	}
	for _, word := range before {
		add(word)
	}
	if list != "" {
		unit := left + list + right
		room := width - ansi.StringWidth(line) - 1
		if fresh {
			room++
		}
		const least = 6 // fewer columns of IDs say nothing
		if ansi.StringWidth(unit) > room && room-ansi.StringWidth(left+right) < min(least, ansi.StringWidth(list)) {
			lines, line, fresh = append(lines, line), indent, true
			room = width - ansi.StringWidth(indent)
		}
		if ansi.StringWidth(unit) > room {
			unit = left + ansi.Truncate(list, max(room-ansi.StringWidth(left+right), 1), "…") + right
		}
		add(unit)
	}
	for _, word := range after {
		add(word)
	}
	lines = append(lines, line)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…") // a word wider than the pane
	}
	return lines
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// runningIDs names the running tickets, oldest first.
func (m Dashboard) runningIDs() []string {
	var ids []string
	for _, st := range m.Running() {
		ids = append(ids, st.Ticket)
	}
	return ids
}

// drainQuestion is the question s asks, worded once for the box and the one-line prompt: whether
// to stop after the running tickets, or, while the run winds down, whether to take tickets again.
type drainQuestion struct {
	ask   string // the question
	short string // the question where the prompt line has no room for ask
	about string // what y does, in the DRAIN line's words
	keys  string // what y and n do, for the box
}

func (m Dashboard) drainQuestion() drainQuestion {
	if m.draining {
		return drainQuestion{ask: "Keep taking tickets?", short: "Keep taking tickets?",
			about: "New tickets start again as slots free up.", keys: "y keep going · n keep stopping"}
	}
	q := drainQuestion{ask: "Stop after the running tickets?", short: "Stop after current?",
		keys: "y stop after current · n keep going"}
	lead, list, tail := dispatch.DrainWords(m.runningIDs())
	switch len(m.active) {
	case 0:
		q.about, q.keys = "No new tickets will start. Nothing is running, so the run ends now.", "y stop · n keep going"
	case 1:
		q.about = capitalize(lead+list+tail) + ". It merges as usual, then the run ends."
	default:
		q.about = capitalize(lead+list+tail) + ". They merge as usual, then the run ends."
	}
	return q
}

// modal is the drain question in a box.
func (m Dashboard) modal(w int) string {
	q := m.drainQuestion()
	body := lipgloss.JoinVertical(lipgloss.Left,
		deferredStyle.Bold(true).Render(q.ask), "", q.about, "", dimStyle.Render(q.keys))
	return box(min(w-4, 64), yellow, body)
}

// promptLine is the drain question on one line, for a pane too small for the box; the question and
// keys come first so a narrow pane keeps them.
func (m Dashboard) promptLine(w int) string {
	q := m.drainQuestion()
	ask := q.ask
	if ansi.StringWidth(" "+ask+" y/n") > w {
		ask = q.short
	}
	return deferredStyle.Bold(true).Render(ansi.Truncate(" "+ask+" y/n · "+q.about, w, "…"))
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
		lines[top+i] = head + "\x1b[0m" + l + "\x1b[0m" + rightOf(b, left+fw)
	}
	return strings.Join(lines, "\n")
}

// rightOf is what shows of line b right of column end. A wide character the column cuts in half
// becomes a space, so the line keeps b's width.
func rightOf(b string, end int) string {
	want := max(ansi.StringWidth(b)-end, 0)
	tail := ansi.TruncateLeft(b, end, "")
	for cut := end + 1; ansi.StringWidth(tail) > want; cut++ {
		tail = ansi.TruncateLeft(b, cut, "")
	}
	return strings.Repeat(" ", want-ansi.StringWidth(tail)) + tail
}

// workerList is the compact form of the worker boxes: one box, one line per running worker.
func (m Dashboard) workerList(w int) string {
	running := m.Running()
	if len(running) == 0 {
		return m.workerPanels(w)
	}
	var lines []string
	for i, st := range running {
		lines = append(lines, ansi.Truncate(m.workerHead(i+1, st)+"  "+oneLine(st.Title), w-4, "…"))
	}
	return box(w, workerBorder(running...), strings.Join(lines, "\n"))
}

// numbered is how many running workers get a number, the key that goes to their tab.
const numbered = 9

// workerHead is the nth running worker's first line: its number, ID, worker status and time. A
// worker beyond the ninth gets no number, only the room for one.
func (m Dashboard) workerHead(n int, st dispatch.Status) string {
	num := "  "
	if n <= numbered {
		num = keyStyle.Render(fmt.Sprint(n)) + " "
	}
	elapsed := time.Since(st.Started).Truncate(time.Second)
	return fmt.Sprintf("%s%s %s  %s  %s", num, m.spin.View(), pickedStyle.Render(st.Ticket),
		agentStyle(doingLabel(st)), dimStyle.Render(elapsed.String()))
}

// goTo switches Herdr to the tab of the worker numbered n in Current, as the list stands now:
// nothing if there is none or it has no tab yet. The callback runs Herdr in the background.
func (m Dashboard) goTo(n int) {
	if running := m.Running(); n <= min(len(running), numbered) && running[n-1].Tab != "" {
		m.focus(running[n-1].Tab)
	}
}

// workerBorder colours a box of running workers red if any waits for the maintainer.
func workerBorder(running ...dispatch.Status) lipgloss.TerminalColor {
	for _, st := range running {
		if st.Agent == dispatch.StateBlocked {
			return red
		}
	}
	return cyan
}

// Running returns the running workers, oldest first; those started at the same time by ticket, so
// their numbers stay put.
func (m Dashboard) Running() []dispatch.Status {
	var l []dispatch.Status
	for _, st := range m.active {
		l = append(l, st)
	}
	sort.Slice(l, func(i, j int) bool {
		if !l[i].Started.Equal(l[j].Started) {
			return l[i].Started.Before(l[j].Started)
		}
		return l[i].Ticket < l[j].Ticket
	})
	return l
}

// workerPanels stacks a box per running worker, or one box saying what the loop is doing; nothing
// when the run winds down with nothing running.
func (m Dashboard) workerPanels(w int) string {
	running := m.Running()
	if len(running) == 0 {
		if m.draining {
			return "" // the line above the hint says the run ends
		}
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
	for i, st := range running {
		boxes = append(boxes, m.workerPanel(w, i+1, st, lines))
	}
	return lipgloss.JoinVertical(lipgloss.Left, boxes...)
}

// workerPanel boxes the nth running ticket: its number, ID, worker status and time, title, latest
// action.
func (m Dashboard) workerPanel(w, n int, st dispatch.Status, titleMax int) string {
	inner := w - 4 // rounded border and one space of padding on each side
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	lines := []string{fit(m.workerHead(n, st))}
	for _, l := range wrapLines(st.Title, inner-2, titleMax) {
		lines = append(lines, "  "+l)
	}
	if st.Activity != "" {
		lines = append(lines, fit("  "+dimStyle.Render(oneLine(st.Activity))))
	}
	return box(w, workerBorder(st), strings.Join(lines, "\n"))
}

func box(w int, border lipgloss.TerminalColor, content string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(w - 2).Render(content)
}

// ---- Tickets table -------------------------------------------------------------------

// rowState starts at working on purpose: a row is added when its ticket is picked up, so an unset
// state means the worker is still at it.
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

// setRow sets ticket id's row to state, with note. A row that leaves rowAsked (its worker deferred
// the ticket in its tab, say) no longer needs the maintainer.
func (m *Dashboard) setRow(id string, state rowState, note string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id})
		i = len(m.rows) - 1
	}
	if m.rows[i].state == rowAsked && state != rowAsked {
		m.asked--
	}
	m.rows[i].state, m.rows[i].note = state, note
}

// working shows ticket id as picked up. A ticket back from a question, or deferred earlier in the
// run, keeps its row, which no longer needs the maintainer and drops the old verdict; title ""
// keeps the row's.
func (m *Dashboard) working(id, title string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id, title: title, state: rowWorking})
		return
	}
	if m.rows[i].state == rowAsked {
		m.asked--
	}
	m.rows[i].state, m.rows[i].note, m.rows[i].triage = rowWorking, "", ""
	if title != "" {
		m.rows[i].title = title
	}
}

// cells renders one row: state, ticket ID, and what to say about it. A picked-up ticket shows
// its title, and in its state what its worker is doing (doingLabel, or ""); a completed one only
// its merged commit; a set-aside one why, with triage's verdict.
func (r ticketRow) cells(width int, doing string) [3]string {
	fit := func(s string) string { return ansi.Truncate(oneLine(s), max(width, 8), "…") }
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
	state := pickedStyle.Render("▶ working")
	switch doing {
	case "resolving":
		state = deferredStyle.Render("⟳ resolving")
	case "testing":
		state = testingStyle.Render("▶ testing")
	case "editing", "reading":
		state = pickedStyle.Render("▶ " + doing)
	}
	return [3]string{state, pickedStyle.Render(r.id), fit(r.title)}
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
		doing := ""
		if st, ok := m.active[r.id]; ok {
			doing = doingLabel(st)
		}
		c := r.cells(aboutWidth, doing)
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
	if m.draining {
		queued += deferredStyle.Render(" · held") // not taken while the run winds down
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
		StyleFunc(func(row, _ int) lipgloss.Style {
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

// statsLine is the totals on one line, for a pane too narrow or too short for the strip. The
// branch, running time and stopping state are on the title line, the winding down above the hint.
func (m Dashboard) statsLine(w int) string {
	queued := "—"
	if m.queued >= 0 {
		queued = fmt.Sprint(m.queued)
	}
	if m.draining {
		queued += " held" // not taken while the run winds down
	}
	parts := []string{
		closedStyle.Render(fmt.Sprintf("✓ %d", m.closed)),
		deferredStyle.Render(fmt.Sprintf("↷ %d", m.deferred)),
		stopStyle.Render(fmt.Sprintf("? %d", m.asked)),
		pickedStyle.Render(fmt.Sprintf("▶ %d", len(m.active))) +
			dimStyle.Render(fmt.Sprintf("/%d", max(m.cfg.Concurrency, 1))),
		dimStyle.Render("queue " + queued),
	}
	return ansi.Truncate(" "+strings.Join(parts, dimStyle.Render(" · ")), w, "…")
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

// oneLine collapses the whitespace in s, line breaks included, to single spaces: text from a
// ticket, a worker or an organ that the dashboard shows on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// wrapLines word-wraps s to width and keeps at most limit lines, ending the last with … if cut.
func wrapLines(s string, width, limit int) []string {
	s = oneLine(s)
	if s == "" || width < 1 {
		return nil
	}
	lines := wordWrap(s, width)
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if len(lines) > limit {
		rest := strings.Join(lines[limit-1:], " ")
		lines = append(lines[:limit-1], ansi.Truncate(rest, width, "…"))
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

// doingLabel is the worker's status, made precise by what it reported doing when it is working.
func doingLabel(st dispatch.Status) string {
	switch {
	case st.Resolving:
		return "resolving"
	case st.Unreadable:
		return "unreadable"
	case st.Agent == dispatch.StateWorking && st.Doing != "":
		return st.Doing
	}
	return string(st.Agent)
}

func agentStyle(s string) string {
	switch s {
	case string(dispatch.StateWorking), "editing", "reading":
		return pickedStyle.Render(s)
	case "testing":
		return testingStyle.Render(s)
	case string(dispatch.StateBlocked):
		return stopStyle.Render(s + " — waiting for you")
	case "":
		return ""
	}
	return deferredStyle.Render(s)
}

// Interrupted reports whether the run was stopped with Ctrl+C.
func (m Dashboard) Interrupted() bool { return m.interrupted }

// Final is the event the run ended with, or nil.
func (m Dashboard) Final() *dispatch.Event { return m.final }

// Received is the number of the loop's events the dashboard received.
func (m Dashboard) Received() int { return m.received }
