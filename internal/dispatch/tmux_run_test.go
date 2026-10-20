package dispatch_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/claudetest"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tmux"
	"github.com/noesis-sol/orchestra/internal/tmuxtest"
)

// The scenarios in this file are whole runs as orchestra makes them with workers in tmux: the loop
// on the real tmux (a server of the test's own, tmuxtest.Server), Beads through bd, real git, and
// workers' reports through the hooks orchestra gives them. The workers are a fake Claude Code
// (claudetest.Claude) that runs those hooks as Claude Code would, and the tracker a fake bd that keeps
// one ticket, tx-1, which the worker claims and closes. Herdr's runs are tested through the fakes.

var _ = faketool.Func("claude", claudetest.Claude)

// tmuxRun is a run's repository, tracker, tmux and worker; or cmux, for a cmuxRun.
type tmuxRun struct {
	t    *testing.T
	repo string
	// ctx gives the commands the run and the test run their environment (command.WithEnv): the fake bd
	// and claude first on PATH and, in tmux, the test's tmux server; the process's is left as it is,
	// so the scenarios run in parallel.
	ctx       context.Context
	bdDir     string // the fake bd's folder: status, the ticket's; calls, each call's arguments
	claudeDir string // the fake claude's folder (claudetest.Claude)
	term      tmux.Terminal
	log       string
	sink      *tmuxSink
	// The workers' terminal, as the loop is given it, which it is set in, and the workspace it opens
	// their tabs in.
	terminal  runTerminal
	kind      string // project.TerminalTmux or TerminalCmux
	workspace string
}

// runTerminal is a terminal as a run uses it for its workers.
type runTerminal interface {
	dispatch.Tabs
	dispatch.Starter
	dispatch.Namer
	dispatch.Agents
}

// newTmuxRun sets up a repository on main and a backlog of one open ticket, tx-1, whose worker takes
// the steps of script (claudetest.Claude), and of resumed when its session is resumed.
func newTmuxRun(t *testing.T, script, resumed []string) *tmuxRun {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped by -short: a whole run in a real tmux")
	}
	r := newRunFixture(t, script, resumed)
	r.ctx = tmuxtest.ServerContext(r.ctx, t, "orch", r.repo)
	r.term = tmux.Terminal{Socket: tmuxtest.Socket}
	r.terminal, r.kind, r.workspace = r.term, project.TerminalTmux, "orch"
	return r
}

// newRunFixture sets up the repository and backlog of newTmuxRun, with no terminal.
func newRunFixture(t *testing.T, script, resumed []string) *tmuxRun {
	t.Helper()
	bin := t.TempDir()
	r := &tmuxRun{t: t, repo: t.TempDir(), bdDir: t.TempDir(), claudeDir: t.TempDir(),
		log: filepath.Join(t.TempDir(), "orchestra.log"), sink: &tmuxSink{},
		ctx: command.WithEnv(context.Background(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))}
	r.git(r.repo, "init", "-q", "-b", "main")
	r.write(filepath.Join(r.repo, ".gitignore"), ".orchestra/\n")
	r.git(r.repo, "add", ".")
	r.git(r.repo, "commit", "-q", "-m", "init")

	r.write(filepath.Join(r.bdDir, "status"), "open\n")
	faketool.Write(t, bin, "bd", `d='`+r.bdDir+`'
for a; do printf '%s|' "$a"; done >> "$d/calls"; echo >> "$d/calls"
st=$(cat "$d/status")
ticket() { printf '[{"id":"tx-1","title":"Add the feature","status":"%s","priority":2,"issue_type":"task"}]\n' "$st"; }
case "$1 $2" in
"ready "*) if [ "$st" = open ]; then ticket; else echo '[]'; fi ;;
"show tx-1") if [ "$3" = --json ]; then ticket; else echo "tx-1 · Add the feature [$st]"; fi ;;
"show "*) echo "Error: no issue found matching \"$2\"" >&2; exit 1 ;;
"list "*) case "$*" in *--parent*|*--label*|*"--status closed"*) echo '[]' ;;
	*) if [ "$st" = closed ]; then echo '[]'; else ticket; fi ;; esac ;;
"update tx-1") case "$*" in *--claim*) echo in_progress > "$d/status" ;; *"--status open"*) echo open > "$d/status" ;; esac ;;
"close tx-1") echo closed > "$d/status" ;;
esac
exit 0
`)
	faketool.WriteFunc(t, bin, "claude", claudetest.Env(r.claudeDir))
	r.write(filepath.Join(r.claudeDir, "script"), strings.Join(script, "\n")+"\n")
	r.write(filepath.Join(r.claudeDir, "script.resume"), strings.Join(resumed, "\n")+"\n")
	return r
}

func (r *tmuxRun) write(path, content string) {
	r.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *tmuxRun) read(path string) string {
	b, _ := os.ReadFile(path) // none yet reads as nothing
	return string(b)
}

