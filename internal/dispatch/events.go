package dispatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Kind is what an Event reports.
type Kind int

// The kinds of event.
const (
	EvInfo     Kind = iota // progress detail (start, worktree)
	EvDispatch             // a ticket was picked up
	EvClosed               // a ticket was completed and merged
	EvDeferred             // a ticket was set aside
	EvWarn                 // a ticket needs review, but the loop continues
	EvStop                 // the loop stopped and needs attention
	EvDone                 // the loop finished normally
	EvTriage               // the triage organ's verdict on a deferred ticket
	EvAsked                // a ticket waits on the maintainer's answer to a question
	EvHold                 // something stopped the run; no new tickets while the running ones finish
	EvDrain                // the maintainer asked to stop after the running tickets
	EvResume               // the maintainer took that back
	EvQueue                // the number of ready tickets waiting changed; for the dashboard, not logged
	EvProbed               // a probe found the machine working after an environment hold: tickets start again
)

// Event is one thing that happened in the run, for the log and the sink.
type Event struct {
	Time   time.Time
	Kind   Kind
	N      int
	Limit  int
	Ticket string
	Title  string    // EvDispatch only
	Queued int       // EvDispatch and EvQueue: ready tickets waiting for a slot
	Solo   SoloState // EvDispatch and EvQueue: the solo ticket running or next, if any
	Detail string    // short suffix for EvClosed / EvDeferred
	Text   string    // the full line written to the log file
}

// Status describes a ticket being worked on. Gone removes it from the display.
type Status struct {
	Ticket   string
	Title    string
	Tab      string
	Started  time.Time
	Agent    string // Herdr agent status
	Activity string // the worker's latest action line
	Doing    string // what a working worker is doing, from its reports: testing, editing, reading or ""
	// Resolving: its branch's rebase stopped on conflicts with Base and was handed back to its
	// worker, and is left in progress until the worker finishes it.
	Resolving bool
	Gone      bool
}

// Sink receives events and live status; the terminal UI and the plain printer implement it.
type Sink interface {
	Event(Event)
	Status(Status)
}

// notifyLimit is how long a notification may take to show before its osascript is stopped.
const notifyLimit = 10 * time.Second

// Log is the run's log file, with its notifications.
type Log struct {
	mu     sync.Mutex
	f      *os.File
	alert  func(text string) // shows a notification; nil when they are off
	lines  []string          // this run's lines, for the reviewer
	shown  sync.WaitGroup    // notifications still being shown
	closed bool              // Close has been called: no more notifications start
}

// OpenLog opens the log file at path for appending. With notify, alerts also show as macOS
// notifications titled with project.
func OpenLog(path string, notify bool, project string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f}
	if _, err := exec.LookPath("osascript"); notify && err == nil { // notifications need macOS
		l.alert = l.inBackground(func(text string) {
			command.Output(context.Background(), notifyLimit, "", "osascript", notification(project, text)...)
		})
	}
	return l, nil
}

// inBackground makes show run on its own goroutine, so a slow notification doesn't hold up the
// loop, and has Close wait for it. Notifications raised after Close are dropped.
func (l *Log) inBackground(show func(text string)) func(text string) {
	return func(text string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed {
			return
		}
		l.shown.Add(1)
		go func() {
			defer l.shown.Done()
			show(text)
		}()
	}
}

// Close waits for the notifications still being shown, each at most notifyLimit, so the run's
// last one isn't lost when orchestra exits, then closes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.shown.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// Line logs text.
func (l *Log) Line(t time.Time, text string) {
	line := t.Format("2006-01-02 15:04:05") + " " + text
	l.mu.Lock()
	fmt.Fprintln(l.f, line)
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

// Alert logs text and shows it as a notification, if they are on.
func (l *Log) Alert(t time.Time, text string) {
	l.Line(t, text)
	if l.alert != nil {
		l.alert(text)
	}
}

// RunLines returns the lines logged in this run.
func (l *Log) RunLines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// Raw appends tool output (git) to the log, as the bash version did.
func (l *Log) Raw(out string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if out = strings.TrimSpace(out); out != "" {
		fmt.Fprintln(l.f, out)
	}
	if err != nil {
		fmt.Fprintln(l.f, err)
	}
}

// notifies says whether events of kind k are shown as notifications: finished tickets and
// anything that needs the maintainer or stops the loop, not progress, dispatches or triage.
func notifies(k Kind) bool {
	switch k {
	case EvClosed, EvDeferred, EvWarn, EvStop, EvDone, EvAsked, EvHold, EvProbed:
		return true
	}
	return false
}

// notification is the osascript arguments showing text, titled with the project. Both go in as
// arguments rather than into the script, so no quoting in them can break it.
func notification(project, text string) []string {
	return []string{
		"-e", "on run argv",
		"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
		"-e", "end run",
		"--", text, "Orchestra: " + project, // -- so text starting with - isn't taken for an option
	}
}

func (o *Loop) emit(ev Event) {
	ev.Time = time.Now()
	switch {
	case notifies(ev.Kind):
		o.log.Alert(ev.Time, ev.Text)
	case ev.Kind != EvQueue:
		o.log.Line(ev.Time, ev.Text)
	}
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	if ev.Kind == EvStop || ev.Kind == EvDone {
		o.final = ev.Text
	}
	o.sink.Event(ev)
}

func (o *Loop) status(st Status) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	o.sink.Status(st)
}

// SetSink switches output, e.g. to plain lines once the terminal view has closed.
func (o *Loop) SetSink(s Sink) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	o.sink = s
}

func (o *Loop) info(format string, a ...any) {
	o.emit(Event{Kind: EvInfo, Text: fmt.Sprintf(format, a...)})
}

func (o *Loop) stop(code int, format string, a ...any) int {
	o.emit(Event{Kind: EvStop, Text: fmt.Sprintf(format, a...)})
	return code
}
