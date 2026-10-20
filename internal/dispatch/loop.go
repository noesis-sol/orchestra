// Package dispatch is the orchestra loop: it hands ready tickets to workers, up to a number at a
// time, watches them settle, merges finished tickets one at a time and sets the rest aside.
package dispatch

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noesis-sol/orchestra/internal/load"
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
	ExitTool        = 4   // terminal (Herdr, tmux or cmux), Beads or git failure, or a worker panicked
	ExitDirty       = 5   // uncommitted changes in the main checkout, or it left its branch
	ExitMerge       = 6   // a finished ticket's branch does not fast-forward
	ExitEnvironment = 7   // workers kept failing at once, whichever ticket they had: the machine, not the tickets
	ExitUsageLimit  = 8   // workers reached the account's usage limit, and it held longer than Config.UsageLimitWait
	ExitInterrupted = 130 // stopped with Ctrl+C
)

// Loop is one orchestra run: it picks ready tickets, hands them to workers and merges them. Its state
// is grouped by owner: each embedded struct says which lock guards its fields, or whose they are.
// What only Run itself reads and writes is runState's.
type Loop struct {
	cfg    Config
	log    *Log
	prompt string // worker prompt with the TICKET_ID placeholder

	deps                   // the connections, set by New and never changed
	tallies                // the sink and what the events sent to it counted, under sinkMu
	ledger                 // what the workers, Run and the merges share, under mu
	book        ticketBook // each ticket's place in the run (set aside, asked, left open, …), under its own lock
	carryover              // the workers carried over from the last run and left for the next, under leftMu
	predictor              // predicting footprints, under predictMu
	triageQueue            // triage's queue, set by StartTriage before any worker runs
	envHold                // holding for the environment: Run's own, and triage's once Run has returned
	usage       *usageHold // holding for the account's usage limit, under its own lock
	gate        gate       // why the run takes no new tickets, under its own lock: Run's to set, the merges' to read

	started   time.Time
	startHead string // Base's commit when the run started; the reviewer reads commits since
	// permission is --permission-mode and Config.WorkerPermissionMode, which every Claude worker
	// starts with when its claude takes the mode, as the run found as it began; nil for none.
	permission []string
	// unusedTools is the --disallowedTools that keeps the tools Claude workers never use out of
	// their requests, when their claude takes it, as the run found as it began; nil for none.
	unusedTools []string

	// repoMu serialises git writes to the main repository (worktrees, rebases, merges, branch
	// deletions): workers run side by side, and git's lock files allow one writer at a time.
	repoMu sync.Mutex
	merges mergeQueue // the merge queue (see mergeTurn)

	organCost organCost // what the run's organ calls cost
	hookTally hookTally // what the workers' hooks noted: permission prompts, compactions
	holdSaid  sayOnce   // why each held ticket waits, as last said
	doneSaid  sayOnce   // parents said to be ready to close

	// branches: closed tickets' branches, as git found them (see branchWhy)
	branches closedBranches

	// Winding down: the maintainer's latest request to stop after the running tickets, or to take
	// tickets again, which Run hasn't heard yet (buffered 1, a newer request replacing it). Whether
	// the run winds down is the gate's, as Run last heard (see runState.drained).
	drainReqs chan drainRequest

	// poll is how long between reads of a worker's status: statusPoll when zero. Tests on the real
	// clock, with real git, shorten it; the others run on the real durations in a synctest bubble.
	poll time.Duration
	// process reports whether the process of a run registered on the machine runs and when it started,
	// the zero time when unknown (see machine.gone); runningProcess when nil. Tests give their own.
	process func(pid int) (start time.Time, alive bool)
	// sampler reads the machine's load for the dashboard (see watchLoad): load.NewSampler of orchestra's
	// own process when nil. Tests give their own.
	sampler *load.Sampler

	// ReportInterrupt logs the INTERRUPTED line from the loop itself; in the terminal UI the
	// command does it (it knows what stopped the run: Ctrl+C, a signal or the dashboard failing).
	ReportInterrupt bool
}

// deps are the loop's connections to Beads, the terminal, git, the workers' reports and the organs.
type deps struct {
	tickets   Tickets
	notes     Notes
	tabs      Tabs
	starter   Starter
	namer     Namer
	agents    Agents
	processes PaneProcesses
	reporter  Reporter
	help      AgentHelp
	checkout  Checkout
	worktrees Worktrees
	merger    Merger
	history   History
	organ     organ.Client
	organCtx  context.Context // cancelled when the maintainer skips the organs
}

// depsFrom are the connections d gives.
func depsFrom(d Deps) deps {
	return deps{
		tickets: d.Tickets, notes: d.Notes, tabs: d.Tabs, starter: d.Starter, namer: d.Namer, agents: d.Agents,
		processes: d.Processes, reporter: d.Reporter, help: d.Help, checkout: d.Checkout, worktrees: d.Worktrees,
		merger: d.Merger, history: d.History, organ: d.Advisor, organCtx: d.AdviceCtx,
	}
}

