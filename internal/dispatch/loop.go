// Package dispatch is the orchestra loop: it hands ready tickets to workers, up to a number at a
// time, watches them settle, merges finished tickets one at a time and sets the rest aside.
package dispatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Exit codes, unchanged from orchestrate.sh.
const (
	ExitOK          = 0   // nothing left in bd ready, or LIMIT reached
	ExitSetup       = 2   // setup problem found before starting
	ExitStuck       = 3   // a worker stayed blocked >4 min, or went idle with its ticket in_progress
	ExitTool        = 4   // Herdr, Beads or git failure
	ExitDirty       = 5   // uncommitted changes in the main checkout, or it left its branch
	ExitMerge       = 6   // a finished ticket's branch does not fast-forward
	ExitInterrupted = 130 // stopped with Ctrl+C
)

type Kind int

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
)

type Event struct {
	Time   time.Time
	Kind   Kind
	N      int
	Limit  int
	Ticket string
	Title  string // EvDispatch only
	Queued int    // EvDispatch only: ready tickets behind this one
	Detail string // short suffix for EvClosed / EvDeferred
	Text   string // the full line written to the log file
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
	Gone     bool
}

// Sink receives events and live status; the terminal UI and the plain printer implement it.
type Sink interface {
	Event(Event)
	Status(Status)
}

// ---- Log file and notifications ------------------------------------------------------

type Log struct {
	mu      sync.Mutex
	f       *os.File
	notify  bool
	project string
	lines   []string // this run's lines, for the reviewer
}

func OpenLog(path string, notify bool, project string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("osascript"); err != nil {
		notify = false // notifications need macOS
	}
	return &Log{f: f, notify: notify, project: project}, nil
}

