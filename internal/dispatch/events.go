package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Kind is what an Event reports.
type Kind int

// The kinds of event. Each has a name in the event stream (kindNames): give a new one its own.
const (
	EvInfo      Kind = iota // progress detail (start, worktree)
	EvDispatch              // a ticket was picked up
	EvClosed                // a ticket was completed and merged, or closed with no change of its own to merge
	EvDeferred              // a ticket was set aside
	EvWarn                  // something needs review, but the loop continues; Aside if a ticket is left for it
	EvStop                  // the loop stopped and needs attention
	EvDone                  // the loop finished normally
	EvTriage                // the triage organ's verdict on a deferred ticket
	EvAsked                 // a ticket waits on the maintainer's answer to a question
	EvHold                  // something stopped the run; no new tickets while the running ones finish
	EvDrain                 // the maintainer asked to stop after the running tickets
	EvResume                // the maintainer took that back
	EvQueue                 // the number of ready tickets waiting changed; for the dashboard, not logged
	EvProbed                // a probe found the machine working after an environment hold: tickets start again
	EvAnswered              // an asked ticket's question was answered: it comes back, dispatched next
	EvFullCheck             // the full check (check_full) at the end of the run: Detail says how it went
)

// kindNames are the kinds' names in the event stream, which programs read: keep them as they are.
var kindNames = [...]string{
	EvInfo: "info", EvDispatch: "dispatch", EvClosed: "closed", EvDeferred: "deferred", EvWarn: "warn",
	EvStop: "stop", EvDone: "done", EvTriage: "triage", EvAsked: "asked", EvHold: "hold", EvDrain: "drain",
	EvResume: "resume", EvQueue: "queue", EvProbed: "probed", EvAnswered: "answered", EvFullCheck: "full_check",
}

// String is k's name in the event stream: info, dispatch, closed and so on.
func (k Kind) String() string {
	if k >= 0 && int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Event is one thing that happened in the run, for the log, the event stream and the sink.
type Event struct {
	Time   time.Time
	Kind   Kind
	N      int // EvDispatch: the ticket's number in the run; EvDone: the tickets in all, as Text counts them
	Limit  int // EvDispatch and EvDone: the ticket limit
	Ticket string
	// Title: the ticket's title, for EvDispatch, EvClosed, EvDeferred, EvAsked and EvAnswered; the
	// verdict's summary for EvTriage.
	Title  string
	Queued int       // EvDispatch and EvQueue: ready tickets waiting for a slot
	Solo   SoloState // EvDispatch and EvQueue: the solo ticket running or next, if any
	// Detail: a short suffix for EvClosed and EvDeferred, and for an EvWarn that sets its ticket
	// aside, why; for EvAsked and EvAnswered, the question's ID and title ("Q: title"); for EvStop,
	// what stopped the run, the word its line starts with (PAUSED, INTERRUPTED, …).
	Detail string
	Text   string // the full line written to the log file
	// Closed and SetAside: for EvDone, the tickets closed in the run (merged, or with nothing to
	// merge), and those set aside (deferred, or left for review), as their events said.
	Closed, SetAside int
	// Aside: an EvWarn that leaves Ticket set aside for review, out of this run (CHECKS_FAILED,
	// DEFER_FAILED, …), not one about a ticket still running or already deferred.
	Aside bool
	// Blocked: Ticket's work is done, but it can't merge until the maintainer acts; why, in a few
	// words ("main checkout has uncommitted changes", "merge conflict in a.go"). Set on the EvHold or
	// EvStop for a DIRTY_TREE, GIT_FAILED or MERGE_FAILED met as it merged, and on an EvWarn for its
	// MERGE_CONFLICT; not on CHECKS_FAILED or CLOSED_WITHOUT_COMMIT, where the work itself needs a look.
	Blocked string
	// Suite and Output: for an EvFullCheck that failed, the suite it failed in (the last line its
	// output starts with project.SuiteMarker, or else the check itself; "" when the setup before it
	// failed) and where its whole output is.
	Suite, Output string
}

// Status describes a ticket being worked on. Gone removes it from the display.
type Status struct {
	Ticket   string
	Title    string
	Tab      string
	Started  time.Time
	Agent    AgentState // as Herdr last reported it; "" before the first read or when it failed
	Activity string     // the worker's latest action line
	Doing    string     // what a working worker is doing, from its reports: testing, editing, reading or ""
	// Resolving: its branch's rebase stopped on conflicts with Base and was handed back to its
	// worker, and is left in progress until the worker finishes it.
	Resolving  bool
	Fixing     bool // its check failed on its rebased branch and was handed back to its worker to fix
	Unreadable bool // the last read of Agent failed
	Gone       bool
}

// Sink receives events and live status; the terminal UI and the plain printer implement it.
type Sink interface {
	Event(Event)
	Status(Status)
}

// notifyLimit is how long a notification may take to show before its osascript is stopped.
const notifyLimit = 10 * time.Second

// Log is the run's log file, with its notifications, and its event stream (see Begin).
type Log struct {
	mu      sync.Mutex
	f       *os.File
	project string              // what notifications are titled with: the repository folder's name
	alert   func(args []string) // shows a notification, given osascript's arguments; nil when they are off
	lines   []string            // this run's lines, for the reviewer
	shown   sync.WaitGroup      // notifications still being shown
	closed  bool                // Close has been called: no more notifications start

	events     *os.File  // the event stream, from Begin to End; nil when it isn't open
	run        time.Time // the run's start, in each of its records
	eventsSaid bool      // the event stream failed, and the log has said so
}

// OpenLog opens the log file at path for appending. With notify, Notify shows macOS notifications
// titled with title.
func OpenLog(path string, notify bool, title string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f, project: title}
	if _, err := exec.LookPath("osascript"); notify && err == nil { // notifications need macOS
		l.alert = l.inBackground(func(args []string) {
			// Best effort: the line is in the log already, a notification that fails is only not shown.
			_, _ = command.Output(context.Background(), notifyLimit, "", "osascript", args...)
		})
	}
	return l, nil
}

