package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	dimStyle      = lipgloss.NewStyle().Faint(true)
	pickedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan: picked up
	closedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true) // green: completed
	deferredStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))            // yellow: set aside
	stopStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // red: needs you
	doneStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	home, _       = os.UserHomeDir()
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
	width       int
	quitting    bool
	interrupted bool
	final       *Event // the stop or done event, printed by main after exit
	cancel      func()
}

func newModel(cfg Config, cancel func()) model {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(pickedStyle))
	return model{cfg: cfg, spin: s, n: cfg.DoneSoFar, width: 80, cancel: cancel}
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
			m.n = ev.N
		case EvClosed:
			m.closed++
		case EvDeferred:
			m.deferred++
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
	w := max(m.width, 20)
	var b strings.Builder
	if m.st.Ticket == "" {
		b.WriteString(m.spin.View() + " " + dimStyle.Render("picking the next ticket…"))
	} else {
		elapsed := time.Since(m.st.Started).Truncate(time.Second)
		line := fmt.Sprintf("%s %s  %s  %s", m.spin.View(), pickedStyle.Render(m.st.Ticket),
			agentStyle(m.st.Agent), dimStyle.Render(fmt.Sprintf("%s · tab %s", elapsed, m.st.Tab)))
		b.WriteString(ansi.Truncate(line, w, "…"))
		if m.st.Activity != "" {
			b.WriteString("\n  " + dimStyle.Render(ansi.Truncate(m.st.Activity, w-2, "…")))
		}
	}
	footer := fmt.Sprintf("%d completed · %d deferred · %d/%d · %s · ctrl+c stops (the worker keeps running)",
		m.closed, m.deferred, m.n, m.cfg.Limit, m.cfg.Base)
	b.WriteString("\n" + dimStyle.Render(ansi.Truncate(footer, w, "…")))
	return b.String()
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

// plainSink prints the log lines as they are written, for pipes and non-interactive use.
type plainSink struct{}

func (plainSink) Event(ev Event) {
	fmt.Printf("%s %s\n", ev.Time.Format("2006-01-02 15:04:05"), ev.Text)
}
func (plainSink) Status(Status) {}
