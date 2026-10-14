package dispatch_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/cmux"
	"github.com/noesis-sol/orchestra/internal/cmuxtest"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The scenarios in this file are whole runs as orchestra makes them with workers in cmux: the loop on
// the real cmux (an app of the test's own, cmuxtest.Start), and otherwise as the tmux runs of
// tmux_run_test.go are made, with the same repository, tracker and fake Claude Code. The app's first
// workspace stands for orchestra's own, where it would run, and a workspace of the user's is opened
// beside it, which the run must leave alone.

// cmuxRun is a run whose workers are in cmux.
type cmuxRun struct {
	*tmuxRun
	app  *cmuxtest.App
	term cmux.Terminal
	mine string // the user's workspace
}

// newCmuxRun is newTmuxRun's run with the workers in cmux.
func newCmuxRun(t *testing.T, script, resumed []string) *cmuxRun {
	t.Helper()
	app := cmuxtest.Start(t)
	r := &cmuxRun{tmuxRun: newRunFixture(t, script, resumed), app: app}
	r.term = cmux.Terminal{Book: filepath.Join(r.repo, project.RunPath(cmux.BookName)), Own: app.Own}
	r.terminal, r.kind = r.term, project.TerminalCmux
	var made struct {
		Workspace string `json:"workspace_id"`
	}
	if err := json.Unmarshal([]byte(app.CLI("--json", "workspace", "create", "--name", "mine", "--focus", "false")),
		&made); err != nil || made.Workspace == "" {
		t.Fatalf("the user's workspace: %v", err)
	}
	r.mine = strings.ToUpper(made.Workspace)
	return r
}

// start runs the loop in the background; its exit code comes on the channel.
func (r *cmuxRun) start(ctx context.Context, o *dispatch.Loop) <-chan int {
	done := make(chan int, 1)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		done <- o.Run(ctx)
	}()
	return done
}