// inBackground makes show run on its own goroutine, so a slow notification doesn't hold up the
// loop, and has Close wait for it. Notifications raised after Close are dropped.
func (l *Log) inBackground(show func(args []string)) func(args []string) {
	return func(args []string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed {
			return
		}
		l.shown.Go(func() { show(args) })
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
	l.closeEvents()
	return l.f.Close()
}

// CloseNow closes the log file without waiting for the notifications still being shown, which
// show anyway: each runs in its own process group. It is for orchestra quitting at once.
func (l *Log) CloseNow() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.closeEvents()
	return l.f.Close()
}

// logTime is how the log dates its lines.
const logTime = "2006-01-02 15:04:05"

// Line logs text.
func (l *Log) Line(t time.Time, text string) {
	line := t.Format(logTime) + " " + text
	l.mu.Lock()
	fmt.Fprintln(l.f, line)
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

// Notify shows notice as a notification titled with the project, if they are on; "" shows none.
// The notice is a few words (see Notice): the line it is about goes to the log.
func (l *Log) Notify(notice string) {
	if l.alert != nil && notice != "" {
		l.alert(notification(l.project, notice))
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

// The event stream is the run's events for programs, agents and scripts, as the log is for people,
// whose wording may change: .orchestra/run/events.jsonl in the main checkout, out of git with the
// rest of .orchestra/run/, which keeps every run, as the log does. Each record is a JSON object on a
// line of its own, appended in a single write, so a reader tailing the file never sees half of one:
// a run's start record, one for each event it emits (an EvQueue too, which the log leaves out), and
// its end record, with the exit code. Each gives the run's start time, which tells the runs in the
// file apart. The README documents the records; keep it in step.

// EventsName is the event stream's file in the main checkout's .orchestra/run/.
const EventsName = "events.jsonl"

// RunStart is what a run's start record says about it.
type RunStart struct {
	Started     time.Time // when the run started, as its lock says: every record of the run gives it
	Version     string
	Repo        string // the main checkout
	Branch      string // where finished tickets land
	Scope       string // the ticket the run is scoped to (--ticket, or a feature's epic); "" for all of bd ready
	Feature     string // the feature request (--feature), if any
	Concurrency int
}

// recordHead begins every record: when it was written, the run's start and what it is.
type recordHead struct {
	Time time.Time `json:"time"`
	Run  time.Time `json:"run"`
	Kind string    `json:"kind"`
}

// startRecord is a run's first record: what it runs.
type startRecord struct {
	recordHead
	Version     string `json:"version"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Scope       string `json:"scope,omitempty"`
	Feature     string `json:"feature,omitempty"`
	Concurrency int    `json:"concurrency"`
}

// eventRecord is an Event. Queued and Solo are given for EvDispatch and EvQueue, N and Limit for
// EvDispatch, as the Event sets them; a queue of 0 is given too.
type eventRecord struct {
	recordHead
	Ticket  string      `json:"ticket,omitempty"`
	Title   string      `json:"title,omitempty"`
	Detail  string      `json:"detail,omitempty"`
	Text    string      `json:"text,omitempty"`
	Aside   bool        `json:"aside,omitempty"`
	Blocked string      `json:"blocked,omitempty"`
	Suite   string      `json:"suite,omitempty"`
	Output  string      `json:"output,omitempty"`
	N       *int        `json:"n,omitempty"`
	Limit   *int        `json:"limit,omitempty"`
	Queued  *int        `json:"queued,omitempty"`
	Solo    *soloRecord `json:"solo,omitempty"`
}

// soloRecord is a SoloState.
type soloRecord struct {
	Ticket string `json:"ticket"`
	Next   bool   `json:"next,omitempty"`
}

// endRecord is a run's last record: the code orchestra exits with.
type endRecord struct {
	recordHead
	Code int `json:"code"`
}

// Begin opens the event stream in the main checkout repo for the run s describes, and appends its
// start record. Like the probe's file, it is reached through an os.Root at the checkout, which won't
// follow a symlink out of it; one in its place inside the checkout is replaced by the file rather
// than written through, as WriteRun does. A stream that can't be opened is logged, and the run goes
// on without it.
func (l *Log) Begin(repo string, s RunStart) {
	f, err := openEvents(repo)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.eventsFailed(err)
		return
	}
	l.events, l.run = f, s.Started
	l.write(startRecord{recordHead: recordHead{Time: time.Now(), Run: s.Started, Kind: "start"},
		Version: s.Version, Repo: s.Repo, Branch: s.Branch, Scope: s.Scope, Feature: s.Feature,
		Concurrency: s.Concurrency})
}

// openEvents opens the event stream in the main checkout repo for appending, making it if need be.
func openEvents(repo string) (*os.File, error) {
	root, err := project.OpenRun(repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // the file stays open without it
	rel := project.RunPath(EventsName)
	if fi, err := root.Lstat(rel); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if err := project.RemoveRun(root, repo, rel); err != nil {
			return nil, err
		}
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, project.RunError(repo, rel, err)
	}
	return f, nil
}

// Record appends ev to the event stream, if it is open.
func (l *Log) Record(ev Event) {
	r := eventRecord{recordHead: recordHead{Time: ev.Time, Kind: ev.Kind.String()},
		Ticket: ev.Ticket, Title: ev.Title, Detail: ev.Detail, Text: ev.Text, Aside: ev.Aside, Blocked: ev.Blocked,
		Suite: ev.Suite, Output: ev.Output}
	if ev.Kind == EvDispatch {
		r.N, r.Limit = &ev.N, &ev.Limit
	}
	if ev.Kind == EvDispatch || ev.Kind == EvQueue {
		r.Queued = &ev.Queued
		if ev.Solo != (SoloState{}) {
			r.Solo = &soloRecord{Ticket: ev.Solo.Ticket, Next: ev.Solo.Next}
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Run = l.run
	l.write(r)
}

// End appends the run's end record, with the exit code orchestra ends with, and closes the event
// stream: from then on, records are dropped.
func (l *Log) End(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.write(endRecord{recordHead: recordHead{Time: time.Now(), Run: l.run, Kind: "end"}, Code: code})
	l.closeEvents()
}

// write appends r to the event stream, if it is open, as one line in a single write. The first
// failure is logged; the run goes on regardless, and later records are still tried. The caller
// holds mu.
func (l *Log) write(r any) {
	if l.events == nil {
		return
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // the text stays as the log has it: "->", not "-\u003e"
	err := enc.Encode(r)     // ends the line
	if err == nil {
		_, err = l.events.Write(b.Bytes())
	}
	if err != nil {
		l.eventsFailed(err)
	}
}

// eventsFailed logs, once, that the event stream couldn't be opened or written to. Not as one of
// the run's lines: the reviewer's evidence is the run, not orchestra's own files. The caller holds mu.
func (l *Log) eventsFailed(err error) {
	if l.eventsSaid {
		return
	}
	l.eventsSaid = true
	fmt.Fprintln(l.f, time.Now().Format(logTime)+" events not recorded in "+project.RunPath(EventsName)+": "+
		err.Error()+"; the run goes on, and later failures aren't logged")
}

// closeEvents closes the event stream, if it is open. The caller holds mu.
func (l *Log) closeEvents() {
	if l.events == nil {
		return
	}
	_ = l.events.Close() // each record was written whole as it came: nothing is left to flush
	l.events = nil
}

// noticeWidth is how many characters of a title or reason a notification gives, cut with "…": it is
// read at a glance, and the log has the rest.
const noticeWidth = 60

// Notice is what a notification says of ev, in a few words, or "" for an event that doesn't notify.
// Only the outcomes that matter to the maintainer do: a ticket merged, set aside or waiting on a
// question, and the run stopping or finishing. Holds, probes, the other warnings and triage are in
// the log, the event stream and the dashboard.
func Notice(ev Event) string {
	switch ev.Kind {
	case EvClosed:
		return about("Closed "+ev.Ticket, ev.Title)
	case EvDeferred:
		return about("Set aside "+ev.Ticket, ev.Detail)
	case EvWarn:
		if ev.Aside && ev.Ticket != "" { // CHECKS_FAILED, MERGE_CONFLICT, …: not LONG_RUNNING and the like
			return about("Set aside "+ev.Ticket, ev.Detail)
		}
	case EvAsked:
		_, question, _ := strings.Cut(ev.Detail, ": ") // after the question's ID, its title
		return about(ev.Ticket+" needs your answer", question)
	case EvStop:
		n := "Stopped"
		if ev.Detail != "" {
			n += ": " + ev.Detail
		}
		if ev.Ticket != "" {
			n += " on " + ev.Ticket
		}
		return n
	case EvFullCheck:
		if ev.Suite != "" {
			return about("Full check failed", ev.Suite)
		}
		if ev.Detail == FullCheckFailed || ev.Detail == FullCheckTimedOut { // in the setup before it
			return about("Full check failed", "setup")
		}
	case EvDone:
		n := "Finished the run · " + closedCount(ev.Closed)
		if ev.SetAside > 0 {
			n += fmt.Sprintf(" · %d set aside", ev.SetAside)
		}
		return n
	}
	return ""
}

// about is what happened, then what it happened over (a title, a reason), shortened to noticeWidth.
func about(what, over string) string {
	over = strings.Join(strings.Fields(over), " ")
	if over == "" {
		return what
	}
	if r := []rune(over); len(r) > noticeWidth {
		over = strings.TrimRight(string(r[:noticeWidth-1]), " ") + "…"
	}
	return what + " · " + over
}

// closedCount is "no tickets closed", "1 ticket closed" or "n tickets closed".
func closedCount(n int) string {
	switch n {
	case 0:
		return "no tickets closed"
	case 1:
		return "1 ticket closed"
	}
	return fmt.Sprintf("%d tickets closed", n)
}

// notification is the osascript arguments showing notice, titled with the project. Both go in as
// arguments rather than into the script, so no quoting in them can break it.
func notification(project, notice string) []string {
	return []string{
		"-e", "on run argv",
		"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
		"-e", "end run",
		"--", notice, project, // -- so a notice starting with - isn't taken for an option
	}
}

func (o *Loop) emit(ev Event) {
	ev.Time = time.Now()
	o.tally(&ev)
	if ev.Kind != EvQueue {
		o.log.Line(ev.Time, ev.Text)
	}
	o.log.Notify(Notice(ev))
	o.log.Record(ev)
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	if ev.Kind == EvStop || ev.Kind == EvDone {
		o.final = ev.Text
	}
	o.sink.Event(ev)
}

// tally counts the tickets closed and set aside in the run, as their events say, and gives the
// counts to the run's EvDone. A ticket set aside counts once, however many times it is (its check
// failing again, say), and no longer once it merges (see recheck).
func (o *Loop) tally(ev *Event) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	switch {
	case ev.Kind == EvClosed:
		o.closedN++
		if o.counted[ev.Ticket] {
			o.asideN--
			delete(o.counted, ev.Ticket)
		}
	case ev.Kind == EvDeferred, ev.Kind == EvWarn && ev.Aside && ev.Ticket != "":
		if o.counted[ev.Ticket] {
			return
		}
		if o.counted == nil {
			o.counted = map[string]bool{}
		}
		o.counted[ev.Ticket] = true
		o.asideN++
	case ev.Kind == EvDone:
		ev.Closed, ev.SetAside = o.closedN, o.asideN
	}
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

// stop ends the run for s with text, its line, and returns the code orchestra exits with.
func (o *Loop) stop(s *stopReason, text string) int {
	o.emit(Event{Kind: EvStop, Ticket: s.ticket, Detail: string(s.kind), Blocked: s.blocked, Text: text})
	return s.code
}
