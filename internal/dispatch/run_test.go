package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// Whole runs, with Beads and Herdr faked and git real.

type harness struct {
	t        *testing.T
	repo     string
	git      func(dir string, args ...string) string
	beads    *fakeBeads
	herdr    *fakeHerdr
	sink     *runSink
	logPath  string
	cfg      Config
	reporter Reporter
	alerts   *alerts // the last loop's notifications
}

// newHarness sets up a repository on main and an empty tracker; workers are Claude, given their
// prompt at launch, one at a time.
func newHarness(t *testing.T) *harness {
	t.Helper()
	repo, run := gitRepo(t)
	run(repo, "branch", "-M", "main")
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".orchestra/\n"), 0o644) // the launch prompt
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "ignore .orchestra")
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	beads := newFakeBeads()
	h := &harness{t: t, repo: repo, git: run, beads: beads, herdr: newFakeHerdr(t, beads),
		sink: &runSink{held: make(chan struct{})}, logPath: logPath,
		cfg: Config{Repo: repo, Base: "main", Workspace: "ws", Limit: 10, Concurrency: 1, AgentKind: "claude",
			WTRoot: t.TempDir(), LogPath: logPath, LaunchPrompt: true}}
	t.Cleanup(h.herdr.running.Wait) // before the folders go
	return h
}

// worker gives the ticket's workers their behaviours, one per dispatch.
func (h *harness) worker(id string, bs ...behaviour) {
	h.herdr.behaviours[id] = append(h.herdr.behaviours[id], bs...)
}

func (h *harness) loop() *Loop {
	h.t.Helper()
	log, err := OpenLog(h.logPath, false, "t")
	if err != nil {
		h.t.Fatal(err)
	}
	h.alerts = recordAlerts(log)
	o := New(h.cfg, log, "Work on TICKET_ID.", Deps{Tickets: h.beads, Notes: h.beads, Tabs: h.herdr, Starter: h.herdr,
		Namer: h.herdr, Agents: h.herdr, Reporter: h.reporter, Checkout: git.Git{}, Worktrees: git.Git{}, Merger: git.Git{},
		History: git.Git{}, Advisor: organ.Client{Bin: filepath.Join(h.t.TempDir(), "no-claude")}, AdviceCtx: context.Background()})
	o.SetSink(h.sink)
	o.wait = timing{poll: time.Millisecond, startRetry: time.Millisecond, adopt: 5 * time.Second, blocked: 30 * time.Millisecond,
		idleGrace: 30 * time.Millisecond, settle: 5 * time.Second}
	return o
}

func (h *harness) run() (*Loop, int) {
	o := h.loop()
	return o, o.Run(context.Background())
}

func (h *harness) mainLog() string { return h.git(h.repo, "log", "--oneline", "main") }

func (h *harness) worktree(id string) string { return filepath.Join(h.cfg.WTRoot, id) }

func (h *harness) logged() string { return read(h.t, h.logPath) }