func (l *Log) Line(t time.Time, text string) {
	line := t.Format("2006-01-02 15:04:05") + " " + text
	l.mu.Lock()
	fmt.Fprintln(l.f, line)
	l.lines = append(l.lines, line)
	l.mu.Unlock()
	if l.notify && notifiable(text) {
		go l.show(text)
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

// Notify only for finished tickets and anything that stops the loop.
func notifiable(text string) bool {
	for _, k := range []string{"closed", "deferred", "BLOCKED", "PAUSED", "DIRTY_TREE", "READY_EMPTY",
		"LIMIT_REACHED", "FAILED", "UNREADABLE", "WITHOUT_COMMIT", "INTERRUPTED", "REPORT", "ASKED", "HOLD", "CONFLICT"} {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

func (l *Log) show(text string) {
	msg := strings.NewReplacer(`\`, "", `"`, "'").Replace(text) // keep AppleScript quoting intact
	exec.Command("osascript", "-e",
		fmt.Sprintf(`display notification "%s" with title "Orchestra: %s"`, msg, l.project)).Run()
}

// ---- Orchestrator --------------------------------------------------------------------

type Loop struct {
	cfg    Config
	log    *Log
	sink   Sink
	sinkMu sync.Mutex // triage reports from its own goroutine
	prompt string     // worker prompt with the TICKET_ID placeholder
	count  int

	started   time.Time
	startHead string // Base's commit when the run started; the reviewer reads commits since
	final     string // the stop or done line

	// repoMu serialises git writes to the main repository (worktrees, rebases, merges, branch
	// deletions): workers run side by side, and git's lock files allow one writer at a time.
	repoMu sync.Mutex
	// mergeMu is the merge queue: a finished ticket holds it through rebase, check and merge, so
	// Base doesn't move while its rebased code is checked. Worktree creation isn't held up.
	mergeMu sync.Mutex
	active  map[string]Status // tickets being worked on; left set for those still running at the end

	tickets   Tickets
	notes     Notes
	tabs      Tabs
	starter   Starter
	namer     Namer
	agents    Agents
	reporter  Reporter
	checkout  Checkout
	worktrees Worktrees
	merger    Merger
	history   History
	organ     organ.Client
	organCtx  context.Context // cancelled when the maintainer skips the organs
	mu        sync.Mutex
	asideIDs  []string          // tickets deferred or left unmerged in this run
	unmerged  map[string]string // tickets closed but left unmerged, in this run or an earlier one, with why
	labelled  map[string]bool   // tickets carrying UnmergedLabel, which a merge removes
	holdSaid  map[string]string // why each held ticket waits, as last said
	askedIDs  map[string]bool   // tickets set aside in this run to wait on a question

	// Triage's queue. Workers add to it until FinishTriage closes it; a worker still settling
	// after that finds it closed rather than a closed channel.
	triageMu     sync.Mutex
	triageOn     bool // StartTriage was called
	triageClosed bool // FinishTriage was called
	triageQ      []organ.Deferral
	triageWake   chan struct{} // buffered 1: something was queued, or the queue closed
	triageDone   chan struct{}

	wait timing // how long it waits on things; tests shorten it

	// ReportInterrupt logs Ctrl+C from the loop itself; in the terminal UI the command does it (it
	// knows what stopped the run: Ctrl+C, a signal or the dashboard failing).
	ReportInterrupt bool
}

// New sets up a run: the worker prompt (with TICKET_ID), and its connections.
func New(cfg Config, log *Log, prompt string, d Deps) *Loop {
	return &Loop{cfg: cfg, log: log, prompt: prompt, tickets: d.Tickets, notes: d.Notes, tabs: d.Tabs, starter: d.Starter, namer: d.Namer, agents: d.Agents, reporter: d.Reporter,
		checkout: d.Checkout, worktrees: d.Worktrees, merger: d.Merger, history: d.History, organ: d.Advisor, organCtx: d.AdviceCtx}
}

// Final is the line the run ended with.
func (o *Loop) Final() string {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	return o.final
}

func (o *Loop) emit(ev Event) {
	ev.Time = time.Now()
	o.log.Line(ev.Time, ev.Text)
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

// stopReason ends the run: a ticket hit something that needs the maintainer, or a tool failed.
// With several workers, no new tickets start and the running ones finish first.
type stopReason struct {
	code int
	text string
}

// errInterrupted is returned by a worker when Ctrl+C cancelled the run.
var errInterrupted = &stopReason{code: ExitInterrupted}

func halt(code int, format string, a ...any) *stopReason {
	return &stopReason{code: code, text: fmt.Sprintf(format, a...)}
}

func (o *Loop) setActive(st Status) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		o.active = map[string]Status{}
	}
	o.active[st.Ticket] = st
}

func (o *Loop) clearActive(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.active, id)
}

// activeList returns the tickets being worked on, oldest first.
func (o *Loop) activeList() []Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	var l []Status
	for _, st := range o.active {
		l = append(l, st)
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Started.Before(l[j].Started) })
	return l
}

// Running returns the tickets being worked on, oldest first: after an interrupt, those whose
// workers were left running.
func (o *Loop) Running() []Status { return o.activeList() }

func (o *Loop) interrupted() int {
	if !o.ReportInterrupt {
		return ExitInterrupted
	}
	o.emit(Event{Kind: EvStop, Text: "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree"})
	return ExitInterrupted
}

// timing is how long the loop waits on things. A zero field means the default.
type timing struct {
	poll       time.Duration // between reads of a worker's status: 3 seconds
	startRetry time.Duration // after a failed start, before looking for the agent: 3 seconds
	blocked    time.Duration // a worker blocked for longer stops the run: blockedLimit
	idleGrace  time.Duration // idleGrace
	settle     time.Duration // SettleWait
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// pollEvery is how often a worker's status is read while waiting on it.
func (o *Loop) pollEvery() time.Duration {
	return orDefault(o.wait.poll, 3*time.Second)
}

// sleep waits for d, returning false if ctx is cancelled first.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// result is what a worker returns to Run.
type result struct {
	id   string
	stop *stopReason
}

// Run works through bd ready with up to Concurrency workers at a time and returns the exit code.
func (o *Loop) Run(ctx context.Context) int {
	c := o.cfg
	o.count = c.DoneSoFar
	o.started = time.Now()
	o.startHead = o.checkout.Head(c.Repo, c.Base)
	o.info("START orchestra %s in %s on %s (done so far: %d, limit: %d, concurrent: %d, workspace: %s, agent: %s, worktrees: %s)",
		c.Version, c.Repo, c.Base, o.count, c.Limit, c.Concurrency, c.Workspace, c.AgentKind, c.WTRoot)
	if s := o.loadUnmerged(); s != nil {
		return o.stop(s.code, "%s", s.text)
	}

	results := make(chan result, c.Concurrency) // buffered: a worker finishing after settle never blocks
	inflight := map[string]bool{}
	var stop *stopReason // the first reason decides the exit code
	var alsoStopped []string
	for {
		// Start tickets while there are free slots, unless something has stopped the run.
		for stop == nil && ctx.Err() == nil && len(inflight) < c.Concurrency && o.count < c.Limit {
			t, queued, s := o.next(inflight)
			if s != nil {
				stop = s
				if len(inflight) > 0 {
					o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
						"HOLD: %s; no new tickets while the %d running finish", s.text, len(inflight))})
				}
				break
			}
			if t == nil {
				break // nothing ready that isn't already running
			}
			o.count++
			inflight[t.ID] = true
			o.emit(Event{Kind: EvDispatch, N: o.count, Limit: c.Limit, Ticket: t.ID, Title: t.Title, Queued: queued,
				Text: fmt.Sprintf("[%d/%d] %s dispatching: %s", o.count, c.Limit, t.ID, t.Title)})
			go func(t Ticket) { results <- result{t.ID, o.work(ctx, t)} }(*t)
		}
		if len(inflight) == 0 {
			break
		}
		select {
		case r := <-results:
			delete(inflight, r.id)
			if r.stop == nil || r.stop == errInterrupted {
				continue
			}
			// Every reason is shown as it arrives. The first decides the exit code; the final
			// line gives it and then the others.
			first := stop == nil
			if first {
				stop = r.stop
			} else {
				alsoStopped = append(alsoStopped, r.stop.text)
			}
			switch {
			case len(inflight) > 0:
				o.emit(Event{Kind: EvHold, Ticket: r.id, Text: fmt.Sprintf(
					"HOLD: %s; no new tickets while the %d running finish", r.stop.text, len(inflight))})
			case !first:
				o.emit(Event{Kind: EvHold, Ticket: r.id, Text: "HOLD: " + r.stop.text})
			}
		case <-ctx.Done():
			o.settle(results, inflight)
			return o.interrupted()
		}
	}
	switch {
	case ctx.Err() != nil:
		return o.interrupted()
	case stop != nil:
		text := stop.text
		for _, t := range alsoStopped {
			text += "; also " + t
		}
		return o.stop(stop.code, "%s", text)
	case o.count >= c.Limit:
		o.emit(Event{Kind: EvDone, Text: fmt.Sprintf("LIMIT_REACHED at %d tickets", o.count)})
	default:
		o.emit(Event{Kind: EvDone, Text: fmt.Sprintf("READY_EMPTY after %d tickets", o.count)})
	}
	return ExitOK
}

// SettleWait is how long Run waits after Ctrl+C for its workers to return, so what follows (triage,
// the run report) reads a loop that has stopped changing. A worker notices the cancellation within
// seconds unless a command it runs hangs.
const SettleWait = 10 * time.Second

