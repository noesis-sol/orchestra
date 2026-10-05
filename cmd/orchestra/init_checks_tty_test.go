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
	"github.com/noesis-sol/orchestra/internal/faketool"
	"golang.org/x/sys/unix"
)

// scoutFoundTwo is claude's answer for a scout that finds a fast suite and a full one.
const scoutFoundTwo = `{"type":"result","is_error":false,"structured_output":{"suites":[` +
	`{"name":"unit tests","kind":"unit","command":"make test","found_in":"Makefile:3","tier":"fast",` +
	`"parallel_safe":true,"needs":[]},` +
	`{"name":"e2e","kind":"e2e","command":"make e2e","found_in":"Makefile:7","tier":"full",` +
	`"parallel_safe":false,"needs":["postgres"]}],"note":""}}`

// In a terminal, init's stage 2 looks for the project's suites and writes the runners from those
// chosen: here, the suites found, used as they are.
func TestInitWritesTheRunnersFromTheSuitesTheScoutFound(t *testing.T) {
	tty, master := openTerminal(t) // first, so that go test -short skips the test before any setup
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 120}); err != nil {
		t.Fatal(err)
	}
	dir := fakeBeadsTools(t, true)
	faketool.Write(t, dir, "claude", "#!/bin/sh\n[ \"$1\" = --help ] && exit 0\ncat >/dev/null\necho '"+
		scoutFoundTwo+"'\n")
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
	select {
	case code := <-done:
		done <- code // for the cleanup
		if code != dispatch.ExitOK {
			t.Fatalf("exit %d, want %d; stderr:\n%s", code, dispatch.ExitOK, errOut.String())
		}
	case <-time.After(patience):
		t.Fatalf("init didn't exit; the terminal:\n%s", term.screen.String())
	}
	for path, want := range map[string]string{
		"scripts/check-fast.sh": "\n# unit tests, from Makefile:3\nprintf '%s\\n' 'SUITE: unit tests'\nmake test\n",
		"scripts/check-full.sh": "\nscripts/check-fast.sh\n\n# e2e, from Makefile:7, one worktree at a time\n" +
			"printf '%s\\n' 'SUITE: e2e'\nlock e2e\nmake e2e\nunlock\n",
	} {
		b, err := os.ReadFile(filepath.Join(repo, path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s doesn't run %q:\n%s", path, want, b)
		}
	}
	b, err := os.ReadFile(filepath.Join(repo, ".orchestra", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"check_fast_timeout": "30m"`, `"check_full_timeout": "60m"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings.json lacks %s:\n%s", want, b)
		}
	}
}
