// Package dispatch is the orchestra loop: it hands ready tickets to workers, up to a number at a
// time, watches them settle, merges finished tickets one at a time and sets the rest aside.
package dispatch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Exit codes, unchanged from orchestrate.sh.
const (
	// nothing left in bd ready, LIMIT reached, or it stopped after the running tickets as asked; a
	// scoped run says whether its scope is done
	ExitOK    = 0
	ExitSetup = 2 // setup problem found before starting
	// a worker stayed blocked or unknown too long, went idle with its ticket in_progress, or ran past
	// the ticket limit
	ExitStuck       = 3
	ExitTool        = 4   // Herdr, Beads or git failure, or a worker panicked
	ExitDirty       = 5   // uncommitted changes in the main checkout, or it left its branch
	ExitMerge       = 6   // a finished ticket's branch does not fast-forward
	ExitEnvironment = 7   // workers kept failing at once, whichever ticket they had: the machine, not the tickets
	ExitInterrupted = 130 // stopped with Ctrl+C
)

// Loop is one orchestra run: it picks ready tickets, hands them to workers and merges them.
type Loop struct {
	cfg    Config
	log    *Log
	sink   Sink
	sinkMu sync.Mutex // triage reports from its own goroutine
	prompt string     // worker prompt with the TICKET_ID placeholder
	count  int
	queued int // the queue size last reported, -1 before the first; Run's own

	// Tickets labelled SoloLabel, Run's own: the one running, the one next in line waiting for the
	// running tickets to finish, the state last reported and the wait last logged.
	solo      string
	soloNext  string
	soloShown SoloState
	soloSaid  string

	started   time.Time
	startHead string // Base's commit when the run started; the reviewer reads commits since
	final     string // the stop or done line
	// closedN and asideN: the tickets merged and set aside in the run, counted as their events go
	// out, under sinkMu (see tally).
	closedN, asideN int

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
	asideIDs  []string              // tickets deferred or left unmerged in this run
	unmerged  map[string]string     // tickets closed but left unmerged, in this run or an earlier one, with why
	checkSaid map[string]checkFail  // what each ticket's last check in this run said, if it failed
	labelled  map[string]bool       // tickets carrying UnmergedLabel, which a merge removes
	holdSaid  map[string]string     // why each held ticket waits, as last said
	blockers  map[string]blockLinks // each ready ticket's blockers, read once per run
	// askedIDs: tickets set aside in this run to wait on a question, and where their workers were left;
	// also those the last run left behind, carried over (see loadCarried).
	askedIDs map[string]askedWorker
	// placed: where each worker started (from when its tab is open) or adopted in this run is, and,
	// once it stopped the run, why: the next run carries on with those left running (see saveCarried).
	placed map[string]askedWorker
	// keptOut: the workers the last run left behind on tickets outside this run's scope, saved again
	// for a later run as they were.
	keptOut   []project.LeftWorker
	parentOf  map[string]string // the parent of each ticket dispatched or left unmerged, which waits for it
	doneSaid  map[string]bool   // parents said to be ready to close
	finishing map[string]string // what each worker is doing that Ctrl+C doesn't stop: finishMerge, say

	// Scheduling by footprint. The running tickets' footprints, under mu; the repository's files,
	// the reason each ready ticket was last skipped and the shared edits warned about, Run's own.
	footprints map[string]*runFootprint
	files      *repoFiles
	skipSaid   map[string]string
	warned     map[string]bool

	// Predicting footprints, under predictMu: whether the predictor runs, its queue, the tickets
	// queued in this run and the predictions made. predictWake (buffered 1) says one was queued.
	predictMu   sync.Mutex
	predictOn   bool
	predictQ    []Ticket
	predictSeen map[string]bool
	predicted   map[string][]string
	predictWake chan struct{}

	// Triage's queue, set by StartTriage before any worker runs. Workers send to triageQ until
	// FinishTriage calls triageFinish, cancelling triageStop; triageDone closes when the triage
	// goroutine returns.
	triageQ      chan queuedDeferral
	triageStop   context.Context
	triageFinish context.CancelFunc
	triageDone   chan struct{}

	// Holding for the environment, Run's own: the tickets whose workers failed at once in a row, the
	// tickets triage blamed on the environment in a row, the reason once the run holds, with why,
	// and whether the machine was probed in this run. Workers say how they settled on their result;
	// triage hands its verdicts over on verdicts until Run has returned (runDone), then counts them
	// itself, as nothing else does by then. envGen counts the holds a probe has ended: each deferral
	// is stamped with it as it is queued for triage, and its verdict counts toward the hold only
	// while envGen is unchanged. Only Run changes it, but workers read it as they defer: atomic.
	fastFails   []string
	envVerdicts []string
	envStop     *stopReason
	envWhy      string
	envProbed   bool
	envGen      atomic.Uint64
	verdicts    chan verdict
	runDone     chan struct{}

	// Winding down: the maintainer's latest request to stop after the running tickets, or to take
	// tickets again, which Run hasn't heard yet (buffered 1, a newer request replacing it). Whether
	// the run winds down is Run's own.
	drainReqs chan drainRequest

	// poll is how long between reads of a worker's status: statusPoll when zero. Tests on the real
	// clock, with real git, shorten it; the others run on the real durations in a synctest bubble.
	poll time.Duration

	// ReportInterrupt logs the INTERRUPTED line from the loop itself; in the terminal UI the
	// command does it (it knows what stopped the run: Ctrl+C, a signal or the dashboard failing).
	ReportInterrupt bool
}

