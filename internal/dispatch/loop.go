// Package dispatch is the orchestra loop: it hands ready tickets to workers, up to a number at a
// time, watches them settle, merges finished tickets one at a time and sets the rest aside.
package dispatch

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Exit codes, unchanged from orchestrate.sh.
const (
	ExitOK          = 0   // nothing left in bd ready, or LIMIT reached
	ExitSetup       = 2   // setup problem found before starting
	ExitStuck       = 3   // a worker stayed blocked or unknown too long, went idle with its ticket in_progress, or ran past the ticket limit
	ExitTool        = 4   // Herdr, Beads or git failure
	ExitDirty       = 5   // uncommitted changes in the main checkout, or it left its branch
	ExitMerge       = 6   // a finished ticket's branch does not fast-forward
	ExitInterrupted = 130 // stopped with Ctrl+C
)

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
	labelled  map[string]bool       // tickets carrying UnmergedLabel, which a merge removes
	holdSaid  map[string]string     // why each held ticket waits, as last said
	blockers  map[string]blockLinks // each ready ticket's blockers, read once per run
	askedIDs  map[string]bool       // tickets set aside in this run to wait on a question

	// Scheduling by footprint. The running tickets' footprints, under mu; the repository's files,
	// the reason each ready ticket was last skipped and the shared edits warned about, Run's own.
	footprints map[string]*runFootprint
	files      *repoFiles
	skipSaid   map[string]string
	warned     map[string]bool

	// Triage's queue. Workers add to it until FinishTriage closes it; a worker still settling
	// after that finds it closed rather than a closed channel.
	triageMu     sync.Mutex
	triageOn     bool // StartTriage was called
	triageClosed bool // FinishTriage was called
	triageQ      []organ.Deferral
	triageWake   chan struct{} // buffered 1: something was queued, or the queue closed
	triageDone   chan struct{}

	wait timing // how long it waits on things; tests shorten it

	// ReportInterrupt logs the INTERRUPTED line from the loop itself; in the terminal UI the
	// command does it (it knows what stopped the run: Ctrl+C, a signal or the dashboard failing).
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

// timing is how long the loop waits on things. A zero field means the default.
type timing struct {
	poll       time.Duration // between reads of a worker's status: 3 seconds
	startRetry time.Duration // after a failed start, before looking for the agent: 3 seconds
	adopt      time.Duration // watching a pane for a worker slow to start: lateAdopt
	blocked    time.Duration // a worker blocked for longer stops the run: blockedLimit
	unknown    time.Duration // a worker whose status stays unknown for longer stops the run: unknownLimit
	longRun    time.Duration // without a ticket limit, a worker going on longer is reported once: longRunning
	idleGrace  time.Duration // idleGrace
	settle     time.Duration // SettleWait
	ready      time.Duration // between reads of bd ready while workers run: readyPoll
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

// ShortDuration is d as a person would write it: 2h, 1h30m, 45m.
func ShortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
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
}
