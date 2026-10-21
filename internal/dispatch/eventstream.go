package dispatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The event stream is the run's events for programs, agents and scripts, as the log is for people,
// whose wording may change: .orchestra/run/events.jsonl in the main checkout, out of git with the
// rest of .orchestra/run/, which keeps every run, as the log does. Each record is a JSON object on a
// line of its own, appended in a single write, so a reader tailing the file never sees half of one:
// a run's start record, one for each event it emits (an EvQueue too, which the log leaves out), one
// for each check or setup it runs (see Log.Check), which the log has no line for, and its end record,
// with the exit code. Each gives the run's start time, which tells the runs in the
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
	Spec        string // the spec (--spec), by its path in the repository, if any
	Overnight   bool   // an overnight run (--overnight)
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
	Spec        string `json:"spec,omitempty"`
	Overnight   bool   `json:"overnight,omitempty"`
	Concurrency int    `json:"concurrency"`
}

// eventRecord is an Event. Queued, Held, Unclosed and Solo are given for EvDispatch and EvQueue, N
// and Limit for EvDispatch, as the Event sets them; a queue of 0 is given too, an unclosed count only
// once read, the tickets held back only when there are some, the limit only when the run has one,
// MachineFull only when set. Reset is given for EvLimited, when known, and Shared for EvShared;
// Commit, Seconds and Result for an EvFullCheck that ran.
type eventRecord struct {
	recordHead
	Ticket   string      `json:"ticket,omitempty"`
	Title    string      `json:"title,omitempty"`
	Detail   string      `json:"detail,omitempty"`
	Text     string      `json:"text,omitempty"`
	Aside    bool        `json:"aside,omitempty"`
	Blocked  string      `json:"blocked,omitempty"`
	Suite    string      `json:"suite,omitempty"`
	Output   string      `json:"output,omitempty"`
	Commit   string      `json:"commit,omitempty"`
	Seconds  *seconds    `json:"seconds,omitempty"`
	Result   CheckResult `json:"result,omitempty"`
	Reset    *time.Time  `json:"reset,omitempty"`
	N        *int        `json:"n,omitempty"`
	Limit    *int        `json:"limit,omitempty"`
	Queued   *int        `json:"queued,omitempty"`
	Held     int         `json:"held,omitempty"`
	HeldBy   *heldRecord `json:"held_by,omitempty"`
	Unclosed *int        `json:"unclosed,omitempty"`
	Solo     *soloRecord `json:"solo,omitempty"`
	Machine  bool        `json:"machine_full,omitempty"`
	Shared   *int        `json:"shared,omitempty"`

	// Phases, HandBacks and Carried: an event's Times, given only on a record that ends its ticket's part.
	Phases    *phasesRecord    `json:"phases,omitempty"`
	HandBacks *handBacksRecord `json:"hand_backs,omitempty"`
	Carried   bool             `json:"carried,omitempty"`
	// Tokens: an event's Tokens, given only on a record that ends its ticket's part.
	Tokens map[string]tokensRecord `json:"tokens,omitempty"`
}

// tokensRecord is what was used of one model (ModelTokens).
type tokensRecord struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
}

// tokensOf is t as the event stream gives it; nil for none.
func tokensOf(t Tokens) map[string]tokensRecord {
	if len(t) == 0 {
		return nil
	}
	r := make(map[string]tokensRecord, len(t))
	for model, n := range t {
		r[model] = tokensRecord{Input: n.Input, Output: n.Output, CacheWrite: n.CacheWrite, CacheRead: n.CacheRead}
	}
	return r
}

// organsRecord is what the run's organ calls cost, as their results said.
type organsRecord struct {
	Calls   int     `json:"calls"`
	Turns   int     `json:"turns"`
	CostUSD float64 `json:"cost_usd"`
}

// checkRecord is a CheckRun: a check or a setup that ran, as it ended.
type checkRecord struct {
	recordHead
	Ticket  string      `json:"ticket,omitempty"`
	Suite   CheckSuite  `json:"suite"`
	Command string      `json:"command"`
	Commit  string      `json:"commit,omitempty"`
	Seconds seconds     `json:"seconds"`
	Result  CheckResult `json:"result"`
	Output  string      `json:"output,omitempty"`
	Train   bool        `json:"train,omitempty"`
}

// heldRecord is a HeldBack: how many ready tickets each reason holds back.
type heldRecord struct {
	Overlap int `json:"overlap,omitempty"`
	Parent  int `json:"parent,omitempty"`
	Merge   int `json:"merge,omitempty"`
	Solo    int `json:"solo,omitempty"`
	Setup   int `json:"setup,omitempty"`
}

// phasesRecord is PhaseTimes.Spent, in seconds, by the dashboard's name for each phase: a phase the ticket
// never entered is left out.
type phasesRecord struct {
	Working   *seconds `json:"working,omitempty"`
	Waiting   *seconds `json:"waiting,omitempty"`
	Merging   *seconds `json:"merging,omitempty"`
	Resolving *seconds `json:"resolving,omitempty"`
	Fixing    *seconds `json:"fixing,omitempty"`
}

// handBacksRecord is a ticket's hand-backs: to resolve its rebase's conflicts, and to fix its check.
type handBacksRecord struct {
	Resolve int `json:"resolve"`
	Fix     int `json:"fix"`
}

