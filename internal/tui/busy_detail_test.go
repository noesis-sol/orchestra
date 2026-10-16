package tui

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// A busy line shows how far its step has got, as Detail last set it, between its text and the time
// taken; plain output shows nothing of it.
func TestBusyLineShowsItsDetail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		busy := Terminal(&b, 120).Busy("breaking the spec down…", "Breaking the spec down with Claude…",
			"(Ctrl+C stops)")
		time.Sleep(500 * time.Millisecond)
		busy.Detail("1 epic · 0 tickets")
		time.Sleep(1000 * time.Millisecond)
		busy.Detail("3 epics · 41 tickets\x1b[31m")
		time.Sleep(500 * time.Millisecond)
		busy.Done("Spec broken down: 3 epics, 41 tickets")
		out := ansi.Strip(b.String())
		for _, want := range []string{"Claude… 0:00 (Ctrl+C stops)\r", "Claude… 1 epic · 0 tickets · 0:01 (Ctrl+C stops)\r",
			"Claude… 3 epics · 41 tickets · 0:01 (Ctrl+C stops)\r", "\r◆ Spec broken down: 3 epics, 41 tickets (2s)\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%q", want, out)
			}
		}
		if strings.Contains(b.String(), "\x1b[31m") {
			t.Errorf("the detail's control sequence reached the terminal:\n%q", b.String())
		}
	})

	var b strings.Builder
	busy := Printer{Out: &b}.Busy("SPEC: breaking the spec down", "", "")
	busy.Detail("3 epics · 41 tickets")
	busy.Done("Spec broken down")
	if out := b.String(); !strings.HasSuffix(out, " SPEC: breaking the spec down\n") || strings.Count(out, "\n") != 1 {
		t.Errorf("plain output printed %q", out)
	}
}
