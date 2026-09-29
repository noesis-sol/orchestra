package main

import (
	"fmt"
	"os"
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
	st          Status
	n           int
	closed      int
	deferred    int
	triaged     int
	width       int
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
		m.width = msg.Width
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
		m.st = Status(msg)
	case eventMsg:
		ev := Event(msg)
		switch ev.Kind {
		case EvDispatch:
			m.n, m.queued = ev.N, ev.Queued
		case EvClosed:
			m.closed++
		case EvDeferred:
			m.deferred++
		case EvTriage:
			m.triaged++
		case EvStop, EvDone:
			// main prints the last line after the program exits, since a Println queued just
			// before Quit can be dropped. The short delay lets earlier lines flush.
			m.final, m.quitting = &ev, true
			return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tea.Quit() })
		}
		// Wrap here: a line the terminal soft-wraps makes Bubble Tea miscount the lines it must
		// clear, leaving pieces of the status area behind.
		return m, tea.Println(ansi.Wrap(renderEvent(ev), max(m.width, 20), ""))
	case finishedMsg:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	w := max(m.width, 30)
	hint := dimStyle.Render(ansi.Truncate("  ctrl+c stops · the worker keeps running", w, "…"))
	return lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render("Orchestrator"), m.statsTable(w), m.workerPanel(w), hint)
}

// workerPanel boxes the current ticket: ID, worker status and time, title, latest action.
func (m model) workerPanel(w int) string {
	inner := w - 4 // rounded border and one space of padding on each side
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	border := lipgloss.TerminalColor(grey)
	var lines []string
	if m.st.Ticket == "" {
		lines = append(lines, fit(m.spin.View()+" "+dimStyle.Render("picking the next ticket…")))
	} else {
		border = cyan
		if m.st.Agent == "blocked" {
			border = red
		}
		elapsed := time.Since(m.st.Started).Truncate(time.Second)
		lines = append(lines, fit(fmt.Sprintf("%s %s  %s  %s", m.spin.View(), pickedStyle.Render(m.st.Ticket),
			agentStyle(m.st.Agent), dimStyle.Render(elapsed.String()))))
		if m.st.Title != "" {
			lines = append(lines, fit("  "+m.st.Title))
		}
		if m.st.Activity != "" {
			lines = append(lines, fit("  "+dimStyle.Render(m.st.Activity)))
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(w - 2).Render(strings.Join(lines, "\n"))
}

// statsTable lists the run's totals, one per row, with the counts in their event colours.
func (m model) statsTable(w int) string {
	count := func(n int, mark string, style lipgloss.Style) string {
		if n == 0 {
			return dimStyle.Render("0")
		}
		return style.Render(fmt.Sprintf("%s %d", mark, n))
	}
	queued, tab := dimStyle.Render("—"), dimStyle.Render("—")
	if m.queued >= 0 {
		queued = fmt.Sprintf("%d ready", m.queued)
	}
	if m.st.Tab != "" {
		tab = m.st.Tab
	}
	rows := [][]string{
		{"Completed", count(m.closed, "✓", closedStyle)},
		{"Deferred", count(m.deferred, "↷", deferredStyle) + triagedNote(m.triaged)},
		{"Picked up", pickedStyle.Render(fmt.Sprint(m.n)) + dimStyle.Render(fmt.Sprintf(" of %d max", m.cfg.Limit))},
		{"In queue", queued},
		{"Branch", m.cfg.Base},
		{"Worker tab", tab},
		{"Running", time.Since(m.began).Truncate(time.Second).String()},
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if col == 0 {
				return s.Faint(true).Width(12) // "Worker tab" plus padding; values get the rest
			}
			return s
		}).
		Rows(rows...).
		Width(w).
		Render()
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
