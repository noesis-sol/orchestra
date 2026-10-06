package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The loop's sinks: the dashboard program, and lines printed once it has closed or for pipes.

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

// Event sends ev to the dashboard, or after Handoff to the next sink.
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

// Status sends st to the dashboard, or after Handoff to the next sink.
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
	live   *liveLine // the busy line, on a Terminal printer
}

// Event prints ev as a line; the queue count, which only the dashboard shows, is skipped.
func (p Printer) Event(ev dispatch.Event) {
	if ev.Kind == dispatch.EvQueue {
		return // the dashboard's count, not a line
	}
	ev = printableEvent(ev)
	if p.Styled {
		p.wrapped(renderEvent(ev))
		return
	}
	p.print(fmt.Sprintf("%s %s\n", ev.Time.Format("2006-01-02 15:04:05"), ev.Text))
}

// printableEvent is ev with its text, which can quote a ticket's title or an organ's words, made
// printable.
func printableEvent(ev dispatch.Event) dispatch.Event {
	ev.Ticket, ev.Title, ev.Detail, ev.Text = Printable(ev.Ticket), Printable(ev.Title), Printable(ev.Detail),
		Printable(ev.Text)
	return ev
}

// Summary prints the run's summary from the final dashboard d, which the alternate screen took with
// it as the dashboard closed: its title, totals and tickets, as wide as the terminal and as long as
// they are. A dashboard that never started (the zero Dashboard) has none.
func (p Printer) Summary(d Dashboard) {
	if d.began.IsZero() {
		return
	}
	p.print(d.summary(max(p.Width, 30)) + "\n")
}

// End prints the event the run ended with, if any, below the run's summary from the final dashboard
// d: as Event does, but on a terminal a run that ended by itself also says how long it took.
func (p Printer) End(d Dashboard) {
	switch ev := d.Final(); {
	case ev == nil:
	case p.Styled && ev.Kind == dispatch.EvDone:
		var took time.Duration
		if !d.began.IsZero() && ev.Time.After(d.began) {
			took = ev.Time.Sub(d.began)
		}
		p.wrapped(closing(printableEvent(*ev), took))
	default:
		p.Event(*ev)
	}
}

// wrapped prints styled lines wrapped to the terminal's width.
func (p Printer) wrapped(lines string) {
	p.print(ansi.Wrap(lines, max(p.Width, 20), "") + "\n")
}

// Status does nothing: a printed run shows no live status.
func (Printer) Status(dispatch.Status) {}

// Say prints a line of the orchestrator's own progress outside the event stream: on a terminal the
// styled sentence, in a light colour after the organs' ◆, else the plain line, worded as the log
// words it.
func (p Printer) Say(plain, styled string) {
	plain, styled = Printable(plain), Printable(styled) // they can quote an error's words
	if p.Styled {
		p.print(organStyle.Render("◆ ") + sayStyle.Render(styled) + "\n")
		return
	}
	p.sayPlain(plain)
}

// Warn is Say for a warning, worded the same in both and in the warning colour on a terminal.
func (p Printer) Warn(text string) {
	text = Printable(text) // it can quote an organ's error
	if p.Styled {
		p.print(organStyle.Render("◆ ") + deferredStyle.Render(text) + "\n")
		return
	}
	p.sayPlain(text)
}

// sayPlain prints text as Say and Warn do outside a terminal: after the time, as the log has it.
func (p Printer) sayPlain(text string) {
	p.print(fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02 15:04:05"), text))
}

// Report prints the reviewer's Markdown after a blank line, rendered with Glamour on a terminal.
func (p Printer) Report(md string) {
	md = Printable(md) // the organ's words
	if p.Styled {
		r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(max(p.Width-4, 40)))
		if err == nil {
			if out, err := r.Render(md); err == nil {
				p.print("\n" + out)
				return
			}
		}
	}
	p.print("\n" + md + "\n")
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
		return fmt.Sprintf("%s %s  %s",
			ts, organStyle.Render("◆ "+ev.Ticket+" triage: "+ev.Detail), dimStyle.Render(ev.Title))
	case dispatch.EvAsked:
		return fmt.Sprintf("%s %s  %s", ts, stopStyle.Render("? "+ev.Ticket+" needs your answer"), dimStyle.Render(ev.Detail))
	case dispatch.EvAnswered:
		return fmt.Sprintf("%s %s  %s", ts, pickedStyle.Render("↺ "+ev.Ticket+" answered"), dimStyle.Render(ev.Detail))
	case dispatch.EvHold:
		return fmt.Sprintf("%s %s", ts, stopStyle.Render("■ "+Tildify(ev.Text)))
	case dispatch.EvWarn:
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("! "+Tildify(strings.TrimSpace(ev.Text))))
	case dispatch.EvStop:
		return fmt.Sprintf("%s %s", ts, stopStyle.Render("■ "+Tildify(ev.Text)))
	case dispatch.EvDone:
		return closing(ev, 0)
	case dispatch.EvDrain, dispatch.EvResume:
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("■ "+ev.Text))
	case dispatch.EvProbed:
		return fmt.Sprintf("%s %s", ts, closedStyle.Render("■ "+ev.Text))
	case dispatch.EvFullCheck: // after the run: passed, or something to look at
		if ev.Detail == dispatch.FullCheckPassed {
			return fmt.Sprintf("%s %s", ts, closedStyle.Render("✓ "+Tildify(strings.TrimSpace(ev.Text))))
		}
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("! "+Tildify(strings.TrimSpace(ev.Text))))
	}
	return fmt.Sprintf("%s %s", ts, dimStyle.Render(Tildify(ev.Text)))
}

// closing is a terminal's line for a run that ended by itself (ev, an EvDone), in place of its log
// line: a headline in the done colour, with the ♪ of init's sign-off, then, quieter, how many
// tickets and how long the run took (took, when known). A scoped run's SCOPE_ part follows on its
// own line: in the done colour when the scope is finished, else in the deferred colour, as work left.
func closing(ev dispatch.Event, took time.Duration) string {
	text, scope, scoped := strings.Cut(ev.Text, "; SCOPE_")
	facts := tickets(ev.N)
	if took >= time.Second {
		facts += " · " + runTime(took)
	}
	line := doneStyle.Bold(true).Render("♪ "+headline(text, ev.Limit)) + "  " + dimStyle.Render(facts)
	if !scoped {
		return line
	}
	scope = "SCOPE_" + scope
	style := deferredStyle
	if strings.HasPrefix(scope, "SCOPE_DONE") {
		style = doneStyle
	}
	return line + "\n" + style.Render("  "+scope)
}

// headline says how a run ended by itself, from the word its line starts with, as the event stream
// documents it; a word it doesn't know leaves the line as it is.
func headline(text string, limit int) string {
	switch word, _, _ := strings.Cut(text, " "); word {
	case "READY_EMPTY":
		return "Completed the Run"
	case "LIMIT_REACHED":
		return fmt.Sprintf("Reached the ticket limit (%d)", limit)
	case "DRAINED":
		return "Stopped after the running tickets, as asked"
	}
	return text
}

// tickets is "no tickets", "1 ticket" or "n tickets".
func tickets(n int) string {
	switch n {
	case 0:
		return "no tickets"
	case 1:
		return "1 ticket"
	}
	return fmt.Sprintf("%d tickets", n)
}

// runTime is d to the second, as the dashboard's title line shows it, without the zero seconds of
// a whole minute: "1h12m", not "1h12m0s".
func runTime(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d >= time.Minute && d%time.Minute == 0 {
		return strings.TrimSuffix(d.String(), "0s")
	}
	return d.String()
}
