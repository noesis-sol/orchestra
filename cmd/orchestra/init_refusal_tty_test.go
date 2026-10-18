//go:build darwin || linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/sys/unix"
)

// A run that starts while init's form is answered stops init once it is: init looks again before
// it changes anything.
func TestInitIsRefusedByARunStartedDuringTheForm(t *testing.T) {
	tty, master := openTerminal(t) // first, so that go test -short skips the test before any setup
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 120}); err != nil {
		t.Fatal(err)
	}
	tools := fakeBeadsTools(t, true)
	repo, git := gitRepo(t)
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
	args := []string{"--check", "make check", "--check-timeout", "5m", "--check-full-timeout", "5m", "--mcp", "",
		"--setup", "", "--changelog-union=false", "--install-beads=false", "--verifier=false",
		"--prefix", project.DefaultPrefix(repo)} // all but -c
	go func() {
		done <- runInit(testCtx(t, ctx), repo, args, func(k string) string { return env[k] }, tty, tty, &errOut)
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

	term.waitFor(t, "┃ Tickets at the same time")
	holdLock(t, repo, project.Holder{PID: 44497, Branch: "main"})
	term.typeKeys(t, keyEnter, false)
	select {
	case code := <-done:
		done <- code // for the cleanup
		want := "orchestra init: orchestra is already running in "
		if code != dispatch.ExitSetup || !strings.Contains(errOut.String(), want) ||
			!strings.Contains(errOut.String(), "(pid 44497, main). init changes files") {
			t.Errorf("exit %d, want %d; stderr:\n%s", code, dispatch.ExitSetup, errOut.String())
		}
	case <-time.After(patience):
		t.Fatalf("init didn't exit; the terminal:\n%s", term.screen.String())
	}
	assertUnchanged(t, repo, tools, git)
}
