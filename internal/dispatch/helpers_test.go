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
	noLeaks(t) // checked once the workers below have returned
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
	o.wait = timing{poll: time.Millisecond, startRetry: time.Millisecond, adopt: patience, blocked: 30 * time.Millisecond,
		idleGrace: 30 * time.Millisecond, startGrace: 30 * time.Millisecond}
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
