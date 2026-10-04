package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The Select keymap's Up and Down keys, as Bubble Tea reads them.
var (
	upKeys = []tea.KeyMsg{
		{Type: tea.KeyUp}, {Type: tea.KeyRunes, Runes: []rune("k")}, {Type: tea.KeyCtrlK}, {Type: tea.KeyCtrlP},
	}
	downKeys = []tea.KeyMsg{
		{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune("j")}, {Type: tea.KeyCtrlJ}, {Type: tea.KeyCtrlN},
	}
	filterKey = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}
)

// abcSelect is a bounded select of a, b and c on value, with the keys a form gives it.
func abcSelect(value string) *boundedSelect[string] {
	s := newBoundedSelect(huh.NewSelect[string](), &value, huh.NewOptions("a", "b", "c")...)
	s.WithKeyMap(huh.NewDefaultKeyMap())
	return s
}

// pressOn updates s with k and returns the option under the cursor; the field stays s.
func pressOn(t *testing.T, s *boundedSelect[string], k tea.KeyMsg) string {
	t.Helper()
	if m, _ := s.Update(k); m != s {
		t.Fatalf("%s: Update returned %T, want the bounded select", k, m)
	}
	hovered, _ := s.Hovered()
	return hovered
}

func TestBoundedSelectStopsAtItsEnds(t *testing.T) {
	for _, k := range upKeys {
		if got := pressOn(t, abcSelect("a"), k); got != "a" {
			t.Errorf("%s on the first option: the cursor is on %s", k, got)
		}
		if got := pressOn(t, abcSelect("b"), k); got != "a" {
			t.Errorf("%s on the second option: the cursor is on %s, want a", k, got)
		}
	}
	for _, k := range downKeys {
		if got := pressOn(t, abcSelect("c"), k); got != "c" {
			t.Errorf("%s on the last option: the cursor is on %s", k, got)
		}
		if got := pressOn(t, abcSelect("b"), k); got != "c" {
			t.Errorf("%s on the second option: the cursor is on %s, want c", k, got)
		}
	}
}

// '/' never starts filtering, even after the select was left, which turns huh's filter key back on;
// and the help line doesn't offer it.
func TestBoundedSelectDoesNotFilter(t *testing.T) {
	s := abcSelect("b")
	for _, k := range []tea.KeyMsg{filterKey, {Type: tea.KeyEnter}, filterKey} {
		pressOn(t, s, k)
		if s.GetFiltering() {
			t.Fatalf("filtering after %s", k)
		}
	}
	var help []string
	for _, kb := range s.KeyBinds() {
		help = append(help, kb.Help().Key)
	}
	if got := strings.Join(help, " "); strings.Contains(got, "/") || !strings.Contains(got, "↑ ↓") {
		t.Errorf("the help offers %q, want ↑ and ↓ without /", got)
	}
}

// init's choice of tickets at the same time stops at 1 and at Custom…, and doesn't filter: '/'
// then "busy" would leave only 4.
func TestInitConcurrencyStopsAtItsEnds(t *testing.T) {
	const up, down, enter, ctrlA = "\x1b[A", "\x1b[B", "\r", "\x01"
	for _, tc := range []struct {
		name    string
		current int
		steps   []keysOn
		want    int
	}{
		{"up on 1", 1, []keysOn{{onConcurrency, up + enter}}, 1},
		{"k on 1", 1, []keysOn{{onConcurrency, "k" + enter}}, 1},
		{"down on a saved 6", 6, []keysOn{{onConcurrency, down + enter}, {onCustom, enter}}, 6},
		{"j on a saved 6", 6, []keysOn{{onConcurrency, "j" + enter}, {onCustom, enter}}, 6},
		{"down past Custom… and back", 4, []keysOn{{onConcurrency, down + down + up + up + enter}}, 3},
		{"filter for busy", 1, []keysOn{{onConcurrency, "/" + ctrlA + "busy" + enter}}, 1},
	} {
		if got := askConcurrency(t, tc.current, tc.steps...); got != tc.want {
			t.Errorf("%s: concurrent = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestInitConcurrencyHelpOffersNoFilter(t *testing.T) {
	c := project.Choice{Concurrent: 1}
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		return AskInit(in, out, &c, false, false, true, false, false, false)
	})
	term.waitFor(t, "↑ up")
	if screen := term.screen.String(); strings.Contains(screen, "filter") {
		t.Errorf("the help line offers the filter key:\n%s", screen)
	}
	term.typeKeys(t, "\r")
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
}
