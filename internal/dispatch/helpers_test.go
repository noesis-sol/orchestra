package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// gitRepo makes a repository with one commit and returns its path and a git runner.
func gitRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := command.Output(context.Background(), 0, dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git(repo, "init", "-q")
	git(repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo, git
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Whole runs, with Beads and Herdr faked and git real, or in memory (newTimedHarness).

type harness struct {
	t        *testing.T
	repo     string
	git      func(dir string, args ...string) string // nil with git in memory
	mem      *fakeGit                                // git in memory, or nil
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
	noLeaks(t) // checked once the workers below have returned
	repo, run := gitRepo(t)
	run(repo, "branch", "-M", "main")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".orchestra/\n"), 0o644); err != nil { // the launch prompt
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "ignore .orchestra")
	h := harnessIn(t, repo)
	h.git = run
	return h
}

// newTimedHarness is newHarness with git in memory, for scenarios whose subject is timing rather
// than git. It is called inside synctest.Test: the loop then waits as long as it would in a real
// run (10 minutes' idle grace, a ready poll every 30 seconds, …) on the bubble's clock, which moves
// on whenever every goroutine in the run waits, so a test takes no longer, and no less, however
// loaded the machine is. A worker waits on that clock too: time.Sleep lets the run go on that long.
func newTimedHarness(t *testing.T) *harness {
	t.Helper()
	noLeaks(t)
	h := harnessIn(t, t.TempDir())
	h.mem = newFakeGit("main")
	h.herdr.git = h.mem
	return h
}

// harnessIn sets up a run of the repository in repo with an empty tracker.
func harnessIn(t *testing.T, repo string) *harness {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	beads := newFakeBeads()
	h := &harness{t: t, repo: repo, beads: beads, herdr: newFakeHerdr(t, beads),
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
	d := Deps{Tickets: h.beads, Notes: h.beads, Tabs: h.herdr, Starter: h.herdr, Namer: h.herdr, Agents: h.herdr,
		Reporter: h.reporter, Checkout: git.Git{}, Worktrees: git.Git{}, Merger: git.Git{}, History: git.Git{},
		Advisor: organ.Client{Bin: filepath.Join(h.t.TempDir(), "no-claude")}, AdviceCtx: context.Background()}
	if h.mem != nil {
		d.Checkout, d.Worktrees, d.Merger, d.History = h.mem, h.mem, h.mem, h.mem
	}
	o := New(h.cfg, log, "Work on TICKET_ID.", d)
	o.SetSink(h.sink)
	if h.mem == nil {
		o.poll = time.Millisecond // git takes real time: the loop's clock is the real one
	}
	return o
}

func (h *harness) run() (*Loop, int) {
	o := h.loop()
	return o, o.Run(context.Background())
}

func (h *harness) mainLog() string {
	if h.mem != nil {
		return h.mem.log("main")
	}
	return h.git(h.repo, "log", "--oneline", "main")
}

func (h *harness) worktree(id string) string { return filepath.Join(h.cfg.WTRoot, id) }

func (h *harness) logged() string { return read(h.t, h.logPath) }

// waitHeld waits for the first HOLD, as a worker still running when another stops the run.
func (h *harness) waitHeld() {
	select {
	case <-h.sink.held:
	case <-time.After(patience):
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

// patience is how long a test waits for something that should happen: a passing test never waits
// it out, and a loaded machine (the race detector, many test binaries at once) can take tens of
// seconds for what takes a second alone.
const patience = 2 * time.Minute

// eventually waits for cond, failing the test after patience.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(patience); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Error(what)
			return
		}
	}
}