// tallies is where the run's events go and what they counted, all under sinkMu: workers and triage
// emit from goroutines of their own.
type tallies struct {
	// emitMu is held over the whole of an emit, so the log, the event stream and the sink each get the
	// events in one order, whichever goroutines emit them. It is taken before sinkMu and the log's.
	emitMu sync.Mutex
	sinkMu sync.Mutex
	sink   Sink
	final  string // the stop or done line
	// closedN: the tickets closed in the run (merged, or with nothing to merge), counted as their events
	// go out (see tally); closedIDs, the tickets it counts, in the order they closed. Those set aside
	// are the ticket book's.
	closedN   int
	closedIDs []string
}

// newTallies are the tallies of a run that has sent no events yet, to sink.
func newTallies(sink Sink) tallies {
	return tallies{sink: sink}
}

// ledger is what the workers, Run and the merges share, all under mu.
type ledger struct {
	mu        sync.Mutex
	active    map[string]Status // tickets being worked on; left set for those still running at the end
	finishing map[string]string // what each worker is doing that Ctrl+C doesn't stop: finishMerge, say
	labelled  map[string]bool   // tickets carrying UnmergedLabel, which a merge removes
	rechecks  []recheck         // tickets set aside for a failed check, to check again once Base moves on
	rechecked map[string]bool   // tickets taken up to be checked again (see dueRecheck)
	// setupSet: setup and setup_files as a merge in this run committed them to Base (see takeSettings);
	// nil for Config's.
	setupSet *setupConf
	// blockers: each ready ticket's blockers and sources, kept for the run (see linksOf); picks, how
	// many times pick has read bd ready; baseAt, the commit Base was on as a pick last read it (see
	// pickHead).
	blockers map[string]blockLinks
	picks    int
	baseAt   pickedHead
	// placed: where each worker started (from when its tab is open) or adopted in this run is, and,
	// once it stopped the run, why: the next run carries on with those left running (see saveCarried).
	placed map[string]workerSpot
	// footprints: the running tickets' footprints (see startFootprint).
	footprints map[string]*runFootprint
	// mergedN: the tickets merged into Base in this run (see countMerge); fullChecked: how the full
	// check after the run went, once it has run (see FullCheck).
	mergedN     int
	fullChecked *fullCheck
}

// newLedger is the ledger of a run that has done nothing yet.
func newLedger() ledger {
	return ledger{
		active: map[string]Status{}, finishing: map[string]string{},
		labelled: map[string]bool{}, rechecked: map[string]bool{}, blockers: map[string]blockLinks{},
		placed: map[string]workerSpot{}, footprints: map[string]*runFootprint{},
	}
}

// carryover is the workers carried over from the last run and those left for the next. keptOut is
// written as the run carries the last run's workers over, before carriedIn is set; from then on all
// of it is under leftMu.
type carryover struct {
	leftMu sync.Mutex
	// keptOut: the workers the last run left behind that this run doesn't carry over but doesn't
	// drop either, on tickets outside its scope, in the other terminal or that it couldn't check,
	// saved again for a later run as they were. The run doesn't start their tickets (see isKeptOut).
	keptOut []project.LeftWorker
	// carriedIn: whether the run has carried over the last run's workers (see loadCarried); leftSaved,
	// whether it has saved those it leaves for the next (see leaveBehind, SaveLeft).
	carriedIn bool
	leftSaved bool
}

// triageQueue is triage's queue, set by StartTriage before any worker runs. Workers send to triageQ
// until FinishTriage calls triageFinish, cancelling triageStop; triageDone closes when the triage
// goroutine returns.
type triageQueue struct {
	triageQ      chan queuedDeferral
	triageStop   context.Context
	triageFinish context.CancelFunc
	triageDone   chan struct{}
}

// envHold is the hold for the environment, Run's own until Run has returned (runDone), then
// triage's: the tickets whose workers failed at once, or whose setup failed, in a row, the tickets
// triage blamed on the environment in a row, the reason once the run holds, with why, and whether the
// machine was probed in this run. Run puts the hold on the gate (gateEnv) as it takes it up, with the
// probe's time as its end (see probeEnvironment). Workers say how they settled on their result;
// triage hands its verdicts over on verdicts until Run has returned, then counts them itself, as
// nothing else does by then. envGen counts the holds a probe has ended: each deferral is stamped with
// it as it is queued for triage, and its verdict counts toward the hold only while envGen is
// unchanged. Only Run changes it, but workers read it as they defer: atomic.
type envHold struct {
	fastFails   []fastFail
	envVerdicts []string
	envStop     *stopReason
	envWhy      string
	envProbed   bool
	envGen      atomic.Uint64
	verdicts    chan verdict
	runDone     chan struct{}
}

