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
	"golang.org/x/sys/unix"
)

// Init asks in stages, and writes and installs nothing until the last one is submitted: Esc in
// stage 2, with stage 1 answered yes to installing Beads and to the CHANGELOG.md union, leaves the
// repository as it was.
func TestInitCancelledInItsLastStageChangesNothing(t *testing.T) {
	tty, master := openTerminal(t) // first, so that go test -short skips the test before any setup
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 100}); err != nil {
		t.Fatal(err)
	}
	dir := fakeBeadsTools(t, false)
	repo, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		{"┃ Install Beads with Homebrew?", keyEnter},
		{"┃ Tickets at the same time", keyEnter},
		{"┃ Merge CHANGELOG.md by union", keyEnter},
		{"Step 2 of 2 · Checks", "just verify"},
		{"just verify", keyEsc},
	} {
		term.waitFor(t, step.on)
		term.typeKeys(t, step.keys, false)
	}
	term.waitFor(t, "Cancelled; nothing was changed.")
	select {
	case code := <-done:
		done <- code // for the cleanup
		if code != dispatch.ExitSetup {
			t.Errorf("exit %d, want %d; stderr:\n%s", code, dispatch.ExitSetup, errOut.String())
		}
	case <-time.After(patience):
		t.Fatalf("init didn't exit; the terminal:\n%s", term.screen.String())
	}
	if calls := toolCalls(t, dir); calls != "" {
		t.Errorf("init ran:\n%s", calls)
	}
	for _, name := range []string{".orchestra", ".beads", ".gitattributes"} {
		if _, err := os.Stat(filepath.Join(repo, name)); !os.IsNotExist(err) {
			t.Errorf("init wrote %s: %v", name, err)
		}
	}
}
