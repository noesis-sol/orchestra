//go:build darwin || linux

package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"

	"github.com/noesis-sol/orchestra/internal/tui"
)

// A terminal that says it has no columns, as a fresh pseudo-terminal can, is taken as 80 wide.
func TestTermWidthOfATerminalWithoutColumns(t *testing.T) {
	tty, _ := openTerminal(t)
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{}); err != nil {
		t.Fatal(err)
	}
	if got := termWidth(tty); got != 80 {
		t.Errorf("termWidth = %d, want 80", got)
	}
}

// A busy line follows the terminal's width as it is resized during the step: each frame fits it.
func TestBusyLineFollowsAResize(t *testing.T) {
	tty, master := openTerminal(t)
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 200}); err != nil {
		t.Fatal(err)
	}
	var (
		mu     sync.Mutex
		screen strings.Builder
	)
	go func() { // until the terminal closes, as the test ends
		b := make([]byte, 4096)
		for {
			n, err := master.Read(b)
			mu.Lock()
			screen.Write(b[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	busy := tui.Terminal(tty, termWidth(tty)).Busy("", strings.Repeat("Writing the run report ", 5), "")
	if err := unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 40}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	busy.Done("Run report written")
	deadline := time.Now().Add(patience)
	for {
		mu.Lock()
		out := ansi.Strip(screen.String())
		mu.Unlock()
		if strings.Contains(out, "Run report written") {
			frames := strings.Split(out, "\r")
			for _, f := range frames[:len(frames)-1] {
				if w := ansi.StringWidth(f); w > 39 {
					t.Errorf("a frame %d columns wide on a terminal 40 wide: %q", w, f)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the busy step didn't end; the terminal:\n%q", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
