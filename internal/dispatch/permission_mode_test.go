package dispatch

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// claudeHelp is Claude Code 2.1.301's --help around --permission-mode, which lists auto but not
// default, and the option after it, whose choices aren't permission modes.
const claudeHelp = `Usage: claude [options] [command] [prompt]

Options:
  --no-chrome                           Disable Claude in Chrome integration
  --permission-mode <mode>              Permission mode to use for the session
                                        (choices: "acceptEdits", "auto",
                                        "bypassPermissions", "manual",
                                        "dontAsk", "plan")
  --permission-prompts <target>         Who answers permission prompts with
                                        --print: "host" (the SDK host or
                                        --permission-prompt-tool) or "none"
                                        (choices: "host", "none", default:
                                        "host")
  -v, --version                         Output the version number

Commands:
  doctor                                Check the health of your Claude Code
`

// olderHelp is the --permission-mode of a claude from before auto, on one line.
const olderHelp = `Options:
  --permission-mode <mode>  Permission mode to use for the session (choices: "acceptEdits", "bypassPermissions", "default", "plan")
  --resume [sessionId]      Resume a conversation
`

// fakeHelp is a claude whose --help prints help, or fails with err. asked counts its calls, which
// the run makes before any worker starts and the test reads once it has returned.
type fakeHelp struct {
	help  string
	err   error
	asked int
}

func (f *fakeHelp) Help(context.Context) (string, error) {
	f.asked++
	return f.help, f.err
}

// startsIn reports whether args, as fakeHerdr records a start, start the worker in permission mode
// mode.
func startsIn(args []string, mode string) bool {
	i := slices.Index(args, "--permission-mode")
	return i >= 0 && i+1 < len(args) && args[i+1] == mode
}

// permissionWarnings are the run's PERMISSION_MODE warnings.
func permissionWarnings(h *harness) []string {
	var l []string
	for _, ev := range h.sink.of(EvWarn) {
		if strings.HasPrefix(ev, "PERMISSION_MODE:") {
			l = append(l, ev)
		}
	}
	return l
}

func TestListsPermissionMode(t *testing.T) {
	for _, tc := range []struct {
		help, mode string
		want       bool
	}{
		{claudeHelp, "auto", true},
		{claudeHelp, "acceptEdits", true},
		{claudeHelp, "plan", true},
		{claudeHelp, "default", false},
		{claudeHelp, "host", false}, // the next option's
		{claudeHelp, "none", false},
		{olderHelp, "default", true},
		{olderHelp, "auto", false},
		{"Options:\n  --print  Print the response and exit\n", "auto", false},
		{`  --permission-mode <mode>  (choices: "auto")`, "auto", true}, // the last line, unended
		{`  --permission-mode <mode>  (choices: "autonomous")`, "auto", false},
	} {
		if got := listsPermissionMode(tc.help, tc.mode); got != tc.want {
			t.Errorf("%s in %q: %v, want %v", tc.mode, tc.help, got, tc.want)
		}
	}
}

// A run with a permission mode its claude lists starts every Claude worker in it, the START line
// naming it: the one resuming a gone worker's session too. claude is asked once.
func TestWorkersStartInThePermissionMode(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		help := &fakeHelp{help: claudeHelp}
		h.help, h.cfg.WorkerPermissionMode = help, "auto"
		h.cfg.WorkerArgs = []string{"--no-chrome"}
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", vanishes, finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		a, b := h.herdr.argsFor("A"), h.herdr.argsFor("B")
		if len(a) != 2 || !resumedWith(a[1], testSession) {
			t.Fatalf("A's starts %q; want a new worker, then one resuming %s", a, testSession)
		}
		for _, args := range append(a, b...) {
			if !startsIn(args, "auto") || args[1] != "--no-chrome" {
				t.Errorf("a worker started with %q, want --no-chrome and --permission-mode auto", args)
			}
		}
		if ev := h.sink.text(); !strings.Contains(ev, ", agent: claude, permission mode: auto, worktrees: ") {
			t.Errorf("the START line should name the mode:\n%s", ev)
		}
		if w := permissionWarnings(h); len(w) > 0 || help.asked != 1 {
			t.Errorf("claude asked %d times, warnings %q; want once, none", help.asked, w)
		}
	})
}