// waitHeld waits for the first HOLD, as a worker still running when another stops the run.
func (h *harness) waitHeld() {
	select {
	case <-h.sink.held:
	case <-time.After(5 * time.Second):
		h.t.Error("no HOLD")
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func activeIDs(o *Loop) []string {
	var ids []string
	for _, st := range o.activeList() {
		ids = append(ids, st.Ticket)
	}
	return ids
}

func equal(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

// Notifications follow the kind of event, not words in it: a title or triage summary saying
// closed, FAILED or deferred shows nothing.
func TestRunNotifiesByEventNotText(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "closed FAILED deferred", 1)
	h.worker("A", finishes("a.txt"))
	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	o.emit(Event{Kind: EvTriage, Ticket: "A", Text: "  triage A: flaky (high confidence) - deferred after FAILED checks, closed sockets"})
	closed := h.sink.of(EvClosed)
	if len(closed) != 1 {
		t.Fatalf("closed: %q", closed)
	}
	want := []string{strings.TrimPrefix(closed[0], "A "), o.Final()}
	if got := h.alerts.list(); !equal(got, want) {
		t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRunMergesEachFinishedTicket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", finishes("a.txt"))
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got, want := h.sink.of(EvDispatch), []string{"A [1/10] A dispatching: first", "B [2/10] B dispatching: second"}; !equal(got, want) {
		t.Errorf("dispatched:\n%s", strings.Join(got, "\n"))
	}
	if got := h.sink.of(EvClosed); len(got) != 2 || !strings.HasPrefix(got[0], "A ") || !strings.HasPrefix(got[1], "B ") {
		t.Errorf("closed:\n%s", strings.Join(got, "\n"))
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
	if exists(h.worktree("A")) || exists(h.worktree("B")) {
		t.Error("merged tickets' worktrees should be removed")
	}
	if br := h.git(h.repo, "branch", "--list", "wt/*"); br != "" {
		t.Errorf("merged tickets' branches should be deleted: %q", br)
	}
	if got := h.herdr.tabsClosed(); !equal(got, []string{"tab1", "tab2"}) {
		t.Errorf("tabs closed: %v", got)
	}
	if got := h.herdr.pastedTo(); len(got) != 0 {
		t.Errorf("the prompt was given at launch, yet pasted to %v", got)
	}
	if got := activeIDs(o); len(got) != 0 {
		t.Errorf("still active: %v", got)
	}
}

func TestRunGoesOnPastATicketItsWorkerDefers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) string { w.claim(); w.deferIt(); return "idle" })
	h.worker("B", finishes("b.txt"))
	o := h.loop()
	o.StartTriage()
	code := o.Run(context.Background())
	o.FinishTriage(context.Background())
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.HasPrefix(got[0], "A   A deferred by worker") {
		t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
	}
	// Triage got the deferral, and fails here without claude.
	if !strings.Contains(h.sink.text(), "TRIAGE_FAILED for A") {
		t.Errorf("A was not triaged:\n%s", h.sink.text())
	}
	if !strings.Contains(h.mainLog(), "B: add b.txt") {
		t.Error("B should be merged")
	}
	if !exists(h.worktree("A")) || !equal(h.herdr.tabsClosed(), []string{"tab2"}) {
		t.Error("the deferred ticket's worktree and tab should be left open")
	}
	if got := o.setAside(); !equal(got, []string{"A"}) {
		t.Errorf("set aside: %v", got)
	}
}

func TestRunStopsForAWorkerIdleWithItsTicketInProgress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "idle" })
	o, code := h.run()
	if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress in tab tab1") {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if got := h.sink.of(EvDispatch); len(got) != 1 {
		t.Errorf("nothing should start after a pause:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(h.beads.notesOf("A"), "went idle with the ticket still in_progress") {
		t.Errorf("notes: %q", h.beads.notesOf("A"))
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v, want A left for the reviewer", got)
	}
	if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
		t.Error("the paused worker's tab and worktree should be left open")
	}
}

func TestRunStopsForAWorkerBlockedTooLong(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "blocked" })
	o, code := h.run()
	if code != ExitStuck || o.Final() != "BLOCKED >4min: tab tab1 (A) needs attention" {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v", got)
	}
}

