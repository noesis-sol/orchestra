package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// questionStates are the stop question's three forms, with what y and n do in each.
var questionStates = []struct {
	name               string
	workers            int
	draining           bool
	keys, yDoes, nDoes string // keys: the box's keys line, as plain text
}{
	{"two running", 2, false, "y stop after current · n keep going", "stop after current", "keep going"},
	{"none running", 0, false, "y stop · n keep going", "stop", "keep going"},
	{"winding down", 2, true, "y keep going · n keep stopping", "keep going", "keep stopping"},
}

// questionDashboard is a dashboard with the stop question open in the given state.
func questionDashboard(workers int, draining bool) Dashboard {
	var focused []string
	m := numberedDashboard(workers, &focused)
	m.draining, m.asking = draining, true
	return m
}

// The stop question names y and n as the hint names its keys: each key bold in keyStyle's colour,
// what it does faint, in the box and in the one-line prompt alike, on dark and light backgrounds.
func TestStopQuestionKeysLookLikeTheHint(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetHasDarkBackground(true)
	const faint = "\x1b[2m"
	for _, c := range []struct {
		dark bool
		key  string // bold (1) and the exact colour: #E5E7EB on dark, #374151 on light
	}{{true, "\x1b[1;38;2;229;231;235m"}, {false, "\x1b[1;38;2;55;65;81m"}} {
		lipgloss.SetHasDarkBackground(c.dark)
		for _, s := range questionStates {
			m := questionDashboard(s.workers, s.draining)
			modal := m.modal(80)
			for _, want := range []string{c.key + "y", c.key + "n", faint + " " + s.yDoes, faint + " " + s.nDoes} {
				if !strings.Contains(modal, want) {
					t.Errorf("dark %v, %s: the box lacks %q:\n%q", c.dark, s.name, want, modal)
				}
			}
			prompt := m.promptLine(80)
			for _, want := range []string{c.key + "y", c.key + "n"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("dark %v, %s: the prompt line lacks %q: %q", c.dark, s.name, want, prompt)
				}
			}
		}
	}
}

// Styling the keys changes no character of the question: stripped of colour, the box wraps and the
// prompt line cuts as they did with the keys line one faint string, in every pane they show in.
func TestStopQuestionKeepsItsWidths(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	for _, s := range questionStates {
		m := questionDashboard(s.workers, s.draining)
		q := m.drainQuestion()
		for w := 36; w <= 100; w++ { // the box shows from 36 columns
			plain := box(min(w-4, 64), 0, yellow, lipgloss.JoinVertical(lipgloss.Left, q.ask, "", q.about, "", s.keys))
			if got, want := ansi.Strip(m.modal(w)), ansi.Strip(plain); got != want {
				t.Errorf("%s, %d wide: the box reads\n%s\nwant\n%s", s.name, w, got, want)
			}
		}
		for w := 1; w <= 100; w++ {
			ask := q.ask
			if ansi.StringWidth(" "+ask+" y/n") > w {
				ask = q.short
			}
			want := ansi.Truncate(" "+ask+" y/n · "+q.about, w, "…")
			if got := m.promptLine(w); ansi.Strip(got) != want || ansi.StringWidth(got) > w {
				t.Errorf("%s, %d wide: the prompt line reads %q (%d wide), want %q", s.name, w, ansi.Strip(got),
					ansi.StringWidth(got), want)
			}
		}
	}
}
