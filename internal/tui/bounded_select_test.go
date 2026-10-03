package tui

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
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

// syncBuffer is what a form draws, written from Bubble Tea's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.b.String())
}

// init's choice of tickets at the same time stops at 1 and at Custom…, and doesn't filter: '/'
// then "busy" would leave only 4.
func TestInitConcurrencyStopsAtItsEnds(t *testing.T) {
	const up, down, enter, ctrlA = "\x1b[A", "\x1b[B", "\r", "\x01"
	for _, tc := range []struct {
		name    string
		current int
		keys    string
		want    int
	}{
		{"up on 1", 1, up + enter, 1},
		{"k on 1", 1, "k" + enter, 1},
		{"down on a saved 6", 6, down + enter + enter, 6},
		{"j on a saved 6", 6, "j" + enter + enter, 6},
		{"down past Custom… and back", 4, down + down + up + up + enter, 3},
		{"filter for busy", 1, "/" + ctrlA + "busy" + enter, 1},
	} {
		if got := askConcurrency(t, tc.current, tc.keys); got != tc.want {
			t.Errorf("%s: concurrent = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestInitConcurrencyHelpOffersNoFilter(t *testing.T) {
	in, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })
	var out syncBuffer
	c := project.Choice{Concurrent: 1}
	done := make(chan error, 1)
	go func() {
		done <- AskInit(in, &out, &c, false, false, true, false, false, false)
	}()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), "↑ up"); {
		if time.Now().After(deadline) {
			t.Fatalf("the help line never showed:\n%s", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if screen := out.String(); strings.Contains(screen, "filter") {
		t.Errorf("the help line offers the filter key:\n%s", screen)
	}
	if _, err := keys.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AskInit didn't finish")
	}
}
