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
}

// Event prints ev as a line; the queue count, which only the dashboard shows, is skipped.
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

// Status does nothing: a printed run shows no live status.
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
		return fmt.Sprintf("%s %s", ts, doneStyle.Render("■ "+ev.Text))
	case dispatch.EvDrain, dispatch.EvResume:
		return fmt.Sprintf("%s %s", ts, deferredStyle.Render("■ "+ev.Text))
	case dispatch.EvProbed:
		return fmt.Sprintf("%s %s", ts, closedStyle.Render("■ "+ev.Text))
	}
	return fmt.Sprintf("%s %s", ts, dimStyle.Render(Tildify(ev.Text)))
}
