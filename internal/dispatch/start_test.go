package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// refusingHerdr is Herdr refusing every agent name: the start, the adoption and the rename.
type refusingHerdr struct {
	starts, adopts []string // names asked for
}

var errNameRefused = errors.New(`herdr agent start: exit status 1: {"error":{"code":"invalid_agent_name","message":"agent names must be lowercase"}}`)

func (h *refusingHerdr) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	return nil
}
func (h *refusingHerdr) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	h.starts = append(h.starts, name)
	return errNameRefused
}
func (h *refusingHerdr) IsArgumentRefused(err error) bool { return false }
func (h *refusingHerdr) IsNameRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_name")
}
func (h *refusingHerdr) WaitReady(ctx context.Context, name string) bool { return false }
func (h *refusingHerdr) AgentName(id string) string                      { return "agent-for-" + id }
func (h *refusingHerdr) AdoptAgent(ctx context.Context, pane, kind, name string) (AgentState, error) {
	h.adopts = append(h.adopts, name)
	return "working", errNameRefused
}
func (h *refusingHerdr) PaneAgent(ctx context.Context, pane string) (string, string, AgentState, error) {
	return "", "claude", "working", nil
}
func (h *refusingHerdr) RenameAgent(ctx context.Context, name, to string) error {
	return errNameRefused
}
func (h *refusingHerdr) FreeName(ctx context.Context, name string) string { return name + "-1" }
func (h *refusingHerdr) Status(ctx context.Context, name string) (AgentState, error) {
	return "gone", nil
}
func (h *refusingHerdr) Screen(ctx context.Context, name string, status AgentState) string { return "" }
func (h *refusingHerdr) Prompt(ctx context.Context, name, prompt string) error {
	return errors.New("no agent")
}
func (h *refusingHerdr) SendKeys(ctx context.Context, name string, keys ...string) error { return nil }
func (h *refusingHerdr) WaitStarted(ctx context.Context, name string) bool               { return false }

func TestRefusedAgentNameEndsTheStartWithoutRetries(t *testing.T) {
	for _, launch := range []bool{false, true} {
		f := newMergeFixture(t, "true")
		h := &refusingHerdr{}
		o := f.orch
		o.cfg.WTRoot, o.cfg.AgentKind, o.cfg.LaunchPrompt = t.TempDir(), "claude", launch
		o.starter, o.namer, o.agents = h, h, h
		s := o.work(context.Background(), Ticket{ID: "Cal-bl0.1", Title: "t"}, new(settling))
		if s == nil || s.code != ExitTool || s.kind != stopStartFailed || !errors.Is(s, errNameRefused) {
			t.Fatalf("launch=%v: want START_FAILED, got %+v", launch, s)
		}
		if !strings.Contains(s.Error(), "agent-for-Cal-bl0.1") || !strings.Contains(s.Error(), "agent names must be lowercase") {
			t.Errorf("launch=%v: the stop should name the agent and give Herdr's message: %s", launch, s)
		}
		if launch {
			if len(h.adopts) != 1 || h.adopts[0] != "agent-for-Cal-bl0.1" || len(h.starts) != 0 {
				t.Errorf("launch: adopted as %v and started as %v; want one adoption and no fallback start", h.adopts, h.starts)
			}
		} else if len(h.starts) != 1 || h.starts[0] != "agent-for-Cal-bl0.1" {
			t.Errorf("started as %v, want once as agent-for-Cal-bl0.1", h.starts)
		}
	}
}

// A probe whose start fails, with no agent left in its pane, reports the start's error.
func TestAProbeThatCannotStartReportsWhy(t *testing.T) {
	f := newMergeFixture(t, "true")
	h := &refusingHerdr{}
	o := f.orch
	o.cfg.AgentKind = "claude"
	o.starter, o.namer, o.agents = h, h, h
	_, err := o.probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "could not be started") || !strings.Contains(err.Error(), "agent names must be lowercase") {
		t.Errorf("want the start's error, got %v", err)
	}
}

func TestPrepareWorktreeReplacesADeletedFolder(t *testing.T) {
	f := newMergeFixture(t, "true")
	old := f.ticket(t, "k-1", "a.txt", "a\n")
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	f.orch.cfg.WTRoot = t.TempDir()
	wt, conflicts, s := f.orch.prepareWorktree(context.Background(), "k-1", "wt/k-1")
	if s != nil || conflicts {
		t.Fatalf("stop %v, conflicts %v", s, conflicts)
	}
	if want := filepath.Join(f.orch.cfg.WTRoot, "k-1"); wt != want {
		t.Errorf("worktree = %q, want a fresh one at %q", wt, want)
	}
	if got := read(t, filepath.Join(wt, "a.txt")); got != "a\n" {
		t.Errorf("the fresh worktree should be on the existing branch, a.txt = %q", got)
	}
	if br := strings.TrimSpace(f.git(wt, "branch", "--show-current")); br != "wt/k-1" {
		t.Errorf("branch = %q", br)
	}
}

func TestAnUnreadableStatusIsNotAClaim(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{}, log, "", Deps{Tickets: brokenBd{}})
	if o.claimed(context.Background(), "A") {
		t.Error("an unreadable status was taken for a claim")
	}
	if !strings.Contains(read(t, logPath), "database is locked") {
		t.Error("the cause was not logged")
	}
}

// A returning ticket whose branch conflicts with main gets no worker, which could only work on a
// stale base and end in MERGE_CONFLICT: it is deferred with a note on how to rebase it.
func TestReturningTicketThatConflictsIsSetAside(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.git(h.repo, "branch", "wt/A")
	h.git(h.repo, "checkout", "-q", "wt/A")
	if err := os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "A: earlier attempt")
	h.git(h.repo, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if st, _ := h.beads.Status(context.Background(), "A"); st != "deferred" {
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

// cutName stands in for herdr.AgentName, which this package's tests can't import (herdr imports
// dispatch): an ID over Herdr's 32-character limit gets a shorter name that isn't the ID.
func cutName(id string) string {
	if len(id) <= 32 {
		return id
	}
	return id[:28] + "-cut"
}

// A ticket ID longer than Herdr's 32-character name limit still gets a worker, under a cut name
// like the one herdr.AgentName gives it, whether the worker is launched with its prompt or started
// and pasted to.
func TestLongTicketIDRunsUnderACutName(t *testing.T) {
	t.Parallel()
	const id = "platform-backend-services-core-a3f.12.34" // 40 characters
	for _, launch := range []bool{true, false} {
		h := newHarness(t)
		h.cfg.LaunchPrompt = launch
		h.herdr.agentName = cutName
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
	if st, _ := h.beads.Status(context.Background(), "A"); st != "deferred" || !strings.Contains(h.beads.notesOf("A"), "never started on its prompt") {
		t.Errorf("A is %s, notes %q", st, h.beads.notesOf("A"))
	}
	if !strings.Contains(h.mainLog(), "B: add b.txt") {
		t.Error("B should be merged")
	}
}