// A worker asks the maintainer a question: its ticket leaves the queue and the run goes on. Once
// the question is answered the ticket comes back, to its old worktree, and the earlier worker still
// in its tab gives up the ticket's name to the new one.
func TestAskedTicketReturnsOnceAnswered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A",
		func(w *fakeWorker) string { w.claim(); w.ask("Q", "which way?"); return "idle" },
		finishes("a.txt"))
	h.worker("B", func(w *fakeWorker) string {
		w.beads.set("Q", "closed") // the maintainer answers meanwhile
		return finishes("b.txt")(w)
	})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.of(EvAsked); len(got) != 1 || !strings.HasPrefix(got[0], "A   ASKED: A waits on your answer to Q (which way?)") {
		t.Errorf("asked:\n%s", strings.Join(got, "\n"))
	}
	if got := h.sink.of(EvDispatch); len(got) != 3 || !strings.HasPrefix(got[0], "A ") || !strings.HasPrefix(got[1], "B ") || !strings.HasPrefix(got[2], "A ") {
		t.Errorf("dispatched:\n%s", strings.Join(got, "\n"))
	}
	ev := h.sink.text()
	for _, want := range []string{"reusing worktree ", "/A (wt/A)", "earlier worker for A renamed to A-1"} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
	if got := h.herdr.tabsClosed(); !equal(got, []string{"tab2", "tab3"}) {
		t.Errorf("tabs closed: %v; the asking worker's tab1 should stay", got)
	}
}

// A returning ticket whose branch conflicts with main gets no worker, which could only work on a
// stale base and end in MERGE_CONFLICT: it is deferred with a note on how to rebase it.
func TestReturningTicketThatConflictsIsSetAside(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.git(h.repo, "branch", "wt/A")
	h.git(h.repo, "checkout", "-q", "wt/A")
	os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("A\n"), 0o644)
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "A: earlier attempt")
	h.git(h.repo, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("main\n"), 0o644)
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "main moves on")
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", finishes("a.txt"))
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.Contains(got[0], "REBASE_FAILED: wt/A conflicts with main -> A deferred without starting a worker") {
		t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
	}
	if st, _ := h.beads.Status("A"); st != "deferred" {
		t.Errorf("A is %s, want deferred", st)
	}
	if n := h.beads.notesOf("A"); !strings.Contains(n, "git rebase main") || !strings.Contains(n, "bd undefer A") {
		t.Errorf("notes: %q", n)
	}
	if got := h.herdr.tabsClosed(); !equal(got, []string{"tab1"}) {
		t.Errorf("tabs closed: %v; only B's worker should have had a tab", got)
	}
	if log := h.mainLog(); strings.Contains(log, "A:") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
	if got := h.git(h.worktree("A"), "status", "--porcelain"); got != "" {
		t.Errorf("A's worktree should be left clean, with the rebase undone:\n%s", got)
	}
}

// With two workers, one stopping the run holds it: nothing new starts, and the other finishes and
// merges.
func TestHoldLetsTheRunningWorkerFinish(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.add("C", "third", 3)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "idle" })
	h.worker("B", func(w *fakeWorker) string { h.waitHeld(); return finishes("b.txt")(w) })
	o, code := h.run()
	if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") || strings.Contains(o.Final(), "also") {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	holds := h.sink.of(EvHold)
	if len(holds) != 1 || !strings.HasPrefix(holds[0], "A HOLD: PAUSED: A still in_progress") ||
		!strings.HasSuffix(holds[0], "; no new tickets while the 1 running finish") {
		t.Errorf("holds:\n%s", strings.Join(holds, "\n"))
	}
	if !strings.Contains(h.mainLog(), "B: add b.txt") {
		t.Error("B should finish and merge during the hold")
	}
	for _, d := range h.sink.of(EvDispatch) {
		if strings.HasPrefix(d, "C ") {
			t.Errorf("C started during the hold: %s", d)
		}
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v", got)
	}
}

func TestBothWorkersStopReasonsAreReported(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "idle" })
	h.worker("B", func(w *fakeWorker) string { h.waitHeld(); w.claim(); return "blocked" })
	o, code := h.run()
	if code != ExitStuck {
		t.Errorf("exit %d, want %d: the first reason decides", code, ExitStuck)
	}
	final := o.Final()
	if !strings.HasPrefix(final, "PAUSED: A still in_progress") || !strings.Contains(final, "; also BLOCKED >4min: tab ") ||
		!strings.HasSuffix(final, " (B) needs attention") {
		t.Errorf("final %q", final)
	}
	holds := h.sink.of(EvHold)
	if len(holds) != 2 || !strings.HasPrefix(holds[0], "A HOLD: PAUSED") || !strings.HasPrefix(holds[1], "B HOLD: BLOCKED") {
		t.Errorf("holds:\n%s", strings.Join(holds, "\n"))
	}
	if got := activeIDs(o); len(got) != 2 {
		t.Errorf("active: %v, want both left for the reviewer", got)
	}
}