// settle waits for the workers in flight to return, or for SettleWait.
func (o *Loop) settle(results <-chan result, inflight map[string]bool) {
	timeout := time.NewTimer(orDefault(o.wait.settle, SettleWait))
	defer timeout.Stop()
	for len(inflight) > 0 {
		select {
		case r := <-results:
			delete(inflight, r.id)
		case <-timeout.C:
			return
		}
	}
}

// next returns the highest-priority ready ticket not already running, with how many others are
// ready, or a reason to stop. The main checkout must be clean and on Base, since finished tickets
// are fast-forwarded into it; the check waits for any merge in progress.
func (o *Loop) next(running map[string]bool) (*Ticket, int, *stopReason) {
	o.repoMu.Lock()
	s := o.checkoutUnready("")
	o.repoMu.Unlock()
	if s != nil {
		return nil, 0, s
	}
	ready, err := o.tickets.Ready()
	if err != nil {
		return nil, 0, halt(ExitTool, "READY_UNREADABLE: could not read 'bd ready --json'%s", because(err))
	}
	// A ticket set aside in this run stays out of it, even if bd still lists it as ready (a defer
	// that failed, say); otherwise it would be dispatched again at once, in a new tab each time. A
	// ticket waiting on a question is the exception: bd keeps it out of ready until the question is
	// answered, and then it comes back.
	skip := map[string]bool{}
	for _, id := range o.setAside() {
		skip[id] = !o.isAsked(id)
	}
	for id := range running {
		skip[id] = true
	}
	t, queued := pickNext(ready, skip, func(t Ticket) bool { return o.held(t, running) })
	return t, queued, nil
}

// checkoutUnready returns a reason to stop when the main checkout has uncommitted changes or is
// not on Base, or nil. A fast-forward there moves whichever branch is checked out, so it must be
// Base. held, when set, says what is left for review. The caller holds repoMu.
func (o *Loop) checkoutUnready(held string) *stopReason {
	c := o.cfg
	if o.checkout.DirtyTree(c.Repo) != "" {
		return halt(ExitDirty, "DIRTY_TREE: uncommitted changes in %s; stopping%s. Inspect with: git status", c.Repo, held)
	}
	if o.checkout.CurrentBranch(c.Repo) != c.Base {
		return halt(ExitDirty, "DIRTY_TREE: %s is no longer on %s; stopping%s. Check it out again to continue.", c.Repo, c.Base, held)
	}
	return nil
}

// pickNext returns the first ticket (ready is in priority order) that isn't skipped or held, and
// how many other ready tickets aren't either.
func pickNext(ready []Ticket, skip map[string]bool, held func(Ticket) bool) (*Ticket, int) {
	var free []Ticket
	for _, t := range ready {
		if !skip[t.ID] && !held(t) {
			free = append(free, t)
		}
	}
	if len(free) == 0 {
		return nil, 0
	}
	return &free[0], len(free) - 1
}

// held reports whether a ready ticket must wait for a ticket blocking it, saying why once. Workers
// close their ticket before it merges, and bd ready counts a closed blocker as done, but until the
// blocker merges its code is not on Base, which the ticket's worktree is cut from.
func (o *Loop) held(t Ticket, running map[string]bool) bool {
	why := ""
	if len(running) > 0 || o.anyUnmerged() {
		why = o.waitsFor(t.ID, running)
	}
	o.mu.Lock()
	said := o.holdSaid[t.ID]
	if o.holdSaid == nil {
		o.holdSaid = map[string]string{}
	}
	o.holdSaid[t.ID] = why
	o.mu.Unlock()
	if why != "" && why != said {
		o.info("  %s waits: %s", t.ID, why)
	}
	return why != ""
}

// waitsFor returns why ticket id can't start yet, or "" if nothing blocking it is unmerged.
func (o *Loop) waitsFor(id string, running map[string]bool) string {
	info, err := o.tickets.Show(id)
	if err != nil || info.Status == "unknown" {
		if err != nil {
			o.log.Raw("", err)
		}
		return "its dependencies could not be read"
	}
	for _, d := range info.Dependencies {
		if d.DependencyType != "blocks" {
			continue
		}
		if running[d.ID] {
			return fmt.Sprintf("waiting for %s to merge", d.ID)
		}
		if why := o.unmergedWhy(d.ID); why != "" {
			return fmt.Sprintf("%s closed but not merged (%s)", d.ID, why)
		}
	}
	return ""
}

// earlierRun is why a ticket left unmerged by an earlier run is still unmerged.
const earlierRun = "left unmerged by an earlier run"

// loadUnmerged reads the tickets earlier runs left unmerged (closed, labelled UnmergedLabel), so the
// tickets they block wait in this run too. One that has merged since, by hand, loses its label: its
// branch is on Base with a commit naming it, or its branch is gone and a commit on Base names it.
// It returns a reason to stop when bd can't say which they are.
func (o *Loop) loadUnmerged() *stopReason {
	c := o.cfg
	closed, err := o.tickets.Closed(UnmergedLabel)
	if err != nil {
		return halt(ExitTool, "READY_UNREADABLE: could not list the tickets labelled '%s'%s", UnmergedLabel, because(err))
	}
	for _, t := range closed {
		id, br := t.ID, "wt/"+t.ID
		rev := c.Base
		if o.worktrees.HasBranch(c.Repo, br) {
			rev = br
		}
		if o.merger.IsAncestor(c.Repo, rev, c.Base) {
			if commit := o.merger.CommitNamingOn(c.Repo, rev, id); commit != "" {
				o.info("  %s, left unmerged by an earlier run, is on %s now (%s); its '%s' label is removed", id, c.Base, commit, UnmergedLabel)
				o.unlabel(id)
				continue
			}
		}
		o.info("  %s was left unmerged by an earlier run; tickets it blocks wait until %s is merged into %s or its '%s' label is removed",
			id, br, c.Base, UnmergedLabel)
		o.mu.Lock()
		if o.unmerged == nil {
			o.unmerged = map[string]string{}
		}
		o.unmerged[id] = earlierRun
		o.mu.Unlock()
		o.setLabelled(id, true)
	}
	return nil
}

