//go:build darwin || linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/sys/unix"
)

// scoutFoundTwoCosting is scoutFoundTwo with what the call cost, as claude's result message says it.
var scoutFoundTwoCosting = strings.Replace(scoutFoundTwo, `"is_error":false`, `"subtype":"success",`+
	`"is_error":false,"total_cost_usd":0.0456,"num_turns":5,"session_id":"s-1"`, 1)

// initOnTerminal runs init in a terminal, with a claude that answers the scout with answer, and
// returns the terminal and a wait for init's exit code.
func initOnTerminal(t *testing.T, answer string) (*fakeTerminal, func() int) {
	t.Helper()
	tty, master := openTerminal(t) // first, so that go test -short skips the test before any setup
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 120}); err != nil {
		t.Fatal(err)
	}
	dir := fakeBeadsTools(t, true)
	faketool.Write(t, dir, "claude", "#!/bin/sh\n[ \"$1\" = --help ] && exit 0\ncat >/dev/null\necho '"+
		answer+"'\n")
	repo, _ := gitRepo(t)
	term := &fakeTerminal{keys: master}
	go func() { // until the terminal closes, as the test ends
		b := make([]byte, 4096)
		for {
			n, err := master.Read(b)
			_, _ = term.screen.Write(b[:n]) // into memory
			if err != nil {
				return
			}
		}
	}()
	env := map[string]string{"HOME": t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var errOut strings.Builder
	go func() {
		done <- runInit(ctx, repo, []string{"--mcp", ""}, func(k string) string { return env[k] }, tty, tty, &errOut)
	}()
	t.Cleanup(func() { // init still asking as the test ends, as one that fails a wait does
		cancel()
		_, _ = master.WriteString(keyCtrlC)
		select {
		case <-done:
		case <-time.After(patience):
			t.Errorf("init didn't stop as the test ended; the terminal:\n%s", term.screen.String())
		}
	})
	return term, func() int {
		t.Helper()
		select {
		case code := <-done:
			done <- code // for the cleanup
			return code
		case <-time.After(patience):
			t.Fatalf("init didn't exit; the terminal:\n%s; stderr:\n%s", term.screen.String(), errOut.String())
			return 0
		}
	}
}

// scoutLine is the line of the screen that says what the scout cost, as a terminal shows it (what
// follows its last carriage return), or "".
func scoutLine(term *fakeTerminal) string {
	for l := range strings.Lines(term.screen.String()) {
		l = strings.TrimRight(l, "\r\n")
		if strings.Contains(l, "cost $") {
			return strings.TrimSpace(l[strings.LastIndexByte(l, '\r')+1:])
		}
	}
	return ""
}

// init has no log for the scout's ORGAN line: its summary says what the scout cost.
func TestInitSaysWhatTheScoutCost(t *testing.T) {
	term, exit := initOnTerminal(t, scoutFoundTwoCosting)
	for _, step := range []struct{ on, keys string }{
		{"┃ Tickets at the same time", keyEnter},
		{"┃ The project's checks", ""},
		{"> Use them as they are", keyEnter},
		{"┃ On every merge (check-fast)", keyEnter},
		{"┃ At the end of a run (check-full)", keyEnter},
		{"┃ Also file tickets for untested areas", keyEnter},
		{"┃ check-fast time limit", keyEnter},
		{"┃ check-full time limit", keyEnter},
	} {
		term.waitFor(t, step.on)
		term.typeKeys(t, step.keys, false)
	}
	if code := exit(); code != dispatch.ExitOK {
		t.Fatalf("exit %d, want %d; the terminal:\n%s", code, dispatch.ExitOK, term.screen.String())
	}
	term.waitFor(t, " ♪ ") // the sign-off, init's last line
	line := scoutLine(term)
	if !strings.HasPrefix(line, "✓ scout") || !strings.Contains(line, "cost $0.0456 in 5 turns, ") ||
		!strings.HasSuffix(line, ", session s-1") {
		t.Errorf("the scout's line is %q, want ✓ scout and its cost; the terminal:\n%s", line, term.screen.String())
	}
}

// Cancelled, init changes nothing, but the scout's call was paid for all the same.
func TestCancelledInitSaysWhatTheScoutCost(t *testing.T) {
	term, exit := initOnTerminal(t, scoutFoundTwoCosting)
	term.waitFor(t, "┃ Tickets at the same time")
	term.typeKeys(t, keyEnter, false)
	term.waitFor(t, "> Use them as they are")
	term.typeKeys(t, keyCtrlC, false)
	if code := exit(); code != dispatch.ExitSetup {
		t.Fatalf("exit %d, want %d; the terminal:\n%s", code, dispatch.ExitSetup, term.screen.String())
	}
	term.waitFor(t, "Cancelled; nothing was changed.")
	if line := scoutLine(term); !strings.HasPrefix(line, "✓ scout") || !strings.Contains(line, "cost $0.0456") {
		t.Errorf("the scout's line is %q, want ✓ scout and its cost; the terminal:\n%s", line, term.screen.String())
	}
}

// A scout that ended in an error is to watch, and says how it ended; one that never answered has no
// line.
func TestScoutStepsMarkAnErrorToWatch(t *testing.T) {
	if steps := scoutSteps(nil); len(steps) != 0 {
		t.Errorf("no call gave %v, want no step", steps)
	}
	steps := scoutSteps([]organ.Spend{{Organ: "scout", CostUSD: 0.5, Turns: 30, Subtype: organ.SubtypeMaxBudget,
		Took: 90 * time.Second}})
	if len(steps) != 1 {
		t.Fatalf("one call gave %v, want one step", steps)
	}
	s := steps[0]
	if s.Kind != project.StepCaution || s.Label != "scout" ||
		s.Detail != "cost $0.5000 in 30 turns, 1m30s, "+string(organ.SubtypeMaxBudget) {
		t.Errorf("the step is %+v, want scout to watch, with its cost and subtype", s)
	}
}