// Ctrl+C while a worker works: the run stops at once and leaves the worker, its tab and worktree.
func TestInterruptLeavesAWorkingWorker(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	started, release := make(chan struct{}), make(chan struct{})
	h.worker("A", func(w *fakeWorker) string { w.claim(); close(started); <-release; return "idle" })
	defer close(release)
	o := h.loop()
	o.ReportInterrupt = true
	ctx, cancel := context.WithCancel(context.Background())
	codes := make(chan int, 1)
	go func() { codes <- o.Run(ctx) }()
	<-started
	cancel()
	select {
	case code := <-codes:
		if code != ExitInterrupted || !strings.HasPrefix(o.Final(), "INTERRUPTED") {
			t.Errorf("exit %d, final %q", code, o.Final())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Ctrl+C")
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v", got)
	}
	if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
		t.Error("the worker's tab and worktree should be left")
	}
}

// Started from its prompt file the worker never shows up, and Herdr's start refuses the reporting
// arguments: it is started plainly and the prompt pasted.
func TestStartFallsBackToPastingThePrompt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.reporter = fakeReporter{}
	h.herdr.launchFails["A"] = true
	h.herdr.refuseArgs = true
	h.beads.add("A", "first", 1)
	h.worker("A", finishes("a.txt"))
	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) {
		t.Errorf("pasted to %v", got)
	}
	logged := h.logged()
	for _, want := range []string{"could not be started from its prompt file", errRefused.Error()} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
	if !strings.Contains(h.mainLog(), "A: add a.txt") {
		t.Error("A should be merged")
	}
}

// A worker launched from its prompt file that Herdr sees only after the adoption gave up is
// adopted: no second worker is started in its pane, and the prompt it was launched with is not
// pasted to it again.
func TestStartAdoptsAWorkerSlowToAppear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.herdr.launchSlow["A"] = true
	h.beads.add("A", "first", 1)
	h.worker("A", finishes("a.txt"))
	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.herdr.startsFor(); len(got) != 0 {
		t.Errorf("herdr agent start was asked for %v; want no second worker", got)
	}
	if got := h.herdr.pastedTo(); len(got) != 0 {
		t.Errorf("pasted to %v; the worker had its prompt at launch", got)
	}
	if !strings.Contains(h.sink.text(), "A's worker was slow to start; named it A") {
		t.Errorf("events:\n%s", h.sink.text())
	}
	if !strings.Contains(h.mainLog(), "A: add a.txt") {
		t.Error("A should be merged")
	}
}

// A launch that leaves the pane empty for good falls back to Herdr's start and a pasted prompt.
func TestStartFallsBackWhenTheLaunchedWorkerNeverAppears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.herdr.launchLost["A"] = true
	h.beads.add("A", "first", 1)
	h.worker("A", finishes("a.txt"))
	o := h.loop()
	o.wait.adopt = 20 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.herdr.startsFor(); !equal(got, []string{"A"}) {
		t.Errorf("herdr agent start was asked for %v; want A once", got)
	}
	if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) {
		t.Errorf("pasted to %v", got)
	}
	if !strings.Contains(h.mainLog(), "A: add a.txt") {
		t.Error("A should be merged")
	}
}