// New sets up a run: the worker prompt (with TICKET_ID), and its connections.
func New(cfg Config, log *Log, prompt string, d Deps) *Loop {
	o := &Loop{
		cfg: cfg, log: log, prompt: prompt,
		deps: depsFrom(d), tallies: newTallies(nil), ledger: newLedger(), predictor: newPredictor(),
		envHold:   envHold{verdicts: make(chan verdict), runDone: make(chan struct{})},
		gate:      newGate(),
		merges:    mergeQueue{joined: make(chan struct{}, 1)},
		drainReqs: make(chan drainRequest, 1),
	}
	o.usage = newUsageHold(&o.gate, o.emit, cfg.UsageLimitWait)
	return o
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
	// blocked: the finished ticket it keeps from merging, if any, says why in a few words (Event.Blocked).
	blocked string
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
	stopInterrupted      stopKind = "INTERRUPTED"
	stopMergeFailed      stopKind = "MERGE_FAILED"
	stopPanic            stopKind = "PANIC"
	stopPaused           stopKind = "PAUSED"
	stopReadyUnreadable  stopKind = "READY_UNREADABLE"
	stopStartFailed      stopKind = "START_FAILED"
	stopStatusUnreadable stopKind = "STATUS_UNREADABLE"
	stopTabFailed        stopKind = "TAB_FAILED"
	stopTerminalFailed   stopKind = "TERMINAL_FAILED"
	stopTicketLimit      stopKind = "TICKET_LIMIT"
	stopUnknown          stopKind = "UNKNOWN"
	stopUsageLimit       stopKind = "USAGE_LIMIT"
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

// blocks marks s as keeping the finished ticket it stops the run over from merging, with why in a
// few words, for the dashboard's row.
func (s *stopReason) blocks(why string) *stopReason {
	s.blocked = why
	return s
}

// Error is the line the run ends with.
func (s *stopReason) Error() string { return string(s.kind) + s.detail }

// Unwrap is what failed, or nil.
func (s *stopReason) Unwrap() error { return s.cause }

// setActive makes st the status of ticket st.Ticket, worked on from now; showActive shows it too.
func (o *Loop) setActive(st Status) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.active[st.Ticket] = st
}

// showActive makes st the status of ticket st.Ticket, worked on from now, and shows it on the dashboard.
func (o *Loop) showActive(st Status) {
	o.setActive(st)
	o.status(st)
}