// leaveUnmerged sets aside a closed ticket that was not merged; tickets it blocks wait for it, in
// this run and, through its UnmergedLabel, in later ones.
func (o *Loop) leaveUnmerged(id, why string) {
	o.markAside(id)
	o.mu.Lock()
	if o.unmerged == nil {
		o.unmerged = map[string]string{}
	}
	o.unmerged[id] = why
	o.mu.Unlock()
	if err := o.notes.AddLabel(id, UnmergedLabel); err != nil {
		o.log.Raw("", err)
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  LABEL_FAILED: bd could not label %s '%s'%s; later runs may start the tickets it blocks before it merges (bd label add %s %s)",
			id, UnmergedLabel, because(err), id, UnmergedLabel)})
		return
	}
	o.setLabelled(id, true)
}

// merged forgets that the ticket was unmerged, and removes its UnmergedLabel if it has one.
func (o *Loop) merged(id string) {
	o.mu.Lock()
	delete(o.unmerged, id)
	labelled := o.labelled[id]
	o.mu.Unlock()
	if labelled {
		o.unlabel(id)
	}
}

// unlabel removes the ticket's UnmergedLabel, warning when bd can't.
func (o *Loop) unlabel(id string) {
	if err := o.notes.RemoveLabel(id, UnmergedLabel); err != nil {
		o.log.Raw("", err)
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  LABEL_FAILED: bd could not remove %s's '%s' label%s; later runs hold the tickets it blocks until it is removed (bd label remove %s %s)",
			id, UnmergedLabel, because(err), id, UnmergedLabel)})
		return
	}
	o.setLabelled(id, false)
}

func (o *Loop) setLabelled(id string, labelled bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.labelled == nil {
		o.labelled = map[string]bool{}
	}
	o.labelled[id] = labelled
}

func (o *Loop) unmergedWhy(id string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.unmerged[id]
}

// setAsked records whether ticket id is set aside waiting on a question.
func (o *Loop) setAsked(id string, asked bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.askedIDs == nil {
		o.askedIDs = map[string]bool{}
	}
	o.askedIDs[id] = asked
}

func (o *Loop) isAsked(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.askedIDs[id]
}

func (o *Loop) anyUnmerged() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.unmerged) > 0
}

