//go:build darwin || linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"golang.org/x/sys/unix"
)

// In a terminal, "Create from scratch" files the epic and the solo ticket that sets the tests up,
// and init's summary lists them.
func TestInitFromScratchFilesTheTestWorkAndListsIt(t *testing.T) {
	tty, master := openTerminal(t) // first, so that go test -short skips the test before any setup
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 120}); err != nil {
		t.Fatal(err)
	}
	tools := fakeBeadsTools(t, false) // a PATH without the machine's bd
	faketool.Write(t, tools, "claude", "#!/bin/sh\n[ \"$1\" = --help ] && exit 0\ncat >/dev/null\n"+
		`echo '{"type":"result","is_error":false,"structured_output":{"suites":[],"note":""}}'`+"\n")
	bd := trackerBd(t, false)
	repo := initRepo(t)
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

	for _, step := range []struct{ on, keys string }{
		{"┃ Tickets at the same time", keyEnter},
		{"> Create from scratch", keyEnter},
		{"┃ check-fast time limit", keyEnter},
		{"┃ check-full time limit", keyEnter},
	} {
		term.waitFor(t, step.on)
		term.typeKeys(t, step.keys, false)
	}
	select {
	case code := <-done:
		done <- code // for the cleanup
		if code != dispatch.ExitOK {
			t.Fatalf("exit %d, want %d; stderr:\n%s", code, dispatch.ExitOK, errOut.String())
		}
	case <-time.After(patience):
		t.Fatalf("init didn't exit; the terminal:\n%s", term.screen.String())
	}
	term.waitFor(t, "filed t-2 (P1, solo): Set up the test harness and a first suite")
	if !strings.Contains(term.screen.String(), "filed t-1 (epic): The project's tests") {
		t.Errorf("the summary doesn't list the epic:\n%s", term.screen.String())
	}
	hasArgs(t, "t-2", created(t, bd, "t-2"), "--priority=1", "--parent=t-1", "--labels=orchestra-tests,solo")
}
