package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// hintStates are the dashboard's states the hint line differs in, each with two workers running
// and none, and the plain hint each gives in a wide pane.
var hintStates = []struct {
	name                       string
	stopping, draining, asking bool
	twoWorkers, noWorkers      string
}{
	{"running", false, false, false,
		"  1–2 to go to a worker's tab · s to stop after the current tickets", "  s to stop after the current tickets"},
	{"asking", false, false, true, "  s to stop after the current tickets", "  s to stop after the current tickets"},
	{"winding down", false, true, false,
		"  1–2 to go to a worker's tab · s to keep taking tickets", "  s to keep taking tickets"},
	{"winding down, asking", false, true, true, "  s to keep taking tickets", "  s to keep taking tickets"},
	{"stopping", true, false, false, "  1–2 to go to a worker's tab", ""},
	{"stopping, winding down", true, true, false,
		"  1–2 to go to a worker's tab · s to keep taking tickets", "  s to keep taking tickets"},
}

// The hint names each key as "<key> to <what it does>", never Ctrl+C, and fits any pane.
func TestKeyHintInEveryStateAndWidth(t *testing.T) {
	var focused []string
	for _, s := range hintStates {
		for _, workers := range []int{2, 0} {
			m := numberedDashboard(workers, &focused)
			m.stopping, m.draining, m.asking = s.stopping, s.draining, s.asking
			want := s.twoWorkers
			if workers == 0 {
				want = s.noWorkers
			}
			if got := ansi.Strip(m.hintLine(100)); got != want {
				t.Errorf("%s, %d workers: %q, want %q", s.name, workers, got, want)
			}
			for w := 1; w <= 100; w++ {
				hint := m.hintLine(w)
				if ansi.StringWidth(hint) > w {
					t.Errorf("%s, %d workers, %d wide: %d wide: %q", s.name, workers, w, ansi.StringWidth(hint),
						ansi.Strip(hint))
				}
				if plain := strings.ToLower(ansi.Strip(hint)); strings.Contains(plain, "ctrl") {
					t.Errorf("%s, %d workers, %d wide: a Ctrl+C hint: %q", s.name, workers, w, plain)
				}
			}
		}
	}
}

// Ctrl+C has no hint, and still stops the run at once in every state.
func TestCtrlCStopsAtOnceInEveryState(t *testing.T) {
	var focused []string
	for _, s := range hintStates {
		cancelled := false
		m := numberedDashboard(2, &focused)
		m.cancel = func() { cancelled = true }
		m.stopping, m.draining, m.asking = s.stopping, s.draining, s.asking
		if m = press(m, "ctrl+c"); !cancelled || !m.Interrupted() {
			t.Errorf("%s: cancelled %v, interrupted %v", s.name, cancelled, m.Interrupted())
		}
	}
}

// Each key is bold in a colour near the text's own, on dark and light backgrounds alike, and what it
// does is faint, as in Bubble Tea's help views.
func TestKeyHintKeysAreBrighterThanWhatTheyDo(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetHasDarkBackground(true)
	const faint = "\x1b[2m"
	var focused []string
	m := numberedDashboard(2, &focused)
	for _, c := range []struct {
		dark bool
		key  string // bold (1) and the exact colour: #E5E7EB on dark, #374151 on light
	}{{true, "\x1b[1;38;2;229;231;235m"}, {false, "\x1b[1;38;2;55;65;81m"}} {
		lipgloss.SetHasDarkBackground(c.dark)
		for _, w := range []int{80, 40} {
			hint := m.hintLine(w)
			for _, want := range []string{c.key + "1–2", c.key + "s", faint + " "} {
				if !strings.Contains(hint, want) {
					t.Errorf("dark %v, %d wide: %q lacks %q", c.dark, w, hint, want)
				}
			}
		}
	}
}

// A run stopping for another reason with no worker running has no key to name: the hint takes no
// line, rather than leaving an empty one at the bottom.
func TestNoHintTakesNoLine(t *testing.T) {
	var focused []string
	m := numberedDashboard(0, &focused)
	m.stopping = true
	m.width, m.height = 70, 30
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if last := lines[len(lines)-1]; strings.TrimSpace(last) == "" {
		t.Errorf("the view ends in an empty line:\n%s", strings.Join(lines, "\n"))
	}
	m.cfg.AgentKind = "claude" // with no MCP servers chosen: a warning above the hint
	lines = strings.Split(ansi.Strip(m.View()), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "! workers load every MCP server") {
		t.Errorf("the warning is not the last line:\n%s", strings.Join(lines, "\n"))
	}
}