// work runs one ticket from worktree to merge. It returns a reason when the run must stop; the
// ticket then stays listed as active (still being worked on).
func (o *Loop) work(ctx context.Context, t Ticket) (stop *stopReason) {
	c := o.cfg
	id, br := t.ID, "wt/"+t.ID
	defer func() {
		if stop == nil {
			o.clearActive(id)
		}
	}()
	o.setAsked(id, false) // back from a question: set aside again, it stays out
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true) // reopened after an earlier run left it unmerged: merging removes the label
	}

	// One worktree per ticket. A ticket that comes back (deferral ended) resumes its old branch.
	wt, s := o.prepareWorktree(id, br)
	if s != nil {
		return s
	}

	// The worker's Herdr name: Herdr takes fewer characters than a ticket ID can hold.
	agent := o.agentName(id)

	// A returning ticket's earlier worker may still sit in its tab under the ticket's name, which
	// Herdr keeps unique. Rename it so the new worker can have the name; its tab stays as it is.
	st, err := o.readStatus(ctx, agent, 5)
	if ctx.Err() != nil {
		return errInterrupted
	}
	switch st {
	case "gone":
	case "unreadable":
		return halt(ExitTool, "HERDR_FAILED: cannot tell whether an earlier worker for %s is still in its tab: %v", id, err)
	case "working", "blocked":
		return halt(ExitTool, "AGENT_BUSY: an earlier worker for %s is still %s in its tab; stopping rather than starting a second one on %s", id, st, wt)
	default:
		if name := o.namer.FreeName(agent); name == "" || o.namer.RenameAgent(agent, name) != nil {
			return halt(ExitTool, "AGENT_NAME_TAKEN: an earlier worker for %s holds its name and could not be renamed", id)
		} else {
			o.info("  earlier worker for %s renamed to %s; its tab is left open", id, name)
		}
	}

	tab, pane, err := o.tabs.CreateTab(c.Workspace, wt, id)
	if err != nil {
		return halt(ExitTool, "TAB_FAILED for %s (is '%s' a valid workspace?)", id, c.Workspace)
	}
	started := time.Now()
	o.setActive(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	o.status(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started, Agent: "starting"})
	defer o.status(Status{Ticket: id, Gone: true})

	// Claude starts with its prompt already submitted, so nothing is pasted into its input box.
	// Herdr can only pass a one-line argument, so the prompt goes in a file the worker reads.
	prompt := strings.ReplaceAll(o.prompt, "TICKET_ID", id)
	launch := ""
	if c.LaunchPrompt && c.AgentKind == "claude" {
		var err error
		if launch, err = project.WriteLaunchPrompt(wt, id, prompt); err != nil {
			o.log.Raw("", fmt.Errorf("cannot write the launch prompt for %s, pasting it instead: %w", id, err))
			launch = ""
		}
	}

	// A Claude worker reports each tool it uses, through hooks loaded for it alone.
	var report []string
	if o.reporter != nil && c.AgentKind == "claude" {
		var err error
		if report, err = o.reporter.ReportArgs(wt); err != nil {
			o.log.Raw("", fmt.Errorf("cannot set up %s's worker to report what it does: %w", id, err))
			report = nil
		}
	}

	// With its prompt in a file, the worker is started by typing the command into the tab and
	// named once Herdr recognises it. Herdr's own start waits for the agent to look ready for
	// input, which a worker that goes straight to work never does, so it could only time out.
	// A name Herdr refuses would be refused on every retry, so that ends the attempt at once.
	nameRefused := func(err error) *stopReason {
		return halt(ExitTool, "START_FAILED for %s in tab %s: Herdr refused the agent name %s (%v)", id, tab, agent, err)
	}
	ok := false
	if launch != "" {
		err := o.starter.LaunchInPane(pane, c.AgentKind, append(report[:len(report):len(report)], launch))
		if err == nil {
			_, err = o.namer.AdoptAgent(ctx, pane, c.AgentKind, agent)
		}
		if ctx.Err() != nil {
			return errInterrupted
		}
		if ok = err == nil; !ok {
			o.log.Raw("", fmt.Errorf("%s's worker could not be started from its prompt file and named %s (%v); starting it with herdr agent start and pasting the prompt", id, agent, err))
			if o.starter.IsNameRefused(err) {
				return nameRefused(err)
			}
			launch = ""
		}
	}

	// 'agent start' can report failure while the agent is still coming up (agent_not_ready keeps
	// the name), and a retry then finds the pane occupied, so after each failure check whether
	// the agent is there before trying again.
	for attempt := 0; attempt < 10 && !ok; attempt++ {
		args := report[:len(report):len(report)]
		if launch != "" {
			args = append(args, launch)
		}
		err := o.starter.StartAgent(ctx, agent, c.AgentKind, pane, args)
		if ok = err == nil; ok {
			break
		}
		o.log.Raw("", err)
		if o.starter.IsNameRefused(err) {
			return nameRefused(err)
		}
		if o.starter.IsArgumentRefused(err) && len(args) > 0 {
			// Start it plainly: paste the prompt instead, and do without reports if need be.
			if launch != "" {
				launch = ""
			} else {
				report = nil
			}
			continue
		}
		if !sleep(ctx, orDefault(o.wait.startRetry, 3*time.Second)) {
			return errInterrupted
		}
		st, err := o.agents.Status(agent)
		if err != nil {
			o.log.Raw("", err)
		}
		if st == "gone" {
			// A start that times out leaves the agent running unnamed in its pane, and a retry
			// would find the pane busy: adopt that agent under the ticket's name instead.
			if name, kind, pst := o.namer.PaneAgent(pane); pst != "gone" && name == "" && kind == c.AgentKind {
				if err := o.namer.RenameAgent(pane, agent); err == nil {
					o.info("  %s's worker started without its name; named it %s", id, agent)
					st = pst
				} else {
					o.log.Raw("", err)
					if o.starter.IsNameRefused(err) {
						return nameRefused(err)
					}
				}
			}
		}
		switch st {
		case "idle", "done":
			ok = true
		case "working", "blocked", "unknown":
			// Up but busy. With the prompt given at launch that means it started; otherwise (a
			// startup dialog, say) give it time rather than starting a second one.
			ok = launch != "" || o.starter.WaitReady(ctx, agent)
		}
	}
	if !ok {
		return halt(ExitTool, "START_FAILED for %s in tab %s", id, tab)
	}

	stopWatch := o.watch(ctx, wt, Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	defer stopWatch()

	if !o.promptTaken(ctx, id, agent, prompt, launch != "") {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.appendNotes(id, fmt.Sprintf("Orchestra: the worker in Herdr tab %s never started on its prompt; deferred so it can be retried (worktree %s).", tab, wt))
		if err := o.deferAside(id, "the worker never started on its prompt"); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s's worker never started on its prompt, and bd could not defer it%s; kept out of this run, worktree %s and tab %s left open",
				id, because(err), wt, tab)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "its worker never started on the prompt", Text: fmt.Sprintf(
			"  PROMPT_FAILED: %s's worker never started on its prompt -> deferred; worktree %s and tab %s left open", id, wt, tab)})
		return nil
	}

	if stop := o.waitSettled(ctx, id, agent, tab); stop != nil {
		return stop
	}
	stopWatch()

	// Beads, not the worker's own report, decides what happened. A ticket blocked on an open
	// question is out of the queue until the maintainer answers; the run goes on without it.
	info, showErr := o.tickets.Show(id)
	if q := OpenQuestion(info); q != nil && info.Status != "closed" {
		o.markAside(id)
		o.setAsked(id, true)
		if info.Status != "open" { // back in the queue once answered
			if err := o.notes.Reopen(id); err != nil {
				o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
					"  REOPEN_FAILED: %s stays %s, so it won't come back once %s is answered%s; reopen it with: bd update %s --status open",
					id, info.Status, q.ID, because(err), id)})
			}
		}
		o.emit(Event{Kind: EvAsked, Ticket: id, Detail: q.ID + ": " + q.Title, Text: fmt.Sprintf(
			"  ASKED: %s waits on your answer to %s (%s); answer with: bd human respond %s; worktree %s and tab %s left open",
			id, q.ID, q.Title, q.ID, wt, tab)})
		return nil
	}
	switch s := info.Status; outcomeOf(s) {
	case outcomeClosed:
		if s := o.finish(ctx, id, br, wt, tab); s != nil {
			return s
		}
	case outcomeDeferred:
		o.markAside(id)
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "by the worker", Text: fmt.Sprintf(
			"  %s deferred by worker; worktree %s and tab %s left open", id, wt, tab)})
		o.queueTriage(ctx, o.gatherDeferral(id, t.Title, "the worker deferred it", wt))
	case outcomePaused:
		// Most likely waiting for an answer: stop rather than start the next ticket around it.
		o.appendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s went idle with the ticket still in_progress (worktree %s).", tab, wt))
		return halt(ExitStuck, "PAUSED: %s still in_progress in tab %s (worktree %s); stopping so it can be answered", id, tab, wt)
	case outcomeUnreadable:
		return halt(ExitTool, "STATUS_UNREADABLE for %s%s; stopping rather than guessing (worktree %s and tab %s left open)", id, because(showErr), wt, tab)
	case outcomeUnfinished:
		o.appendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s settled with the ticket still '%s'; deferred for review (worktree %s).", tab, s, wt))
		if err := o.deferAside(id, fmt.Sprintf("worker finished without closing; see Herdr tab %s and worktree %s", tab, wt)); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s still %s, and bd could not defer it%s; kept out of this run, worktree %s and tab %s left for review",
				id, s, because(err), wt, tab)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "still " + s + ", noted for review", Text: fmt.Sprintf(
			"  %s still %s -> noted and deferred; worktree %s and tab %s left open", id, s, wt, tab)})
		o.queueTriage(ctx, o.gatherDeferral(id, t.Title, fmt.Sprintf("the worker settled with the ticket still '%s', so the orchestrator deferred it", s), wt))
	}
	return nil
}