// A claude whose --help doesn't list the mode would refuse it, so the run says so once and starts
// its workers without it.
func TestWorkersStartWithoutAModeClaudeDoesNotList(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		help := &fakeHelp{help: olderHelp}
		h.help, h.cfg.WorkerPermissionMode = help, "auto"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, args := range append(h.herdr.argsFor("A"), h.herdr.argsFor("B")...) {
			if slices.Contains(args, "--permission-mode") {
				t.Errorf("a worker started with %q, want no --permission-mode", args)
			}
		}
		want := "PERMISSION_MODE: claude --help doesn't list the permission mode auto; workers start without it, " +
			"in the mode Claude Code's settings give (claude update may bring it)"
		if w := permissionWarnings(h); !equal(w, []string{want}) || help.asked != 1 {
			t.Errorf("claude asked %d times, warnings %q\nwant once, %q", help.asked, w, want)
		}
	})
}

// A claude whose --help fails can't be told to take the mode either: the run says why, and starts
// its workers without it.
func TestWorkersStartWithoutAModeWhenClaudeCannotBeAsked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.help, h.cfg.WorkerPermissionMode = &fakeHelp{err: errors.New("exec: \"claude\": not found")}, "acceptEdits"
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if a := h.herdr.argsFor("A"); len(a) != 1 || slices.Contains(a[0], "--permission-mode") {
			t.Errorf("A's starts %q, want one without --permission-mode", a)
		}
		want := "PERMISSION_MODE: cannot read claude --help to tell whether it takes the permission mode acceptEdits " +
			"(exec: \"claude\": not found); workers start without it, in the mode Claude Code's settings give"
		if w := permissionWarnings(h); !equal(w, []string{want}) {
			t.Errorf("warnings %q\nwant %q", w, want)
		}
	})
}

// Without a mode, workers start as before, in Claude Code's own default, claude asked only for the
// tools they go without. Another kind of agent gets no mode, as it isn't Claude Code's, and the START
// line says so; its claude isn't asked.
func TestNoPermissionModeAsksNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, mode, label string
		asked             int
	}{
		{"claude", "", ", agent: claude, worktrees: ", 1},
		{"codex", "auto", ", agent: codex, permission mode: not passed to codex workers, worktrees: ", 0},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			help := &fakeHelp{help: claudeHelp}
			h.help, h.cfg.AgentKind, h.cfg.WorkerPermissionMode = help, tc.kind, tc.mode
			h.beads.add("A", "first", 1)
			h.worker("A", finishes("a.txt"))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("%s: exit %d, final %q\n%s", tc.kind, code, o.Final(), h.sink.text())
			}
			for _, args := range h.herdr.argsFor("A") {
				if slices.Contains(args, "--permission-mode") {
					t.Errorf("%s: a worker started with %q", tc.kind, args)
				}
			}
			if ev := h.sink.text(); !strings.Contains(ev, tc.label) || help.asked != tc.asked || len(permissionWarnings(h)) > 0 {
				t.Errorf("%s: claude asked %d times, events:\n%s", tc.kind, help.asked, ev)
			}
		})
	}
}

// The probe is a worker without a ticket, and starts in the workers' permission mode too.
func TestTheProbeStartsInThePermissionMode(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.help, h.cfg.WorkerPermissionMode = &fakeHelp{help: claudeHelp}, "auto"
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		h.worker(probeID, runsProbe)
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		want := []string{"start", "--strict-mcp-config", "--permission-mode", "auto"}
		if got := h.herdr.argsFor(probeID); len(got) != 1 || !equal(got[0], want) {
			t.Errorf("probe started with %q, want once with %q", got, want)
		}
	})
}
