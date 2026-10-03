//go:build darwin || linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/sys/unix"
)

// runOnTerminal starts orchestra with these arguments in a repository set up for it, inside a
// Herdr pane, without notifications, triage or the run report, on a pseudo-terminal: its input and
// output. It returns the terminal, the repository, and what waits for orchestra to exit with its
// exit code and stderr.
func runOnTerminal(t *testing.T, args ...string) (term *fakeTerminal, repo string, exit func() (int, string)) {
	t.Helper()
	repo = configFixture(t, `{"concurrent": 1}`)
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0", "ORCHESTRA_TICKETS": ""} {
		t.Setenv(k, v)
	}
	tty, master := openTerminal(t)
	// Sized, as a Herdr pane is: Bubble Tea draws nothing on a terminal 0 wide.
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 100}); err != nil {
		t.Fatal(err)
	}
	term = &fakeTerminal{keys: master}
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
	var errOut strings.Builder
	done := make(chan int, 1)
	go func() {
		err := run(context.Background(), append([]string{"orchestra"}, args...), os.Getenv, tty, tty, &errOut)
		done <- exitOf(err)
	}()
	return term, repo, func() (int, string) {
		t.Helper()
		select {
		case code := <-done:
			return code, errOut.String()
		case <-time.After(20 * time.Second):
			t.Fatalf("orchestra didn't exit; the terminal:\n%s", term.screen.String())
			return 0, ""
		}
	}
}

// In a terminal, a run with nothing said asks what to work on; the feature described goes to the
// --feature flow: screened, planned, shown, and filed if confirmed.
func TestRunAsksWhatToWorkOnAndPlansTheDescribedFeature(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	term, _, exit := runOnTerminal(t)
	term.waitFor(t, "> Current tickets: 0 ready")
	term.typeKeys(t, keyDown+keyEnter, false)
	term.waitFor(t, describing)
	term.typeKeys(t, "Add a --json flag"+keyNewLine+"to the list command"+keyEnter, false)
	term.waitFor(t, "File these 2 tickets and start the run? [y/N]")
	term.typeKeys(t, "n\n", false)
	code, stderr := exit()
	out := strings.ReplaceAll(term.screen.String(), "\r\n", "\n")
	if code != dispatch.ExitOK || stderr != "" {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	for _, want := range []string{"New feature:\n  Add a --json flag\n  to the list command\n",
		"screening the request with claude…", "Epic: JSON output", "Nothing was filed."} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, out)
		}
	}
	if in := read(t, filepath.Join(dir, "in.1")); !strings.Contains(in, "Add a --json flag\nto the list command") {
		t.Errorf("the screen organ wasn't given the description:\n%s", in)
	}
	// The count comes from the run's own ready query.
	if calls := read(t, filepath.Join(dir, "bd-calls")); !strings.HasPrefix(calls,
		"ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|\n") {
		t.Errorf("bd calls:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "bd-n")); !os.IsNotExist(err) {
		t.Errorf("bd filed something: %v", err)
	}
}

// Esc at the question ends orchestra with nothing done: nothing screened, no run recorded.
func TestRunStoppedAtTheQuestionDoesNothing(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	term, repo, exit := runOnTerminal(t)
	term.waitFor(t, "> Current tickets: 0 ready")
	term.typeKeys(t, keyDown+keyEnter, false)
	term.waitFor(t, describing)
	term.typeKeys(t, keyEsc, false)
	code, stderr := exit()
	if code != dispatch.ExitInterrupted || stderr != "orchestra: stopped before the run started; nothing was changed.\n" {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("screened after Esc: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(repo, project.RunPath(dispatch.EventsName))); err == nil && len(b) > 0 {
		t.Errorf("a run was recorded:\n%s", b)
	}
}