// finish merges a closed ticket, or leaves it for review without a commit naming it or with
// uncommitted changes.
func (o *Loop) finish(ctx context.Context, id, br, wt, tab string) *stopReason {
	c := o.cfg
	commit := o.merger.CommitNaming(c.Repo, c.Base, br, id)
	switch closedOutcomeOf(commit, o.checkout.DirtyTree(wt) != "") {
	case closedNoCommit:
		o.leaveUnmerged(id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: no commit on %s names %s; worktree %s and tab %s left for review", br, id, wt, tab)})
	case closedDirty:
		o.leaveUnmerged(id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: %s closed (%s) but %s has uncommitted changes; worktree and tab %s left for review", id, commit, wt, tab)})
	case closedMerge:
		return o.merge(ctx, id, br, wt, tab)
	}
	return nil
}

// prepareWorktree creates the ticket's worktree, or reuses the one left by an earlier attempt and
// brings its branch up to date, holding the repository lock.
func (o *Loop) prepareWorktree(id, br string) (string, *stopReason) {
	c := o.cfg
	o.repoMu.Lock()
	defer o.repoMu.Unlock()
	o.worktrees.Prune(c.Repo) // forget a worktree whose folder was deleted, so it isn't reused
	if wt := o.worktrees.WorktreeOf(c.Repo, br); wt != "" {
		o.info("  reusing worktree %s (%s)", wt, br)
		o.refreshBranch(wt, br)
		return wt, nil
	}
	wt := filepath.Join(c.WTRoot, id)
	var out string
	var err error
	if o.worktrees.HasBranch(c.Repo, br) {
		out, err = o.worktrees.AddWorktree(c.Repo, wt, br)
	} else {
		out, err = o.worktrees.NewWorktree(c.Repo, wt, br, c.Base)
	}
	o.log.Raw(out, err)
	if err != nil {
		return "", halt(ExitTool, "WORKTREE_FAILED for %s at %s (git output is in %s)", id, wt, c.LogPath)
	}
	o.info("  worktree %s on %s", wt, br)
	o.refreshBranch(wt, br)
	return wt, nil
}

// merge brings a finished ticket's branch onto Base, one ticket at a time. When other tickets
// merged while it ran, the branch is rebased first and, since the rebased code is untested, the
// project's check command runs again before it merges. A conflict or a failing check leaves the
// ticket for review and the run goes on.
func (o *Loop) merge(ctx context.Context, id, br, wt, tab string) *stopReason {
	c := o.cfg
	o.mergeMu.Lock()
	defer o.mergeMu.Unlock()
	// Only merges move Base during a run, and they queue here; a second pass covers a commit made
	// by hand while the checks ran.
	for attempt := 0; attempt < 3; attempt++ {
		o.repoMu.Lock()
		if o.merger.IsAncestor(c.Repo, c.Base, br) {
			if s := o.checkoutUnready(fmt.Sprintf(" before merging %s; worktree %s and tab %s left for review", br, wt, tab)); s != nil {
				o.repoMu.Unlock()
				o.leaveUnmerged(id, "DIRTY_TREE")
				return s
			}
			commit := o.merger.CommitNaming(c.Repo, c.Base, br, id)
			out, err := o.merger.FastForward(c.Repo, br)
			o.log.Raw(out, err)
			if err != nil {
				o.repoMu.Unlock()
				o.leaveUnmerged(id, "MERGE_FAILED")
				return halt(ExitMerge, "MERGE_FAILED: %s does not fast-forward onto %s; worktree %s and tab %s left for review", br, c.Base, wt, tab)
			}
			o.merged(id)
			out, err = o.worktrees.RemoveWorktree(c.Repo, wt)
			o.log.Raw(out, err)
			if err == nil {
				out, err = o.worktrees.DeleteBranch(c.Repo, br)
				o.log.Raw(out, err)
			}
			o.repoMu.Unlock()
			hash, _, _ := strings.Cut(commit, " ")
			if err == nil {
				o.tabs.CloseTab(tab)
				o.emit(Event{Kind: EvClosed, Ticket: id, Detail: hash + " merged into " + c.Base, Text: fmt.Sprintf(
					"  %s closed (%s); merged into %s, worktree, branch and tab removed", id, commit, c.Base)})
			} else {
				o.emit(Event{Kind: EvClosed, Ticket: id, Detail: hash + " merged; cleanup failed, tab " + tab + " left open", Text: fmt.Sprintf(
					"  %s closed (%s); merged into %s, but CLEANUP_FAILED for %s / %s (git output is in %s); tab %s left open", id, commit, c.Base, wt, br, c.LogPath, tab)})
			}
			return nil
		}

		// Base moved on while the ticket ran: rebase it, still under the lock.
		out, err := o.merger.Rebase(wt, c.Base)
		o.log.Raw(out, err)
		if err != nil {
			o.merger.AbortRebase(wt)
			o.repoMu.Unlock()
			o.leaveUnmerged(id, "MERGE_CONFLICT")
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  MERGE_CONFLICT: %s closed, but %s conflicts with %s, which moved on while it ran; worktree %s and tab %s left for review (rebase onto %s, check, merge)",
				id, br, c.Base, wt, tab, c.Base)})
			return nil
		}
		o.repoMu.Unlock()
		o.info("  rebased %s onto %s, which moved on while it ran", br, c.Base)
		if c.Check == "" {
			o.info("  no check command in .orchestra/settings.json: merging %s without checking the rebased code", br)
			continue
		}
		if err := o.runCheck(ctx, wt); err != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			o.leaveUnmerged(id, "CHECKS_FAILED")
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  CHECKS_FAILED: %s closed, but '%s' fails on %s rebased onto %s; worktree %s and tab %s left for review (output is in %s)",
				id, c.Check, br, c.Base, wt, tab, c.LogPath)})
			return nil
		}
		o.info("  '%s' passes on the rebased %s", c.Check, br)
		// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	}
	o.leaveUnmerged(id, "MERGE_CONFLICT")
	o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
		"  MERGE_CONFLICT: %s closed, but %s kept changing while its checks ran (commits made by hand?); worktree %s and tab %s left for review", id, c.Base, wt, tab)})
	return nil
}

