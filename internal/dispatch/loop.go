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
	EvHold                 // a ticket stopped the run; no new tickets while the running ones finish
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
	asideIDs  []string // tickets deferred or left unmerged in this run

	// Triage's queue. Workers add to it until FinishTriage closes it; a worker still settling
	// after that finds it closed rather than a closed channel.
	triageMu     sync.Mutex
	triageOn     bool // StartTriage was called
	triageClosed bool // FinishTriage was called
	triageQ      []organ.Deferral
	triageWake   chan struct{} // buffered 1: something was queued, or the queue closed
	triageDone   chan struct{}

	// settleWait bounds how long Run waits after Ctrl+C for its workers to return; 0 means
	// SettleWait.
	settleWait time.Duration

	// ReportInterrupt logs Ctrl+C from the loop itself; in the terminal UI the command does it (it
	// knows which tabs were running).
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

func (o *Loop) interrupted() int {
	if !o.ReportInterrupt {
		return ExitInterrupted
	}
	o.emit(Event{Kind: EvStop, Text: "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree"})
	return ExitInterrupted
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
	wait := o.settleWait
	if wait == 0 {
		wait = SettleWait
	}
	timeout := time.NewTimer(wait)
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
		return nil, 0, halt(ExitTool, "READY_UNREADABLE: could not parse 'bd ready --json'")
	}
	t, queued := pickNext(ready, running)
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