// await waits for cond while the run goes on, failing the test if the run ends first, or after a
// minute.
func (r *cmuxRun) await(what string, done <-chan int, cond func() bool) {
	r.t.Helper()
	deadline := time.After(time.Minute)
	for !cond() {
		select {
		case code := <-done:
			r.t.Fatalf("the run ended (%d) before %s:\n%s\nthe worker's log:\n%s", code, what, r.read(r.log),
				r.read(filepath.Join(r.claudeDir, "log")))
		case <-deadline:
			r.t.Fatalf("%s didn't happen within a minute; the worker's states: %v\nthe run's log:\n%s", what,
				r.sink.states(), r.read(r.log))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// workerTab is the workspace named tx-1, "" if there is none.
func (r *cmuxRun) workerTab(tree cmuxtest.Tree) string {
	for _, id := range tree.Order {
		if tree.Titles[id] == "tx-1" {
			return id
		}
	}
	return ""
}

// workspacesLeft checks that the run left orchestra's own and the user's workspaces, and want, the
// workspaces of tx-1's earlier workers, by UUID, in any order: cmux restores them in its own.
func (r *cmuxRun) workspacesLeft(want ...string) {
	r.t.Helper()
	want = slices.Sorted(slices.Values(append([]string{r.app.Own, r.mine}, want...)))
	if got := slices.Sorted(slices.Values(r.app.Tree().Order)); !slices.Equal(got, want) {
		r.t.Errorf("workspaces left: %v, want %v", got, want)
	}
}

// promptGiven checks that the worker got its prompt whole, of several lines: pasted by a cmux that
// pastes (0.65.0 and later), else through a file it attached.
func (r *cmuxRun) promptGiven(n int, want string) {
	r.t.Helper()
	got := r.read(filepath.Join(r.claudeDir, "prompt-"+strconv.Itoa(n)))
	if !strings.Contains(got, want) || !strings.Contains(got, "\n") {
		r.t.Errorf("prompt %d: %q, want one of several lines with %q", n, got, want)
	}
	attached := strings.Contains(r.read(filepath.Join(r.claudeDir, "log")), "attached .orchestra/run/cmux-prompt-")
	if pastes := !strings.HasPrefix(r.app.Version(), "cmux 0.64."); pastes == attached {
		r.t.Errorf("cmux %s: the worker attached a prompt file: %v", strings.TrimSpace(r.app.Version()), attached)
	}
}

// A ticket goes through a run in cmux: its worker's workspace opens named with its ID, without
// switching to it; the worker is given its prompt, works on it, claims, commits and closes its ticket,
// and settles at its Stop hook; the ticket is merged and its workspace closed, and only it.
func TestCmuxRunMergesATicket(t *testing.T) {
	// A turn of a few seconds, which reading the worker's state through cmux 0.65's top, at times
	// a second or two, can't miss.
	r := newCmuxRun(t, slices.Concat(claimed, []string{"sleep 3s"}, work), nil)
	done := r.start(context.Background(), r.loop())
	var tree cmuxtest.Tree
	r.await("the worker's workspace opened", done, func() bool {
		tree = r.app.Tree()
		return r.workerTab(tree) != ""
	})
	if tree.Selected != r.app.Own {
		t.Errorf("the workspace in view is %s (%s), want orchestra's own", tree.Selected, tree.Titles[tree.Selected])
	}
	r.merged(<-done)
	r.promptGiven(1, "Your ticket is tx-1.")
	if logged := r.read(r.log); !strings.Contains(logged, "tx-1 settled: Stop hook at") {
		t.Errorf("the run's log:\n%s", logged)
	}
	if !r.sink.saw(dispatch.StateWorking) || !r.sink.saw(dispatch.StateIdle) {
		t.Errorf("the worker's states: %v", r.sink.states())
	}
	r.workspacesLeft()
}

// A worker that waits on a permission prompt is blocked, as the dashboard shows, until it is allowed
// in its workspace; then it finishes its ticket, which merges.
func TestCmuxRunWaitsOnAPermissionPrompt(t *testing.T) {
	r := newCmuxRun(t, slices.Concat(claimed, []string{"write feature.txt the feature",
		`permission git add feature.txt && git commit -q -m "$ID: Add the feature"`, `bash bd close "$ID"`, "stop",
		"idle"}), nil)
	done := r.start(context.Background(), r.loop())
	r.await("the worker was shown blocked on its permission prompt", done, r.sink.permission)
	if err := r.term.SendKeys(context.Background(), "tx-1", "1", "enter"); err != nil {
		t.Fatal(err)
	}
	r.merged(<-done)
	if !r.sink.saw(dispatch.StateBlocked) {
		t.Errorf("never shown blocked: %v", r.sink.states())
	}
	r.workspacesLeft()
}

// Ctrl+C while a worker works leaves its workspace, and the worker in it, running; the next run
// carries it over by its workspace's UUID, and merges its ticket once the worker is done.
func TestCmuxRunCarriesAWorkerOverAfterCtrlC(t *testing.T) {
	r := newCmuxRun(t, slices.Concat(claimed, []string{"sleep 3s"}, work), nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := r.start(ctx, r.loop())
	r.await("the worker claimed its ticket", done, func() bool {
		return strings.Contains(r.read(filepath.Join(r.bdDir, "calls")), "update|tx-1|--claim|")
	})
	stop()
	if code := <-done; code != dispatch.ExitInterrupted {
		t.Fatalf("the run stopped with Ctrl+C ended %d, want %d:\n%s", code, dispatch.ExitInterrupted, r.read(r.log))
	}
	tab := r.workerTab(r.app.Tree())
	if tab == "" {
		t.Fatalf("Ctrl+C closed the worker's workspace:\n%s", r.read(r.log))
	}
	r.merged(r.run(r.loop()))
	if logged := r.read(r.log); !strings.Contains(logged, "carried over from the last run: tx-1") {
		t.Errorf("the second run's log doesn't carry tx-1 over:\n%s", logged)
	}
	if got := strings.Count(r.read(filepath.Join(r.claudeDir, "log")), "session "); got != 1 {
		t.Errorf("the worker's sessions: %d, want the one carried over\n%s", got, r.read(filepath.Join(r.claudeDir, "log")))
	}
	r.workspacesLeft()
}

// cmux quitting while a worker works ends the worker. Started again, cmux restores the worker's
// workspace, but with a shell in it, not the worker; the next run resumes the worker's session in a
// new workspace and merges its ticket.
func TestCmuxRunResumesAWorkerAfterCmuxQuits(t *testing.T) {
	r := newCmuxRun(t, slices.Concat(claimed, []string{"sleep 2m"}),
		slices.Concat([]string{"start", "prompt", "sleep 1s"}, work))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := r.start(ctx, r.loop())
	r.await("the worker claimed its ticket", done, func() bool {
		return strings.Contains(r.read(filepath.Join(r.bdDir, "calls")), "update|tx-1|--claim|")
	})
	tab := r.workerTab(r.app.Tree())
	r.app.AwaitSaved(tab)
	r.app.Quit()
	select {
	case code := <-done:
		t.Logf("with cmux gone, the run ended %d", code)
	case <-time.After(30 * time.Second):
		stop()
		t.Logf("with cmux gone, the run went on until stopped: %d", <-done)
	}
	r.app.Restart()
	if got := r.workerTab(r.app.Tree()); got != tab {
		t.Fatalf("cmux restored workspace %q as tx-1, want the worker's, %s", got, tab)
	}
	r.merged(r.run(r.loop()))
	log := r.read(filepath.Join(r.claudeDir, "log"))
	if !strings.Contains(log, " startup\n") || !strings.Contains(log, " resume\n") {
		t.Errorf("the worker's sessions, want one started and then resumed:\n%s", log)
	}
	if logged := r.read(r.log); !strings.Contains(logged, "RESUMED: tx-1's worker is gone") {
		t.Errorf("the run's log:\n%s", logged)
	}
	r.workspacesLeft(tab)
}