// runCheck runs the project's check command in the worktree, logging the end of its output if it
// fails. It gives up after 30 minutes, stopping everything the check started.
func (o *Loop) runCheck(ctx context.Context, wt string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, err := command.GroupOutput(ctx, 5*time.Second, wt, "sh", "-c", o.cfg.Check)
	if err != nil {
		o.log.Raw(lastLines(string(out), 40), fmt.Errorf("check '%s' in %s: %w", o.cfg.Check, wt, err))
	}
	return err
}

// waitSettled waits until the worker settles. Never answer its prompts; stop if it stays blocked for 4
// minutes. A worker waiting on its own background command looks idle too, so an idle worker
// whose ticket is still in progress gets idleGrace to resume before it counts as settled. A
// status Herdr fails to read says nothing about the worker, so the wait goes on through
// maxFailedReads of them in a row before the run stops.
func (o *Loop) waitSettled(ctx context.Context, id, agent, tab string) *stopReason {
	var blockedSince, idleSince time.Time
	failed := 0
	for {
		if ctx.Err() != nil {
			return errInterrupted
		}
		st, err := o.agents.Status(agent)
		if err != nil {
			if failed++; failed == 1 {
				o.log.Raw("", fmt.Errorf("cannot read the status of %s's worker; still waiting on it: %w", id, err))
			}
			if failed >= maxFailedReads {
				return halt(ExitTool, "HERDR_FAILED: the status of %s's worker (tab %s) could not be read %d times in a row: %v", id, tab, failed, err)
			}
			if !sleep(ctx, o.pollEvery()) {
				return errInterrupted
			}
			continue
		}
		failed = 0
		if st == "gone" {
			return nil
		}
		if st == "idle" || st == "done" {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			ts, err := o.tickets.Status(id)
			if err != nil {
				o.log.Raw("", err)
			}
			if !keepWaiting(ts, time.Since(idleSince), orDefault(o.wait.idleGrace, idleGrace)) {
				return nil
			}
		} else {
			idleSince = time.Time{}
		}
		if st == "blocked" {
			if blockedSince.IsZero() {
				blockedSince = time.Now()
			}
			if time.Since(blockedSince) > orDefault(o.wait.blocked, blockedLimit) {
				return halt(ExitStuck, "BLOCKED >4min: tab %s (%s) needs attention", tab, id)
			}
		} else {
			blockedSince = time.Time{}
		}
		if !sleep(ctx, o.pollEvery()) {
			return errInterrupted
		}
	}
}

// blockedLimit is how long a worker may stay blocked before the run stops for it.
const blockedLimit = 4 * time.Minute

// maxFailedReads is how many failed status reads in a row (a minute's worth) stop the wait on a
// worker.
const maxFailedReads = 20

// agentName is the Herdr name of ticket id's worker; without a Namer (in tests), the ID itself.
func (o *Loop) agentName(id string) string {
	if o.namer == nil {
		return id
	}
	return o.namer.AgentName(id)
}

// readStatus reads the worker's status, trying up to tries times while Herdr fails to answer and
// logging each failure. "unreadable" and the last error if it never answers. agent is the
// worker's Herdr name.
func (o *Loop) readStatus(ctx context.Context, agent string, tries int) (string, error) {
	for try := 1; ; try++ {
		st, err := o.agents.Status(agent)
		if err == nil || try == tries {
			return st, err
		}
		o.log.Raw("", err)
		if !sleep(ctx, o.pollEvery()) {
			return st, err
		}
	}
}

// idleGrace is how long an idle worker whose ticket is still in progress may take to resume
// (typically it is waiting on its own background command) before the run pauses for it.
const idleGrace = 10 * time.Minute

func keepWaiting(ticketStatus string, idleFor, grace time.Duration) bool {
	return ticketStatus == "in_progress" && idleFor < grace
}