// pickNext returns the first ticket (ready is in priority order) that isn't running, and how many
// other ready tickets aren't running.
func pickNext(ready []Ticket, running map[string]bool) (*Ticket, int) {
	var free []Ticket
	for _, t := range ready {
		if !running[t.ID] {
			free = append(free, t)
		}
	}
	if len(free) == 0 {
		return nil, 0
	}
	return &free[0], len(free) - 1
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

	// One worktree per ticket. A ticket that comes back (deferral ended) resumes its old branch.
	wt, s := o.prepareWorktree(id, br)
	if s != nil {
		return s
	}

	// A returning ticket's earlier worker may still sit in its tab under the ticket's name, which
	// Herdr keeps unique. Rename it so the new worker can have the name; its tab stays as it is.
	switch st := o.agents.Status(id); st {
	case "gone":
	case "working", "blocked":
		return halt(ExitTool, "AGENT_BUSY: an earlier worker for %s is still %s in its tab; stopping rather than starting a second one on %s", id, st, wt)
	default:
		if name := o.namer.FreeName(id); name == "" || o.namer.RenameAgent(id, name) != nil {
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
	ok := false
	if launch != "" {
		err := o.starter.LaunchInPane(pane, c.AgentKind, append(report[:len(report):len(report)], launch))
		if err == nil {
			_, ok = o.namer.AdoptAgent(ctx, pane, c.AgentKind, id)
		}
		if ctx.Err() != nil {
			return errInterrupted
		}
		if !ok {
			o.log.Raw("", fmt.Errorf("%s's worker was not recognised after starting it from its prompt file (%v); starting it with herdr agent start and pasting the prompt", id, err))
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
		err := o.starter.StartAgent(ctx, id, c.AgentKind, pane, args)
		if ok = err == nil; ok {
			break
		}
		o.log.Raw("", err)
		if o.starter.IsArgumentRefused(err) && len(args) > 0 {
			// Start it plainly: paste the prompt instead, and do without reports if need be.
			if launch != "" {
				launch = ""
			} else {
				report = nil
			}
			continue
		}
		if !sleep(ctx, 3*time.Second) {
			return errInterrupted
		}
		st := o.agents.Status(id)
		if st == "gone" {
			// A start that times out leaves the agent running unnamed in its pane, and a retry
			// would find the pane busy: adopt that agent under the ticket's name instead.
			if name, kind, pst := o.namer.PaneAgent(pane); pst != "gone" && name == "" && kind == c.AgentKind {
				if o.namer.RenameAgent(pane, id) == nil {
					o.info("  %s's worker started without its name; named it", id)
					st = pst
				}
			}
		}
		switch st {
		case "idle", "done":
			ok = true
		case "working", "blocked", "unknown":
			// Up but busy. With the prompt given at launch that means it started; otherwise (a
			// startup dialog, say) give it time rather than starting a second one.
			ok = launch != "" || o.starter.WaitReady(ctx, id)
		}
	}
	if !ok {
		return halt(ExitTool, "START_FAILED for %s in tab %s", id, tab)
	}

	stopWatch := o.watch(ctx, wt, Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	defer stopWatch()

	if !o.promptTaken(ctx, id, prompt, launch != "") {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.notes.AppendNotes(id, fmt.Sprintf("Orchestra: the worker in Herdr tab %s never started on its prompt; deferred so it can be retried (worktree %s).", tab, wt))
		o.notes.Defer(id, "the worker never started on its prompt")
		o.markAside(id)
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "its worker never started on the prompt", Text: fmt.Sprintf(
			"  PROMPT_FAILED: %s's worker never started on its prompt -> deferred; worktree %s and tab %s left open", id, wt, tab)})
		return nil
	}

	// Wait until the worker settles. Never answer its prompts; stop if it stays blocked for 4
	// minutes. A worker waiting on its own background command looks idle too, so an idle worker
	// whose ticket is still in progress gets idleGrace to resume before it counts as settled.
	var blockedSince, idleSince time.Time
	for {
		if ctx.Err() != nil {
			return errInterrupted
		}
		st := o.agents.Status(id)
		if st == "gone" {
			break
		}
		if st == "idle" || st == "done" {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			if !keepWaiting(o.tickets.Status(id), time.Since(idleSince)) {
				break
			}
		} else {
			idleSince = time.Time{}
		}
		if st == "blocked" {
			if blockedSince.IsZero() {
				blockedSince = time.Now()
			}
			if time.Since(blockedSince) > 4*time.Minute {
				return halt(ExitStuck, "BLOCKED >4min: tab %s (%s) needs attention", tab, id)
			}
		} else {
			blockedSince = time.Time{}
		}
		if !sleep(ctx, 3*time.Second) {
			return errInterrupted
		}
	}
	stopWatch()

	// Beads, not the worker's own report, decides what happened. A ticket blocked on an open
	// question is out of the queue until the maintainer answers; the run goes on without it.
	info := o.tickets.Show(id)
	if q := OpenQuestion(info); q != nil && info.Status != "closed" {
		if info.Status != "open" {
			o.notes.Reopen(id) // back in the queue once answered
		}
		o.markAside(id)
		o.emit(Event{Kind: EvAsked, Ticket: id, Detail: q.ID + ": " + q.Title, Text: fmt.Sprintf(
			"  ASKED: %s waits on your answer to %s (%s); answer with: bd human respond %s; worktree %s and tab %s left open",
			id, q.ID, q.Title, q.ID, wt, tab)})
		return nil
	}
	switch s := info.Status; outcomeOf(s) {
	case outcomeClosed:
		commit := o.merger.CommitNaming(c.Repo, c.Base, br, id)
		switch closedOutcomeOf(commit, o.checkout.DirtyTree(wt) != "") {
		case closedNoCommit:
			o.markAside(id)
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  CLOSED_WITHOUT_COMMIT: no commit on %s names %s; worktree %s and tab %s left for review", br, id, wt, tab)})
		case closedDirty:
			o.markAside(id)
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  CLOSED_WITHOUT_COMMIT: %s closed (%s) but %s has uncommitted changes; worktree and tab %s left for review", id, commit, wt, tab)})
		case closedMerge:
			if s := o.merge(ctx, id, br, wt, tab); s != nil {
				return s
			}
		}
	case outcomeDeferred:
		o.markAside(id)
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "by the worker", Text: fmt.Sprintf(
			"  %s deferred by worker; worktree %s and tab %s left open", id, wt, tab)})
		o.queueTriage(ctx, o.gatherDeferral(id, t.Title, "the worker deferred it", wt))
	case outcomePaused:
		// Most likely waiting for an answer: stop rather than start the next ticket around it.
		o.notes.AppendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s went idle with the ticket still in_progress (worktree %s).", tab, wt))
		return halt(ExitStuck, "PAUSED: %s still in_progress in tab %s (worktree %s); stopping so it can be answered", id, tab, wt)
	case outcomeUnreadable:
		return halt(ExitTool, "STATUS_UNREADABLE for %s; stopping rather than guessing (worktree %s and tab %s left open)", id, wt, tab)
	case outcomeUnfinished:
		o.notes.AppendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s settled with the ticket still '%s'; deferred for review (worktree %s).", tab, s, wt))
		o.notes.Defer(id, fmt.Sprintf("worker finished without closing; see Herdr tab %s and worktree %s", tab, wt))
		o.markAside(id)
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "still " + s + ", noted for review", Text: fmt.Sprintf(
			"  %s still %s -> noted and deferred; worktree %s and tab %s left open", id, s, wt, tab)})
		o.queueTriage(ctx, o.gatherDeferral(id, t.Title, fmt.Sprintf("the worker settled with the ticket still '%s', so the orchestrator deferred it", s), wt))
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
				return s
			}
			commit := o.merger.CommitNaming(c.Repo, c.Base, br, id)
			out, err := o.merger.FastForward(c.Repo, br)
			o.log.Raw(out, err)
			if err != nil {
				o.repoMu.Unlock()
				return halt(ExitMerge, "MERGE_FAILED: %s does not fast-forward onto %s; worktree %s and tab %s left for review", br, c.Base, wt, tab)
			}
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
			o.markAside(id)
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
			o.markAside(id)
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  CHECKS_FAILED: %s closed, but '%s' fails on %s rebased onto %s; worktree %s and tab %s left for review (output is in %s)",
				id, c.Check, br, c.Base, wt, tab, c.LogPath)})
			return nil
		}
		o.info("  '%s' passes on the rebased %s", c.Check, br)
		// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	}
	o.markAside(id)
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