// New sets up a run: the worker prompt (with TICKET_ID), and its connections.
func New(cfg Config, log *Log, prompt string, d Deps) *Loop {
	return &Loop{
		cfg: cfg, log: log, prompt: prompt,
		tickets: d.Tickets, notes: d.Notes, tabs: d.Tabs, starter: d.Starter, namer: d.Namer, agents: d.Agents,
		reporter: d.Reporter, checkout: d.Checkout, worktrees: d.Worktrees, merger: d.Merger, history: d.History,
		organ: d.Advisor, organCtx: d.AdviceCtx,
		verdicts: make(chan verdict), runDone: make(chan struct{}), drainReqs: make(chan drainRequest, 1),
	}
}

// Final is the line the run ended with.
func (o *Loop) Final() string {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	return o.final
}

// stopReason ends the run: a ticket hit something that needs the maintainer, or a tool failed.
// With several workers, no new tickets start and the running ones finish first. Its kind says
// what stopped the run and its cause, if any, what failed; its text is the line the run ends
// with, which begins with the kind.
type stopReason struct {
	code   int
	kind   stopKind
	detail string // what follows the kind on the line
	cause  error
	ticket string // the ticket it stopped the run over, if any: its notification names it
}

// stopKind is what stopped the run, as the first word of its line says.
type stopKind string

const (
	stopAgentBusy        stopKind = "AGENT_BUSY"
	stopAgentNameTaken   stopKind = "AGENT_NAME_TAKEN"
	stopBlocked          stopKind = "BLOCKED"
	stopDirtyTree        stopKind = "DIRTY_TREE"
	stopEnvironment      stopKind = "ENVIRONMENT"
	stopGitFailed        stopKind = "GIT_FAILED"
	stopHerdrFailed      stopKind = "HERDR_FAILED"
	stopInterrupted      stopKind = "INTERRUPTED"
	stopMergeFailed      stopKind = "MERGE_FAILED"
	stopPanic            stopKind = "PANIC"
	stopPaused           stopKind = "PAUSED"
	stopReadyUnreadable  stopKind = "READY_UNREADABLE"
	stopStartFailed      stopKind = "START_FAILED"
	stopStatusUnreadable stopKind = "STATUS_UNREADABLE"
	stopTabFailed        stopKind = "TAB_FAILED"
	stopTicketLimit      stopKind = "TICKET_LIMIT"
	stopUnknown          stopKind = "UNKNOWN"
	stopWorktreeFailed   stopKind = "WORKTREE_FAILED"
)

// errInterrupted is returned by a worker when Ctrl+C cancelled the run.
var errInterrupted = &stopReason{code: ExitInterrupted, kind: stopInterrupted}

// Interrupted is an EvStop's Detail when Ctrl+C or a signal stopped the run.
const Interrupted = string(stopInterrupted)

// halt is a reason to stop of the given kind, ending the run with code. Its line is the kind
// followed by format, which begins with what separates them (": ", " for ").
func halt(code int, kind stopKind, format string, a ...any) *stopReason {
	return &stopReason{code: code, kind: kind, detail: fmt.Sprintf(format, a...)}
}

// causedBy keeps err as what made s stop the run, for errors.Is and errors.As; its line names
// err already, if it should.
func (s *stopReason) causedBy(err error) *stopReason {
	s.cause = err
	return s
}

// over says ticket id is what s stopped the run over, unless s names one already.
func (s *stopReason) over(id string) *stopReason {
	if s.ticket == "" {
		s.ticket = id
	}
	return s
}

// Error is the line the run ends with.
func (s *stopReason) Error() string { return string(s.kind) + s.detail }

// Unwrap is what failed, or nil.
func (s *stopReason) Unwrap() error { return s.cause }

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

// titleOf is the title of ticket id, as its Status has it while it is worked on; "" once it isn't.
func (o *Loop) titleOf(id string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.active[id].Title
}

