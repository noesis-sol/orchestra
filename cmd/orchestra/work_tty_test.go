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

// endOfOutput is what the test writes on the terminal once orchestra has exited. The terminal keeps
// the order of what is written on it, so once the screen shows this, it shows all orchestra wrote.
const endOfOutput = "[orchestra exited]"

// cut takes text off the screen.
func (s *screen) cut(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, after, _ := strings.Cut(s.b.String(), text)
	s.b.Reset()
	s.b.WriteString(before + after)
}

// runOnTerminal starts orchestra with these arguments in a repository set up for it, inside a
// Herdr pane, without notifications, triage or the run report, on a pseudo-terminal: its input and
// output. It returns the terminal, the repository, and what waits for orchestra to exit, and for the
// screen to show all it wrote, with its exit code and stderr. orchestra still running as the test
// ends is stopped.
func runOnTerminal(t *testing.T, args ...string) (term *fakeTerminal, repo string, exit func() (int, string)) {
	t.Helper()
	tty, master := openTerminal(t) // first, so that go test -short skips the test before any setup
	repo = configFixture(t, `{"concurrent": 1}`)
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0", "ORCHESTRA_TICKETS": ""} {
		setenv(t, k, v)
	}
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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	ended := make(chan struct{}) // closed once run has returned
	go func() {
		defer close(ended)
		err := run(testCtx(ctx, t), append([]string{"orchestra"}, args...), testCmdEnv(t, tty, tty, &errOut))
		done <- exitOf(err)
	}()
	// A test that ends before orchestra exits, as one that fails a wait does, stops orchestra and waits
	// for it before the terminal closes and the environment, the working directory and the repository
	// go: orchestra still uses them. Registered after them, this cleanup runs first.
	t.Cleanup(func() {
		cancel()
		select {
		case <-ended:
		case <-time.After(patience):
			t.Errorf("orchestra didn't stop as the test ended; the terminal:\n%s", term.screen.String())
		}
	})
	return term, repo, func() (int, string) {
		t.Helper()
		var code int
		select {
		case code = <-done:
		case <-time.After(patience):
			t.Fatalf("orchestra didn't exit; the terminal:\n%s", term.screen.String())
		}
		// What orchestra wrote last may not have been read off the terminal yet.
		if _, err := tty.Write([]byte(endOfOutput)); err != nil {
			t.Fatal(err)
		}
		term.waitFor(t, endOfOutput)
		term.screen.cut(endOfOutput)
		return code, errOut.String()
	}
}

// Esc at the question ends orchestra with nothing done: nothing talked through, no run recorded.
func TestRunStoppedAtTheQuestionDoesNothing(t *testing.T) {
	t.Parallel()
	dir := featureTools(t)
	term, repo, exit := runOnTerminal(t)
	term.waitFor(t, "> Current tickets: none, all done")
	term.typeKeys(t, pickFeature, false)
	term.waitFor(t, describing)
	term.typeKeys(t, keyEsc, false)
	code, stderr := exit()
	if code != dispatch.ExitInterrupted || stderr != "orchestra: stopped before the run started; nothing was changed.\n" {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("claude ran after Esc: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(repo, project.RunPath(dispatch.EventsName))); err == nil && len(b) > 0 {
		t.Errorf("a run was recorded:\n%s", b)
	}
}