// idleGrace is how long an idle worker whose ticket is still in progress may take to resume
// (typically it is waiting on its own background command) before the run pauses for it.
const idleGrace = 10 * time.Minute

func keepWaiting(ticketStatus string, idleFor time.Duration) bool {
	return ticketStatus == "in_progress" && idleFor < idleGrace
}

// promptTaken confirms the worker started on its prompt. Given at launch, it counts as taken once
// the worker works, has claimed the ticket, or shows any action; otherwise (or if it did not take)
// it is delivered by pasting.
func (o *Loop) promptTaken(ctx context.Context, id, prompt string, atLaunch bool) bool {
	if atLaunch {
		if o.agents.WaitStarted(ctx, id) || o.tickets.Status(id) != "open" || lastActivity(o.agents.Screen(id)) != "" {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		o.log.Raw("", fmt.Errorf("%s did not start on the prompt given at launch; pasting it", id))
	}
	return o.deliverPrompt(ctx, id, prompt)
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
func (o *Loop) deliverPrompt(ctx context.Context, id, prompt string) bool {
	for attempt := 1; attempt <= 2; attempt++ {
		err := o.agents.Prompt(ctx, id, prompt)
		if err == nil {
			return true // herdr saw the worker start
		}
		o.log.Raw("", err)
		if ctx.Err() != nil {
			return false
		}
		switch o.agents.Status(id) {
		case "working", "blocked":
			return true // it started; a block is handled by the settle loop
		case "idle", "done":
			if inputHolds(o.agents.Screen(id), prompt) {
				o.agents.SendKeys(id, "enter")
				return o.agents.WaitStarted(ctx, id)
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
	go func() {
		defer close(done)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			s := base
			s.Agent = o.agents.Status(s.Ticket)
			s.Activity = lastActivity(o.agents.Screen(s.Ticket))
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