// A start that times out leaves the agent in its pane without a name: it is adopted, not started
// twice.
func TestStartAdoptsAnAgentLeftUnnamed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.LaunchPrompt = false
	h.herdr.startUnnamed["A"] = true
	h.beads.add("A", "first", 1)
	h.worker("A", finishes("a.txt"))
	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if !strings.Contains(h.sink.text(), "A's worker started without its name; named it") {
		t.Errorf("events:\n%s", h.sink.text())
	}
	if !strings.Contains(h.mainLog(), "A: add a.txt") {
		t.Error("A should be merged")
	}
}

// A ticket ID longer than Herdr's 32-character name limit still gets a worker, under the cut name
// herdr.AgentName gives it, whether the worker is launched with its prompt or started and pasted to.
func TestLongTicketIDRunsUnderACutName(t *testing.T) {
	t.Parallel()
	const id = "platform-backend-services-core-a3f.12.34" // 40 characters
	for _, launch := range []bool{true, false} {
		h := newHarness(t)
		h.cfg.LaunchPrompt = launch
		h.herdr.agentName = herdr.AgentName
		h.beads.add(id, "long", 1)
		h.worker(id, finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("launch=%v: exit %d, final %q\n%s", launch, code, o.Final(), h.sink.text())
		}
		if !strings.Contains(h.mainLog(), id+": add a.txt") {
			t.Errorf("launch=%v: %s should be merged:\n%s", launch, id, h.mainLog())
		}
	}
}

// A prompt that never takes defers the ticket for a retry, and the run goes on without it.
func TestPromptThatNeverTakesDefersTheTicket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.LaunchPrompt = false
	h.herdr.promptFails["A"] = true
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.HasPrefix(got[0], "A   PROMPT_FAILED: A's worker never started on its prompt -> deferred") {
		t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
	}
	if got := h.herdr.pastedTo(); !equal(got, []string{"A", "A", "B"}) {
		t.Errorf("pasted to %v: A's prompt should be sent twice, never more", got)
	}
	if st, _ := h.beads.Status("A"); st != "deferred" || !strings.Contains(h.beads.notesOf("A"), "never started on its prompt") {
		t.Errorf("A is %s, notes %q", st, h.beads.notesOf("A"))
	}
	if !strings.Contains(h.mainLog(), "B: add b.txt") {
		t.Error("B should be merged")
	}
}

// closesWithoutCommit claims the ticket and closes it, committing nothing.
func closesWithoutCommit(w *fakeWorker) string {
	w.claim()
	w.close()
	return "idle"
}

func TestUnmergedTicketHoldsItsDependentsInLaterRuns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", closesWithoutCommit)
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if a, _ := h.beads.Show("A"); !HasLabel(a, UnmergedLabel) {
		t.Fatalf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
	}

	// Run 2: bd ready lists B, since A is closed, but A's code is still not on main.
	if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
		t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "B waits: A closed but not merged (left unmerged by an earlier run)") {
		t.Errorf("events:\n%s", ev)
	}

	// The maintainer finishes A by hand, keeping its branch; run 3 sees it on main.
	wt := h.worktree("A")
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("a\n"), 0o644)
	h.git(wt, "add", "a.txt")
	h.git(wt, "commit", "-q", "-m", "A: add a.txt")
	h.git(h.repo, "merge", "-q", "--ff-only", "wt/A")
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "A, left unmerged by an earlier run, is on main now") {
		t.Errorf("events:\n%s", ev)
	}
	if a, _ := h.beads.Show("A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A's label should be removed: %v", a.Labels)
	}
	if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
}

func TestReopenedUnmergedTicketLosesItsLabelWhenItMerges(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", closesWithoutCommit, finishes("a.txt"))
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	h.beads.set("A", "open") // the maintainer reopens it
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if a, _ := h.beads.Show("A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A merged, so its label should be removed: %v", a.Labels)
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
}