// promptTaken confirms the worker started on its prompt. Given at launch, it counts as taken once
// the worker works, has claimed the ticket, or shows any action; otherwise (or if it did not take)
// it is delivered by pasting.
func (o *Loop) promptTaken(ctx context.Context, id, agent, prompt string, atLaunch bool) bool {
	if atLaunch {
		if o.agents.WaitStarted(ctx, agent) || o.claimed(id) || lastActivity(o.agents.Screen(agent)) != "" {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		o.log.Raw("", fmt.Errorf("%s did not start on the prompt given at launch; pasting it", id))
	}
	return o.deliverPrompt(ctx, agent, prompt)
}

// claimed reports whether the worker has taken its ticket out of "open". A status bd can't give
// doesn't count.
func (o *Loop) claimed(id string) bool {
	st, err := o.tickets.Status(id)
	if err != nil {
		o.log.Raw("", err)
		return false
	}
	return st != "open"
}

// appendNotes adds a note to the ticket, logging a failure: a lost note doesn't stop the run.
func (o *Loop) appendNotes(id, note string) {
	if err := o.notes.AppendNotes(id, note); err != nil {
		o.log.Raw("", err)
	}
}

// deferAside defers the ticket and keeps it out of the rest of the run, which matters most when
// bd fails to defer it: it would still be ready and dispatched again at once. The error is logged
// and returned so the caller can warn.
func (o *Loop) deferAside(id, reason string) error {
	o.markAside(id)
	err := o.notes.Defer(id, reason)
	if err != nil {
		o.log.Raw("", err)
	}
	return err
}

// because renders a tracker error for a log line, with bd's stderr on one line: ": <cause>", or
// "" without an error.
func because(err error) string {
	if err == nil {
		return ""
	}
	return ": " + strings.Join(strings.Fields(err.Error()), " ")
}

// refreshBranch rebases a returning ticket's branch onto Base, which has moved on since the branch
// was cut; otherwise its merge could not fast-forward. A failed rebase is undone and reported.
func (o *Loop) refreshBranch(wt, br string) {
	c := o.cfg
	if o.merger.IsAncestor(c.Repo, c.Base, br) {
		return // already on top of Base
	}
	if d := o.checkout.DirtyTree(wt); d != "" {
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  REBASE_SKIPPED: %s has uncommitted changes, so %s stays behind %s; its merge will fail until it is rebased", wt, br, c.Base)})
		return
	}
	out, err := o.merger.Rebase(wt, c.Base)
	o.log.Raw(out, err)
	if err != nil {
		o.merger.AbortRebase(wt)
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  REBASE_FAILED: %s conflicts with %s; left as it was, so its merge will fail until it is rebased (worktree %s)", br, c.Base, wt)})
		return
	}
	o.info("  rebased %s onto %s", br, c.Base)
}

// deliverPrompt sends the worker its prompt and confirms it started on it. 'herdr agent prompt'
// can paste the text without the Enter registering, leaving the worker idle with the prompt in
// its input box; the worker then looks settled and its ticket would be deferred untouched.
// Enter is pressed only when the box visibly holds the prompt (never on a dialog, where it would
// pick an option), and the prompt is sent again only when the box is empty, so never twice.
func (o *Loop) deliverPrompt(ctx context.Context, agent, prompt string) bool {
	for attempt := 1; attempt <= 2; attempt++ {
		err := o.agents.Prompt(ctx, agent, prompt)
		if err == nil {
			return true // herdr saw the worker start
		}
		o.log.Raw("", err)
		if ctx.Err() != nil {
			return false
		}
		st, _ := o.readStatus(ctx, agent, 5)
		switch st {
		case "working", "blocked":
			return true // it started; a block is handled by the settle loop
		case "idle", "done":
			if inputHolds(o.agents.Screen(agent), prompt) {
				o.agents.SendKeys(agent, "enter")
				return o.agents.WaitStarted(ctx, agent)
			}
			// The box is empty: the paste itself was lost, so send it again.
		default:
			return false
		}
	}
	return false
}

// watch reports the worker's status and latest action every 2 seconds until the returned stop
// function is called; stop waits for the last report, so no update lands after it.
func (o *Loop) watch(ctx context.Context, wt string, base Status) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	agent := o.agentName(base.Ticket)
	go func() {
		defer close(done)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			s := base
			s.Agent, _ = o.agents.Status(agent) // the settle loop logs failures
			s.Activity = lastActivity(o.agents.Screen(agent))
			if o.reporter != nil && s.Agent == "working" {
				if u, ok := o.reporter.LastToolUse(wt); ok {
					s.Doing = Doing(u, o.cfg.Check)
				}
			}
			if ctx.Err() != nil {
				return
			}
			o.status(s)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}

// ---- Decisions -----------------------------------------------------------------------

type outcome int

const (
	outcomeClosed outcome = iota
	outcomeDeferred
	outcomePaused
	outcomeUnreadable
	outcomeUnfinished // any other status: note it and defer for review
)

func outcomeOf(status string) outcome {
	switch status {
	case "closed":
		return outcomeClosed
	case "deferred":
		return outcomeDeferred
	case "in_progress":
		return outcomePaused
	case "unknown":
		return outcomeUnreadable
	}
	return outcomeUnfinished
}

type closedOutcome int

const (
	closedMerge closedOutcome = iota
	closedNoCommit
	closedDirty
)

// A closed ticket is merged only with a commit naming it and a clean worktree.
func closedOutcomeOf(commit string, worktreeDirty bool) closedOutcome {
	switch {
	case commit == "":
		return closedNoCommit
	case worktreeDirty:
		return closedDirty
	}
	return closedMerge
}

// Config is what a run needs to know.
type Config struct {
	Repo         string
	Base         string // branch finished tickets are merged into
	Workspace    string // Herdr workspace for the worker tabs
	Limit        int
	DoneSoFar    int
	AgentKind    string
	WTRoot       string
	LogPath      string
	ReportsDir   string
	LaunchPrompt bool   // give Claude workers their prompt at launch instead of pasting it
	Concurrency  int    // tickets worked on at the same time
	Check        string // the project's check command, from .orchestra/settings.json
	Version      string // orchestra's version, for the log
}