// updateActive changes the status of ticket id with change and shows the dashboard the status as
// changed, the one stored, so that what it shows is what the loop keeps. A ticket no longer worked on
// is left as it is: change isn't called, and nothing is shown. change runs under mu: it must not block.
func (o *Loop) updateActive(id string, change func(*Status)) {
	o.mu.Lock()
	st, ok := o.active[id]
	if ok {
		change(&st)
		o.active[id] = st
	}
	o.mu.Unlock()
	if ok {
		o.status(st)
	}
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

// isActive reports whether ticket id is being worked on: its worker runs, or its work merges.
func (o *Loop) isActive(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, ok := o.active[id]
	return ok
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
	Repo      string
	Base      string // branch finished tickets are merged into
	Workspace string // Herdr workspace or tmux session for the worker tabs; none under cmux
	Terminal  string // what workers run in, project.TerminalHerdr, TerminalTmux or TerminalCmux; "" is Herdr
	// TerminalLabels are how the log names each terminal, by its name (Terminal's): the run's and those
	// workers left for a run elsewhere ran in.
	TerminalLabels map[string]TerminalLabel
	Watch          string // how to see the workers' tabs from elsewhere (tmux attach -t …); "" if in sight
	Limit          int    // stop after this many tickets, DoneSoFar's included; NoLimit for none
	DoneSoFar      int
	AgentKind      string
	WTRoot         string
	LogPath        string
	ReportsDir     string
	LaunchPrompt   bool          // give Claude workers their prompt at launch instead of pasting it
	Concurrency    int           // tickets worked on at the same time
	TicketLimit    time.Duration // a worker still going this long after dispatch stops the run; 0 for none
	Check          string        // the project's merge check (check_fast), from .orchestra/settings.json
	CheckTimeout   time.Duration // how long Check may run before it is stopped; 0 for project.DefaultCheckTimeout
	CheckOwn       string        // a worker's check of its own change (check_own), which its rules name; "" for Check
	Version        string        // orchestra's version, for the log
	NoFootprint    bool          // start tickets side by side even when their footprints overlap
	Predict        bool          // have the predictor organ guess the files of ready tickets naming none
	Ticket         string        // the run's scope: only this ticket and its descendants; "" for all of bd ready
	Feature        string        // the feature request (--feature) Ticket, its epic, was talked through from; "" for none
	// Spec is the spec (--spec) Ticket, its root epic, was broken down from: its path in the
	// repository; "" for none.
	Spec string
	// Filing is the scope still being filed as the run starts on it, as a spec's breakdown files the
	// rest after its environment ticket; nil when it is filed already.
	Filing       *Filing
	ExcludeTypes []string // issue types never dispatched, such as epics, from .orchestra/settings.json
	// CheckFull is every check (check_full), from .orchestra/settings.json, run once at the end of a
	// run for at most CheckFullTimeout (0 for project.DefaultCheckFullTimeout); "" for none.
	CheckFull        string
	CheckFullTimeout time.Duration
	// Setup installs the project's dependencies (setup), run in a ticket's new worktree before its worker
	// starts, and before Check on a finished ticket's branch when the commits since changed a file
	// SetupFiles matches, for at most CheckTimeout each, and always before CheckFull, within its time
	// limit; "" for none (see setUpWorktree, setUp and FullCheck). A merge that commits others to Base's
	// settings.json replaces them from then on (see takeSettings).
	Setup      string
	SetupFiles []string
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
	// CheckHandBacks is how many times a finished ticket whose check fails on its branch, rebased or not, is
	// handed back to its worker to fix (see checkRebased), within ResolveTimeout each; 0 for never.
	CheckHandBacks int
	// MergeTrain is how many of the tickets waiting in the merge queue are checked ahead of their turn,
	// while the ticket before each is checked, rebased onto its branch (see train.go); 0 for none.
	MergeTrain int
	// WorkerArgs start every Claude worker, before its own arguments: --no-chrome or --chrome, and
	// --effort.
	WorkerArgs []string
	// WorkerPermissionMode is the permission mode Claude workers start in (claude --permission-mode),
	// when the workers' claude lists it (see permissionArgs); "" for Claude Code's own default.
	WorkerPermissionMode string
	// EnvProbe is how long after the run holds for the environment, once no ticket runs, one
	// worker without a ticket is started to see whether commands run again; 0 for none.
	EnvProbe time.Duration
	// ProbeHooks has the probe's Claude worker report through hooks, as a ticket's does, to
	// project.ProbeRun in Repo's .orchestra/run/: a terminal that reads its workers' state from their
	// hooks (tmux), not their screens (Herdr), needs them to tell what it is doing.
	ProbeHooks bool
	// UsageLimitWait is how long the run waits for the account's usage limit to reset, from when it
	// began to hold for it, before the workers still limited stop it (USAGE_LIMIT); 0 for not at all.
	UsageLimitWait time.Duration
	// Machine is the folder the machine's orchestra runs register in (see machine); "" for none.
	// MachineWorkers caps the workers running across them: a free slot waits while they are at it;
	// 0 for no cap.
	Machine        string
	MachineWorkers int
	// MCP is the MCP servers Claude workers get and no others: those mcp_servers in
	// .orchestra/settings.json names, with their definitions from this machine's Claude Code config.
	// nil when the project hasn't chosen: workers then load every server Claude Code finds.
	MCP *[]mcp.Server
}

// NoLimit is Config.Limit for a run with no ticket limit, as a run has unless --limit or LIMIT gives one.
const NoLimit = math.MaxInt

// LimitLabel is the ticket limit, as the log and the dashboard say it: the number, or none.
func LimitLabel(limit int) string {
	if limit == NoLimit {
		return "none"
	}
	return strconv.Itoa(limit)
}

// Progress is how far a run is through its ticket limit after n tickets, as the log and the dashboard
// say it: n/limit, or n alone when it has none.
func Progress(n, limit int) string {
	if limit == NoLimit {
		return strconv.Itoa(n)
	}
	return fmt.Sprintf("%d/%d", n, limit)
}

// TerminalLabel is how a run's log names a terminal and the place of its workers' tabs.
type TerminalLabel struct {
	Name  string // the terminal, as the log says it: Herdr, tmux
	Tabs  string // the place of the workers' tabs, as the START line calls Workspace: workspace, session
	Where string // what the START line says in Workspace's place, for a terminal with none; "" for Workspace
}

// terminalLabel is how the log names the terminal t: as TerminalLabels has it, else by its name, its
// tabs in a workspace.
func (c Config) terminalLabel(t string) TerminalLabel {
	for name, l := range c.TerminalLabels {
		if project.SameTerminal(name, t) {
			return l
		}
	}
	return TerminalLabel{Name: t, Tabs: "workspace"}
}

// ClaudeWorkers reports whether the workers are Claude Code agents (AgentKind claude), which alone
// take Claude's arguments, hooks and MCP servers.
func (c Config) ClaudeWorkers() bool {
	return c.AgentKind == "claude"
}