// seconds is a duration in seconds, written with one decimal.
type seconds time.Duration

// MarshalJSON writes s in seconds with one decimal, as in 812.4 or 41.0.
func (s seconds) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, time.Duration(s).Seconds(), 'f', 1, 64), nil
}

// setTimes fills r's phases, hand-backs and carried from t.
func (r *eventRecord) setTimes(t PhaseTimes) {
	spent := func(p Phase) *seconds {
		if !t.Entered[p] {
			return nil
		}
		s := seconds(t.Spent[p])
		return &s
	}
	r.Phases = &phasesRecord{Working: spent(PhaseWorking), Waiting: spent(PhaseQueued), Merging: spent(PhaseMerging),
		Resolving: spent(PhaseResolving), Fixing: spent(PhaseFixing)}
	r.HandBacks = &handBacksRecord{Resolve: t.Resolves, Fix: t.Fixes}
	r.Carried = t.Carried
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
	// Tokens: what the workers of the run's tickets used, the sum of their records' tokens, by model;
	// Organs: what its organ calls cost. Each is left out when there is none.
	Tokens map[string]tokensRecord `json:"tokens,omitempty"`
	Organs *organsRecord           `json:"organs,omitempty"`
}

// Begin opens the event stream in the main checkout repo for the run s describes, and appends its
// start record. Like the probe's file, it is reached through an os.Root at the checkout, which won't
// follow a symlink out of it; one in its place inside the checkout is replaced by the file rather
// than written through, as WriteRun does. A stream that can't be opened is logged, and the run goes
// on without it. The check records of the checks that ended before it follow the start record (see
// Check).
func (l *Log) Begin(repo string, s RunStart) {
	f, err := openEvents(repo)
	l.mu.Lock()
	defer l.mu.Unlock()
	early := l.early
	l.begun, l.early = true, nil
	if err != nil {
		l.eventsFailed(err)
		return
	}
	l.events, l.run = f, s.Started
	l.write(startRecord{recordHead: recordHead{Time: time.Now(), Run: s.Started, Kind: "start"},
		Version: s.Version, Repo: s.Repo, Branch: s.Branch, Scope: s.Scope, Feature: s.Feature, Spec: s.Spec,
		Overnight: s.Overnight, Concurrency: s.Concurrency})
	for _, c := range early {
		l.writeCheck(c)
	}
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
		r.N = &ev.N
		if ev.Limit != NoLimit {
			r.Limit = &ev.Limit
		}
	}
	if ev.Times != nil {
		r.setTimes(*ev.Times)
	}
	r.Tokens = tokensOf(ev.Tokens)
	if ev.Kind == EvLimited && !ev.Reset.IsZero() {
		r.Reset = &ev.Reset
	}
	if ev.Kind == EvShared {
		r.Shared = &ev.Shared
	}
	if c := ev.Ran; c != nil {
		s := seconds(c.Took)
		r.Commit, r.Seconds, r.Result = c.Commit, &s, c.Result
	}
	if ev.Kind == EvDispatch || ev.Kind == EvQueue {
		r.Queued = &ev.Queued
		if h := ev.Held; h.Total() > 0 {
			r.Held, r.HeldBy = h.Total(), &heldRecord{Overlap: h.Overlap, Parent: h.Parent, Merge: h.Merge, Solo: h.Solo,
				Setup: h.Setup}
		}
		if ev.Unclosed >= 0 {
			r.Unclosed = &ev.Unclosed
		}
		if ev.Solo != (SoloState{}) {
			r.Solo = &soloRecord{Ticket: ev.Solo.Ticket, Next: ev.Solo.Next}
		}
		r.Machine = ev.MachineFull
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Run = l.run
	l.tokens = l.tokens.Add(ev.Tokens)
	l.write(r)
}

// Check appends c's check record to the event stream, as c ended. One that ended before the stream
// opened, as the overnight pre-flight's check of Base does, is appended once it opens, after the run's
// start record: a run that ends before then has no records.
func (l *Log) Check(c CheckRun) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.begun {
		l.early = append(l.early, c)
		return
	}
	l.writeCheck(c)
}

// writeCheck appends c's check record, as written now. The caller holds mu.
func (l *Log) writeCheck(c CheckRun) {
	l.write(checkRecord{recordHead: recordHead{Time: time.Now(), Run: l.run, Kind: "check"},
		Ticket: c.Ticket, Suite: c.Suite, Command: c.Command, Commit: c.Commit, Seconds: seconds(c.Took),
		Result: c.Result, Output: c.Output, Train: c.Train})
}

// End appends the run's end record, with the exit code orchestra ends with and the run's totals of
// tokens and organ costs, and closes the event stream: from then on, records are dropped.
func (l *Log) End(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	end := endRecord{recordHead: recordHead{Time: time.Now(), Run: l.run, Kind: "end"}, Code: code,
		Tokens: tokensOf(l.tokens)}
	if l.organs.Calls > 0 {
		organs := l.organs
		end.Organs = &organs
	}
	l.write(end)
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
// question, the run holding for the usage limit, and the run stopping or finishing. The other holds,
// probes, the other warnings and triage are in the log, the event stream and the dashboard.
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
	case EvLimited:
		if ev.Reset.IsZero() {
			return "Usage limit reached · the run waits"
		}
		return "Usage limit reached · resets " + ResetLabel(ev.Reset, ev.Time)
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