// What a worker does that Ctrl+C doesn't stop, once begun.
const (
	finishMerge    = "merge"          // rebasing, merging and the notes on it
	finishWorktree = "worktree setup" // making or reusing the ticket's worktree
)

// Finishing is what a ticket's worker is doing that a stop doesn't cut short: What is "merge" or
// "worktree setup".
type Finishing struct {
	Ticket string
	What   string
}

// markFinishing marks what ticket id's worker is doing that Ctrl+C doesn't stop, for the wait
// after it to name; the function it returns clears the mark.
func (o *Loop) markFinishing(id, what string) func() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finishing == nil {
		o.finishing = map[string]string{}
	}
	o.finishing[id] = what
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		delete(o.finishing, id)
	}
}

// activeList returns the tickets being worked on, oldest first; those started at the same time by
// ticket, so the lines, state.json and the review that list them don't follow map order (inside a
// synctest bubble the tickets started in one step share their start time).
func (o *Loop) activeList() []Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	var l []Status
	for _, st := range o.active {
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

// Running returns the tickets being worked on, oldest first and those started at the same time by
// ticket: after an interrupt, those whose workers were left running.
func (o *Loop) Running() []Status { return o.activeList() }

// Finishing returns what the workers are doing that a stop doesn't cut short, by ticket: after an
// interrupt, what the run still waits for before it ends.
func (o *Loop) Finishing() []Finishing {
	o.mu.Lock()
	defer o.mu.Unlock()
	var l []Finishing
	for id, what := range o.finishing {
		l = append(l, Finishing{Ticket: id, What: what})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Ticket < l[j].Ticket })
	return l
}

// orDefault is d, or def if d is zero.
func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// statusPoll is how often a worker's status is read while waiting on it.
const statusPoll = 3 * time.Second

// pollEvery is how often a worker's status is read while waiting on it: statusPoll, unless a test
// shortens it.
func (o *Loop) pollEvery() time.Duration {
	return orDefault(o.poll, statusPoll)
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

// because renders a tracker error for a log line, with bd's stderr on one line: ": <cause>", or
// "" without an error.
func because(err error) string {
	if err == nil {
		return ""
	}
	return ": " + strings.Join(strings.Fields(err.Error()), " ")
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
	LaunchPrompt bool          // give Claude workers their prompt at launch instead of pasting it
	Concurrency  int           // tickets worked on at the same time
	TicketLimit  time.Duration // a worker still going this long after dispatch stops the run; 0 for none
	Check        string        // the project's check command, from .orchestra/settings.json
	CheckTimeout time.Duration // how long Check may run before it is stopped; 0 for project.DefaultCheckTimeout
	Version      string        // orchestra's version, for the log
	NoFootprint  bool          // start tickets side by side even when their footprints overlap
	Predict      bool          // have the predictor organ guess the files of ready tickets naming none
	Ticket       string        // the run's scope: only this ticket and its descendants; "" for all of bd ready
	Feature      string        // the feature request (--feature) Ticket, its epic, was planned from; "" for none
	ExcludeTypes []string      // issue types never dispatched, such as epics, from .orchestra/settings.json
	// EnvHoldCount tickets in a row whose workers failed at once, or that triage blamed on the
	// environment with high confidence, hold the run; 0 turns it off.
	EnvHoldCount int
	// EnvHoldWindow is how soon after dispatch a worker that settles with its ticket unclaimed and
	// unchanged counts as failing at once.
	EnvHoldWindow time.Duration
	// ResolveConflicts hands a finished ticket whose branch conflicts with Base back to its worker
	// to resolve the rebase, rather than setting it aside at once. It needs Check.
	ResolveConflicts bool
	// ResolveTimeout is how long the worker may take to resolve it; 0 for DefaultResolveTimeout.
	ResolveTimeout time.Duration
	// WorkerArgs start every Claude worker, before its own arguments: --no-chrome or --chrome, and
	// --effort when the project sets one.
	WorkerArgs []string
	// EnvProbe is how long after the run holds for the environment, once no ticket runs, one
	// worker without a ticket is started to see whether commands run again; 0 for none.
	EnvProbe time.Duration
	// MCP is the MCP servers Claude workers get and no others: those mcp_servers in
	// .orchestra/settings.json names, with their definitions from this machine's Claude Code config.
	// nil when the project hasn't chosen: workers then load every server Claude Code finds.
	MCP *[]mcp.Server
}

// ClaudeWorkers reports whether the workers are Claude Code agents (AgentKind claude), which alone
// take Claude's arguments, hooks and MCP servers.
func (c Config) ClaudeWorkers() bool {
	return c.AgentKind == "claude"
}