func (r *tmuxRun) git(dir string, args ...string) string {
	r.t.Helper()
	out, err := command.Output(r.ctx, command.ReadLimit, dir, "git", args...)
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

// wtRoot is the folder of the run's worktrees, the same for each run of the test.
func (r *tmuxRun) wtRoot() string { return filepath.Join(filepath.Dir(r.log), "worktrees") }

// loop is the run's loop, as orchestra makes it for workers in tmux, polling its workers often.
func (r *tmuxRun) loop() *dispatch.Loop {
	r.t.Helper()
	log, err := dispatch.OpenLog(r.log, false, "t")
	if err != nil {
		r.t.Fatal(err)
	}
	tracker := beads.Tracker{Repo: r.repo}
	cfg := dispatch.Config{Repo: r.repo, Base: "main", Workspace: r.workspace, Terminal: r.kind, Limit: 10,
		Concurrency: 1, AgentKind: "claude", WTRoot: r.wtRoot(), LogPath: r.log, ProbeHooks: true}
	o := dispatch.New(cfg, log, "Work on TICKET_ID.", dispatch.Deps{
		Tickets: tracker, Notes: tracker, Tabs: r.terminal, Starter: r.terminal, Namer: r.terminal, Agents: r.terminal,
		Reporter: claude.Reporter{}, Checkout: git.Git{}, Worktrees: git.Git{}, Merger: git.Git{}, History: git.Git{},
		Advisor: organ.Client{Bin: filepath.Join(r.t.TempDir(), "no-claude")}, AdviceCtx: r.ctx})
	o.SetSink(r.sink)
	dispatch.SetPoll(o, 100*time.Millisecond)
	return o
}

// run runs the loop to its end, failing the test if it takes over 2 minutes, and returns its exit code.
func (r *tmuxRun) run(o *dispatch.Loop) int {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(r.ctx, 2*time.Minute)
	defer cancel()
	code := o.Run(ctx)
	if ctx.Err() != nil {
		r.t.Errorf("the run was still going after 2 minutes")
	}
	return code
}

// merged checks that the run ended with tx-1 closed and its worker's commit on main.
func (r *tmuxRun) merged(code int) {
	r.t.Helper()
	if code != dispatch.ExitOK {
		r.t.Errorf("exit code %d, want 0", code)
	}
	if st := strings.TrimSpace(r.read(filepath.Join(r.bdDir, "status"))); st != "closed" {
		r.t.Errorf("tx-1 is %s", st)
	}
	if got := r.git(r.repo, "log", "--format=%s", "main"); got != "tx-1: Add the feature\ninit\n" {
		r.t.Errorf("main's log:\n%s", got)
	}
	if got := r.read(filepath.Join(r.repo, "feature.txt")); got != "the feature\n" {
		r.t.Errorf("feature.txt on main: %q", got)
	}
	if r.t.Failed() {
		r.t.Logf("the run's log:\n%s\nthe worker's log:\n%s\nbd's calls:\n%s", r.read(r.log),
			r.read(filepath.Join(r.claudeDir, "log")), r.read(filepath.Join(r.bdDir, "calls")))
	}
}

// work is the worker's work on tx-1 once it has its prompt: a file written and committed, and the
// ticket closed, then the turn's end; it waits at its prompt after.
var work = []string{"write feature.txt the feature", `bash git add feature.txt && git commit -q -m "$ID: Add the feature"`,
	`bash bd close "$ID"`, "stop", "idle"}

// claimed is a worker's start on tx-1: its session started, its prompt pasted, and the ticket
// claimed after a while (the hooks show it working until its turn ends).
var claimed = []string{"start", "prompt", "sleep 1s", `bash bd update "$ID" --claim`}

// A ticket goes through a run in tmux: its worker starts in a window of the session, is pasted its
// prompt, works on it, claims, commits and closes its ticket, and settles at its Stop hook; the
// ticket is merged and its window closed.
func TestTmuxRunMergesATicket(t *testing.T) {
	t.Parallel()
	r := newTmuxRun(t, slices.Concat(claimed, work), nil)
	code := r.run(r.loop())
	r.merged(code)
	if got := r.read(filepath.Join(r.claudeDir, "prompt-1")); !strings.HasPrefix(got, "Your ticket is tx-1.") {
		t.Errorf("the pasted prompt: %q", got)
	}
	logged := r.read(r.log)
	if !strings.Contains(logged, "tx-1 settled: Stop hook at") {
		t.Errorf("the run's log:\n%s", logged)
	}
	if !r.sink.saw(dispatch.StateWorking) || !r.sink.saw(dispatch.StateIdle) {
		t.Errorf("the worker's states: %v", r.sink.states())
	}
	if windows := r.windows(); strings.Join(windows, " ") != "orch:" {
		t.Errorf("windows left: %q", windows)
	}
}

// A worker that waits on a permission prompt is blocked, as the dashboard shows and the log says,
// until it is allowed in its window; then it finishes its ticket, which merges.
func TestTmuxRunWaitsOnAPermissionPrompt(t *testing.T) {
	t.Parallel()
	r := newTmuxRun(t, slices.Concat(claimed, []string{"write feature.txt the feature",
		`permission git add feature.txt && git commit -q -m "$ID: Add the feature"`, `bash bd close "$ID"`, "stop",
		"idle"}), nil)
	o := r.loop()
	done := make(chan int)
	go func() { done <- r.run(o) }()
	deadline := time.After(time.Minute)
	for !r.sink.permission() {
		select {
		case code := <-done:
			t.Fatalf("the run ended (%d) without the worker blocked on its permission prompt:\n%s", code, r.read(r.log))
		case <-deadline:
			t.Fatalf("the worker was never shown blocked on its permission prompt; its states: %v", r.sink.states())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := r.term.SendKeys(r.ctx, "tx-1", "1", "enter"); err != nil {
		t.Fatal(err)
	}
	r.merged(<-done)
	if !r.sink.saw(dispatch.StateBlocked) {
		t.Errorf("never shown blocked: %v", r.sink.states())
	}
	if logged := r.read(r.log); !strings.Contains(logged, "PERMISSION_PROMPT: tx-1's worker waits on a permission prompt for Bash") {
		t.Errorf("the run's log:\n%s", logged)
	}
}

// A worker whose Claude Code exits with its ticket in progress is gone, its window left open and
// dead; its session is resumed in a new window, told to carry on, and finishes the ticket, which
// merges.
func TestTmuxRunResumesAWorkerGone(t *testing.T) {
	t.Parallel()
	r := newTmuxRun(t, slices.Concat(claimed, []string{"exit 1"}),
		slices.Concat([]string{"start", "prompt", "sleep 1s"}, work))
	code := r.run(r.loop())
	r.merged(code)
	log := r.read(filepath.Join(r.claudeDir, "log"))
	var sessions []string
	for line := range strings.Lines(log) {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "session" {
			sessions = append(sessions, f[1]+" "+f[2])
		}
	}
	if len(sessions) != 2 || !strings.HasSuffix(sessions[0], " startup") ||
		sessions[1] != strings.TrimSuffix(sessions[0], " startup")+" resume" {
		t.Fatalf("the sessions: %q, want one started and then resumed\n%s", sessions, log)
	}
	if got := r.read(filepath.Join(r.claudeDir, "prompt-2")); !strings.Contains(got, "Carry on with tx-1") {
		t.Errorf("the resumed worker's prompt: %q", got)
	}
	logged := r.read(r.log)
	id, _, _ := strings.Cut(sessions[0], " ")
	for _, want := range []string{"tx-1 settled: its worker is gone",
		"RESUMED: tx-1's worker is gone from tab @1 with the ticket in progress; resuming its session " + id,
		"tx-1 settled: Stop hook at"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the run's log lacks %q:\n%s", want, logged)
		}
	}
	if windows := r.windows(); strings.Join(windows, " ") != "orch: tx-1:dead" {
		t.Errorf("windows left: %q, want the first worker's, dead", windows)
	}
}

// windows lists the session's windows as name:dead, the first window's ("") being the session's own.
func (r *tmuxRun) windows() []string {
	r.t.Helper()
	out, err := command.Output(r.ctx, command.ReadLimit, "", "tmux", "-L", r.term.Socket,
		"list-windows", "-t", "orch", "-F", "#{?#{==:#{window_index},0},orch,#{window_name}}:#{?pane_dead,dead,}")
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Fields(out)
}

// tmuxSink keeps the statuses a run shows.
type tmuxSink struct {
	mu       sync.Mutex
	statuses []dispatch.Status
}

func (s *tmuxSink) Event(dispatch.Event) {}

func (s *tmuxSink) Status(st dispatch.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append(s.statuses, st)
}

// saw reports whether tx-1's worker was shown in state st.
func (s *tmuxSink) saw(st dispatch.AgentState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.statuses {
		if x.Ticket == "tx-1" && x.Agent == st {
			return true
		}
	}
	return false
}

// permission reports whether tx-1's worker was shown waiting on a permission prompt.
func (s *tmuxSink) permission() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.statuses {
		if x.Ticket == "tx-1" && x.Permission {
			return true
		}
	}
	return false
}

// states are the states tx-1's worker was shown in, in order, each once in a row.
func (s *tmuxSink) states() []dispatch.AgentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sts []dispatch.AgentState
	for _, x := range s.statuses {
		if x.Ticket == "tx-1" && x.Agent != "" && (len(sts) == 0 || sts[len(sts)-1] != x.Agent) {
			sts = append(sts, x.Agent)
		}
	}
	return sts
}