// A worker whose status Herdr can't tell stops the run once it has stayed that way too long.
func TestRunStopsForAWorkerUnknownTooLong(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "unknown" })
	o := h.loop()
	o.wait.unknown = 30 * time.Millisecond
	code := o.Run(context.Background())
	want := "UNKNOWN >5min: Herdr can't tell what the worker in tab tab1 (A) is doing; it needs attention"
	if code != ExitStuck || o.Final() != want {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if !h.alerts.has(o.Final()) || !strings.Contains(read(t, h.logPath), want) {
		t.Error("the stop should be logged and notified")
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v", got)
	}
}

// A worker still going after the ticket limit, whatever Herdr says it is doing, stops the run like
// a paused one: noted on the ticket, its tab and worktree left open.
func TestRunStopsForAWorkerPastTheTicketLimit(t *testing.T) {
	t.Parallel()
	for _, st := range []string{"working", "unknown"} {
		t.Run(st, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.cfg.TicketLimit = 50 * time.Millisecond
			h.beads.add("A", "first", 1)
			h.beads.add("B", "second", 2)
			h.worker("A", func(w *fakeWorker) string { w.claim(); return st })
			o := h.loop()
			o.wait.unknown = time.Hour
			code := o.Run(context.Background())
			want := "TICKET_LIMIT: A still " + st + " after 50ms in tab tab1 (worktree " + h.worktree("A") + "); stopping so it can be looked at"
			if code != ExitStuck || o.Final() != want {
				t.Fatalf("exit %d, final %q, want %q", code, o.Final(), want)
			}
			if !h.alerts.has(o.Final()) || !strings.Contains(read(t, h.logPath), want) {
				t.Error("the stop should be logged and notified")
			}
			if got := h.sink.of(EvDispatch); len(got) != 1 {
				t.Errorf("nothing should start after the stop:\n%s", strings.Join(got, "\n"))
			}
			if !strings.Contains(h.beads.notesOf("A"), "after the 50ms ticket limit") {
				t.Errorf("notes: %q", h.beads.notesOf("A"))
			}
			if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
				t.Error("the worker's tab and worktree should be left open")
			}
		})
	}
}

// eventually waits for cond, failing the test after a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Error(what)
			return
		}
	}
}

// queueSizes returns the queue sizes the loop reported, in order.
func (s *runSink) queueSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []int
	for _, ev := range s.events {
		if ev.Kind == EvQueue {
			l = append(l, ev.Queued)
		}
	}
	return l
}

// A ticket that becomes ready while a worker runs goes to a free slot at the next poll, not once
// the running ticket finishes.
func TestTicketReadyMidRunTakesAFreeSlot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1)
	bStarted := make(chan struct{})
	h.worker("A", func(w *fakeWorker) string {
		w.claim()
		w.beads.add("B", "follow-up", 2) // the worker files a follow-up
		select {
		case <-bStarted:
		case <-time.After(5 * time.Second):
			t.Error("B waited for A to finish")
		}
		return finishes("a.txt")(w)
	})
	h.worker("B", func(w *fakeWorker) string { close(bStarted); return finishes("b.txt")(w) })
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
}

// With every slot taken, each poll brings the dashboard's queue count up to date, without a line
// in the log.
func TestQueueCountFollowsWhileSlotsAreFull(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) string {
		w.claim()
		w.beads.add("B", "second", 2)
		w.beads.add("C", "third", 3)
		eventually(t, "the queue count never reached 2", func() bool {
			q := h.sink.queueSizes()
			return len(q) > 0 && q[len(q)-1] == 2
		})
		return finishes("a.txt")(w)
	})
	h.worker("B", finishes("b.txt"))
	h.worker("C", finishes("c.txt"))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.queueSizes(); len(got) != 1 || got[0] != 2 {
		t.Errorf("queue sizes reported: %v, want [2]: dispatches carry the rest", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(h.logged()), "\n") {
		if strings.Count(strings.TrimSpace(line), " ") < 2 {
			t.Errorf("log line without text: %q", line)
		}
	}
}
