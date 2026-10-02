package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Triage that won't run costs nothing: a deferral gathers no evidence for it, and skipping the
// organs drops what is still queued without a word.

// evidenceReads counts the reads that gather a deferred ticket's evidence for triage: bd show, the
// worktree's git status, log and diff, and its worker's screen read without a state.
type evidenceReads struct {
	mu sync.Mutex
	n  map[string]int
}

// countEvidence has o's tracker, git and Herdr count the evidence reads they make.
func countEvidence(o *Loop) *evidenceReads {
	r := &evidenceReads{n: map[string]int{}}
	o.tickets = countedTickets{o.tickets, r}
	o.history = countedHistory{o.history, r}
	o.agents = countedAgents{o.agents, r}
	return r
}

func (r *evidenceReads) count(what string) { r.mu.Lock(); r.n[what]++; r.mu.Unlock() }

func (r *evidenceReads) of(what string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n[what]
}

type countedTickets struct {
	Tickets
	reads *evidenceReads
}

func (c countedTickets) Describe(ctx context.Context, id string) string {
	c.reads.count("bd show")
	return c.Tickets.Describe(ctx, id)
}

type countedHistory struct {
	History
	reads *evidenceReads
}

func (c countedHistory) ShortStatus(ctx context.Context, worktree string) string {
	c.reads.count("git status")
	return c.History.ShortStatus(ctx, worktree)
}

func (c countedHistory) OneLineLog(ctx context.Context, dir, revs string) string {
	c.reads.count("git log")
	return c.History.OneLineLog(ctx, dir, revs)
}

func (c countedHistory) DiffStat(ctx context.Context, worktree string) string {
	c.reads.count("git diff")
	return c.History.DiffStat(ctx, worktree)
}

type countedAgents struct {
	Agents
	reads *evidenceReads
}

func (c countedAgents) Screen(ctx context.Context, name string, state AgentState) string {
	if state == "" {
		c.reads.count("screen")
	}
	return c.Agents.Screen(ctx, name, state)
}

// With triage off, neither a ticket its worker defers nor one it leaves open gathers evidence for
// triage. With triage on, each makes each read once.
func TestTriageOffGathersNoEvidence(t *testing.T) {
	t.Parallel()
	for _, triage := range []bool{false, true} {
		t.Run(fmt.Sprintf("triage %v", triage), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "deferred by its worker", 1)
				h.beads.add("B", "left open by its worker", 2)
				h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })
				h.worker("B", func(w *fakeWorker) AgentState { return "idle" })
				o := h.loop()
				reads := countEvidence(o)
				if triage {
					o.StartTriage()
				}
				code := o.Run(t.Context())
				o.FinishTriage(t.Context())
				if code != ExitOK || len(h.sink.of(EvDeferred)) != 2 {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				want := 0
				if triage {
					want = 2
				}
				for _, what := range []string{"bd show", "git status", "git log", "git diff", "screen"} {
					if got := reads.of(what); got != want {
						t.Errorf("%s: %d reads, want %d", what, got, want)
					}
				}
			})
		})
	}
}

// slowTriage is a claude whose triage takes until it is stopped. It creates started once it has
// its input.
func slowTriage(t *testing.T, started string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat >/dev/null\ntouch '" + started + "'\nexec sleep 600\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Ctrl+C during "finishing triage…" skips the organs: the triage in progress and the deferrals
// still queued are dropped, with no TRIAGE_FAILED for any of them.
func TestSkippingTheOrgansDropsTriageQuietly(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(t.TempDir(), "started")
	organCtx, skipOrgans := context.WithCancel(context.Background())
	defer skipOrgans()
	sink := &recordSink{}
	o := &Loop{cfg: Config{LogPath: logPath}, log: log, sink: sink, notes: newFakeBeads(),
		organ: organ.Client{Bin: slowTriage(t, started)}, organCtx: organCtx}
	o.StartTriage()
	for _, id := range []string{"A", "B", "C"} {
		o.queueTriage(context.Background(), organ.Deferral{ID: id})
	}

	// As organPhase does: FinishTriage waits on the phase's context, which Ctrl+C cancels, and
	// that skips the organs.
	phase, ctrlC := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		o.FinishTriage(phase)
	}()
	eventually(t, "A's triage did not start", func() bool { return exists(started) })
	ctrlC()
	skipOrgans()
	for what, done := range map[string]chan struct{}{"FinishTriage": finished, "the triage goroutine": o.triageDone} {
		select {
		case <-done:
		case <-time.After(patience):
			t.Fatalf("%s did not return", what)
		}
	}

	if got := sink.text(); got != "" {
		t.Errorf("skipped triage should say nothing:\n%s", got)
	}
	if logged := read(t, logPath); strings.Contains(logged, "TRIAGE_FAILED") {
		t.Errorf("log:\n%s", logged)
	}
	if len(o.triageQ) != 0 {
		t.Errorf("%d deferrals left in the queue", len(o.triageQ))
	}
}

// Once the organs are skipped, a ticket deferred before FinishTriage has run gathers no evidence.
func TestNoEvidenceOnceTheOrgansAreSkipped(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	organCtx, skipOrgans := context.WithCancel(context.Background())
	o := &Loop{log: log, sink: &recordSink{}, tickets: readyTickets{}, history: quietHistory{}, agents: noAgents{},
		organ: organ.Client{Bin: filepath.Join(t.TempDir(), "no-claude")}, organCtx: organCtx}
	reads := countEvidence(o)
	o.StartTriage()
	defer o.FinishTriage(context.Background())
	skipOrgans()
	o.triageDeferred(context.Background(), "A", "the worker deferred it", t.TempDir())
	for _, what := range []string{"bd show", "git status", "git log", "git diff", "screen"} {
		if got := reads.of(what); got != 0 {
			t.Errorf("%s: %d reads, want none", what, got)
		}
	}
}
