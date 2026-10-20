package tui

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
)

// The start question offers a spec between the current tickets and a new feature: its file, then
// whether to refine it with claude first, running it as it is to start with.

// onRefine is what the screen shows once the choice to refine the spec has the focus.
const onRefine = "┃ Refine it with claude first?"

// askStart asks the start question in the repository repo on a fake terminal, with the steps' keys
// typed, and returns its answer and what it drew.
func askStart(t *testing.T, repo string, steps ...keysOn) (Work, string, error) {
	t.Helper()
	var work Work
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		var err error
		work, err = AskWork(t.Context(), in, out, ttyTerm, repo, 3, nil)
		return err
	})
	term.typeSteps(t, steps)
	err := term.end(t)
	return work, term.screen.String(), err
}

// hasLine reports whether a line of screen, without the bar on the left of the focused field and the
// spaces around it, is line.
func hasLine(screen, line string) bool {
	for l := range strings.Lines(screen) {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "┃")) == line {
			return true
		}
	}
	return false
}

// toSpec picks Spec, the second option, from the first.
var toSpec = keysOn{"> Current tickets: 3 ready", keyDown + keyEnter}

func TestStartQuestionOffersASpecBetweenTicketsAndAFeature(t *testing.T) {
	repo, _ := specRepo(t)
	work, screen, err := askStart(t, repo, toSpec, keysOn{onSpec, "docs/spec.md" + keyEnter}, keysOn{onRefine, keyEnter})
	if want := (Work{Kind: WorkSpec, Spec: filepath.Join(repo, "docs", "spec.md")}); err != nil || work != want {
		t.Errorf("AskWork = %+v, %v; want %+v\n%s", work, err, want, screen)
	}
	options := []string{"Current tickets: 3 ready", "Spec: build what a spec file describes",
		"New feature: describe it, talk it through with claude, run its tickets"}
	at := -1
	for _, o := range options {
		i := strings.Index(screen, o)
		if i <= at {
			t.Errorf("%q isn't shown after the options before it, in %q:\n%s", o, options, screen)
		}
		at = i
	}
	for _, want := range []string{"Spec file", "Refine it with claude first?", "> Run it as it is",
		"Refine it with claude first"} {
		if !hasLine(screen, want) {
			t.Errorf("the question lacks the line %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "Describe the feature") {
		t.Errorf("the feature's description was asked for:\n%s", screen)
	}
}

// Down at the choice to refine, even twice, picks refining it; up again, running it as it is.
func TestStartQuestionRefinesTheSpecWhenAsked(t *testing.T) {
	repo, home := specRepo(t)
	work, screen, err := askStart(t, repo, toSpec, keysOn{onSpec, "~/home-spec.md" + keyEnter},
		keysOn{onRefine, keyDown + keyDown + keyEnter})
	if want := (Work{Kind: WorkSpec, Spec: filepath.Join(home, "home-spec.md"), Refine: true}); err != nil ||
		work != want {
		t.Errorf("AskWork = %+v, %v; want %+v\n%s", work, err, want, screen)
	}
	const up = "\x1b[A"
	work, screen, err = askStart(t, repo, toSpec, keysOn{onSpec, "README.md" + keyEnter},
		keysOn{onRefine, keyDown + up + keyEnter})
	if want := (Work{Kind: WorkSpec, Spec: filepath.Join(repo, "README.md")}); err != nil || work != want {
		t.Errorf("down and up: AskWork = %+v, %v; want %+v\n%s", work, err, want, screen)
	}
}

// The choice to refine comes only once the path names a spec.
func TestStartQuestionAsksToRefineOnlyOnceThePathNamesASpec(t *testing.T) {
	repo, _ := specRepo(t)
	work, screen, err := askStart(t, repo, toSpec, keysOn{onSpec, "nope.md" + keyEnter},
		keysOn{"no such file: nope.md", keyCtrlU + "docs/story.txt" + keyEnter}, keysOn{onRefine, keyEnter})
	if want := (Work{Kind: WorkSpec, Spec: filepath.Join(repo, "docs", "story.txt")}); err != nil || work != want {
		t.Errorf("AskWork = %+v, %v; want %+v\n%s", work, err, want, screen)
	}
	if before, _, _ := strings.Cut(screen, "docs/story.txt"); strings.Contains(before, "Refine it with claude first?") {
		t.Errorf("the choice to refine was shown before the path named a spec:\n%s", screen)
	}
}

// Going back from the spec's file to the first question and picking the current tickets runs them,
// whatever path was typed.
func TestStartQuestionGoesBackFromASpec(t *testing.T) {
	repo, _ := specRepo(t)
	const shiftTab, up = "\x1b[Z", "\x1b[A"
	work, screen, err := askStart(t, repo, toSpec, keysOn{onSpec, "nope.md" + shiftTab},
		keysOn{"┃ > Spec: build what a spec file describes", up + keyEnter})
	if err != nil || work != (Work{Kind: WorkTickets}) {
		t.Errorf("AskWork = %+v, %v; want the current tickets\n%s", work, err, screen)
	}
}

// Esc or Ctrl+C at the spec's file or at the choice to refine cancels the question; Esc while
// browsing for the file only closes the browser.
func TestStartQuestionCancelledAtASpec(t *testing.T) {
	repo, _ := specRepo(t)
	const ctrlC = "\x03"
	for _, cancel := range []string{keyEsc, ctrlC} {
		for name, steps := range map[string][]keysOn{
			"at the file":              {toSpec, {onSpec, "docs/sp"}, {"docs/sp", cancel}},
			"at the choice to refine":  {toSpec, {onSpec, "README.md" + keyEnter}, {onRefine, cancel}},
			"after browsing, at Esc's": {toSpec, {onSpec, keyCtrlO}, {"esc close", keyEsc}, {"ctrl+o", cancel}},
		} {
			if work, screen, err := askStart(t, repo, steps...); !errors.Is(err, ErrCancelled) {
				t.Errorf("%q %s: AskWork = %+v, %v; want %v\n%s", cancel, name, work, err, ErrCancelled, screen)
			}
		}
	}
}

// With TERM=dumb, the start question offers the same three, then asks for the spec's file as a line
// until it names a spec, and whether to refine it, running it as it is unless told otherwise.
func TestAccessibleStartQuestionOffersASpec(t *testing.T) {
	repo, _ := specRepo(t)
	for _, tc := range []struct {
		name    string
		answers []string
		want    Work
	}{
		{"as it is", []string{"2", "docs/spec.md", ""}, Work{Kind: WorkSpec, Spec: filepath.Join(repo, "docs", "spec.md")}},
		{"refined", []string{"2", "nope.md", "README.md", "2"}, Work{Kind: WorkSpec, Spec: filepath.Join(repo, "README.md"),
			Refine: true}},
		{"run as it is", []string{"2", "README.md", "1"}, Work{Kind: WorkSpec, Spec: filepath.Join(repo, "README.md")}},
		{"the current tickets", []string{"1"}, Work{Kind: WorkTickets}},
	} {
		var out strings.Builder
		got, err := AskWork(context.Background(), typed(tc.answers...), &out, "dumb", repo, 3, nil)
		screen := ansi.Strip(out.String())
		if err != nil || got != tc.want {
			t.Errorf("%s: AskWork = %+v, %v; want %+v\n%s", tc.name, got, err, tc.want, screen)
		}
		for _, want := range []string{"1. Current tickets: 3 ready\n", "2. Spec: build what a spec file describes\n",
			"3. New feature: describe it, talk it through with claude, run its tickets\n"} {
			if !strings.Contains(screen, want) {
				t.Errorf("%s: the accessible form lacks %q:\n%s", tc.name, want, screen)
			}
		}
		asked := strings.Contains(screen, "Refine it with claude first? \n1. Run it as it is\n2. Refine it with claude first\n")
		if spec := tc.want.Kind == WorkSpec; asked != spec {
			t.Errorf("%s: asked whether to refine: %v, want %v\n%s", tc.name, asked, spec, screen)
		}
	}
}

func TestAccessibleStartQuestionCancelsAtTheEndOfInputAtASpec(t *testing.T) {
	repo, _ := specRepo(t)
	for name, answers := range map[string][]string{
		"at the file":             {"2"},
		"after a wrong file":      {"2", "nope.md"},
		"at the choice to refine": {"2", "README.md"},
	} {
		var out strings.Builder
		got, err := AskWork(context.Background(), typedThenEnd(answers...), &out, "dumb", repo, 3, nil)
		if !errors.Is(err, ErrCancelled) && !errors.Is(err, huh.ErrUserAborted) {
			t.Errorf("%s: AskWork = %+v, %v; want %v\n%s", name, got, err, ErrCancelled, ansi.Strip(out.String()))
		}
	}
}
