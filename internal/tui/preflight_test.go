package tui

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// An overnight run's checklist, and the questions that offer each check's fix and, while a failed
// check is left, whether to start anyway.

// limitSet is a failed check whose fix runs without the limit.
var limitSet = PreflightCheck{Verdict: CheckFailed, Text: "LIMIT=10 is set: the run would stop after 10 tickets",
	Fix: "Run without a limit?"}

func TestTheChecklistGivesEachCheckItsVerdict(t *testing.T) {
	checks := []PreflightCheck{
		{Verdict: CheckPassed, Text: "claude starts"},
		{Verdict: CheckWarned, Text: "no ticket limit: a hung worker holds its slot all night"},
		limitSet,
	}
	var plain strings.Builder
	Printer{Out: &plain}.Checklist(checks)
	got := stamped(plain.String())
	want := "Overnight pre-flight: 1 failed, 1 warning, 1 passed\n" +
		"  passed: claude starts\n" +
		"  warning: no ticket limit: a hung worker holds its slot all night\n" +
		"  failed: LIMIT=10 is set: the run would stop after 10 tickets\n"
	if got != want {
		t.Errorf("plain checklist:\n%s\nwant:\n%s", got, want)
	}
	var styled strings.Builder
	Printer{Out: &styled, Styled: true, Width: 100}.Checklist(checks)
	for _, line := range []string{"◆ Overnight pre-flight: 1 failed, 1 warning, 1 passed", "  ✓ claude starts",
		"  ! no ticket limit", "  ✗ LIMIT=10 is set"} {
		if !strings.Contains(ansi.Strip(styled.String()), line) {
			t.Errorf("styled checklist lacks %q:\n%s", line, styled.String())
		}
	}
}

func TestTheChecklistHeadingCountsTheChecks(t *testing.T) {
	for _, tc := range []struct {
		checks []PreflightCheck
		want   string
	}{
		{nil, "nothing to check"},
		{[]PreflightCheck{{Verdict: CheckPassed}, {Verdict: CheckPassed}}, "every check passed"},
		{[]PreflightCheck{{Verdict: CheckWarned}, {Verdict: CheckWarned}}, "2 warnings"},
		{[]PreflightCheck{{Verdict: CheckFailed}, {Verdict: CheckFailed}, {Verdict: CheckPassed}}, "2 failed, 1 passed"},
	} {
		if got := checklistCount(tc.checks); got != tc.want {
			t.Errorf("checklistCount(%v) = %q, want %q", tc.checks, got, tc.want)
		}
	}
}

// stamped is plain output without the time Say puts before each line.
func stamped(out string) string {
	var lines []string
	for line := range strings.Lines(out) {
		if _, rest, ok := strings.Cut(line, " "); ok {
			if _, rest, ok = strings.Cut(rest, " "); ok {
				line = rest
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "")
}

func TestThePreflightAsksEachFixThenWhetherToStartAnyway(t *testing.T) {
	warned := PreflightCheck{Verdict: CheckWarned, Text: "usage_limit_wait is 1h", Fix: "Wait 6h for the usage limit?"}
	unfixable := PreflightCheck{Verdict: CheckFailed, Text: "main fails its merge check"}
	for _, tc := range []struct {
		name    string
		checks  []PreflightCheck
		forced  bool
		answers []string
		fix     []bool
		force   bool
		anyway  bool // asked whether to start anyway
	}{
		{"every fix made", []PreflightCheck{warned, limitSet}, false, []string{"y", "y"}, []bool{true, true}, false, false},
		{"a failure left", []PreflightCheck{warned, limitSet}, false, []string{"y", "n", "y"}, []bool{true, false}, true, true},
		{"a failure left, not started", []PreflightCheck{limitSet}, false, []string{"n", "n"}, []bool{false}, false, true},
		{"no fix for a failure", []PreflightCheck{unfixable, warned}, false, []string{"n", "y"}, []bool{false, false}, true, true},
		{"forced", []PreflightCheck{unfixable, limitSet}, true, []string{"n"}, []bool{false, false}, true, false},
	} {
		var out strings.Builder
		got, err := AskPreflight(t.Context(), typed(tc.answers...), &out, "dumb", tc.checks, tc.forced)
		screen := ansi.Strip(out.String())
		if err != nil || !slices.Equal(got.Fix, tc.fix) || got.Force != tc.force {
			t.Errorf("%s: AskPreflight = %+v, %v; want fixes %v, force %v\n%s", tc.name, got, err, tc.fix, tc.force, screen)
		}
		if asked := strings.Contains(screen, "Start the run anyway"); asked != tc.anyway {
			t.Errorf("%s: asked whether to start anyway: %v, want %v\n%s", tc.name, asked, tc.anyway, screen)
		}
	}
}

func TestThePreflightAsksNothingWithoutAFixOrAFailure(t *testing.T) {
	checks := []PreflightCheck{{Verdict: CheckPassed, Text: "claude starts"}, {Verdict: CheckWarned, Text: "on battery"}}
	var out strings.Builder
	got, err := AskPreflight(t.Context(), strings.NewReader(""), &out, "dumb", checks, false)
	if err != nil || slices.Contains(got.Fix, true) || got.Force || out.Len() > 0 {
		t.Errorf("AskPreflight = %+v, %v, drawing %q; want nothing asked", got, err, out.String())
	}
}

func TestThePreflightCancelsAtTheEndOfInput(t *testing.T) {
	var out strings.Builder
	_, err := AskPreflight(t.Context(), strings.NewReader(""), &out, "dumb", []PreflightCheck{limitSet}, false)
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("AskPreflight at the end of input: %v, want ErrCancelled", err)
	}
}

func TestThePreflightFormOnATerminal(t *testing.T) {
	var got PreflightAnswer
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		var err error
		got, err = AskPreflight(t.Context(), in, out, ttyTerm, []PreflightCheck{limitSet}, false)
		return err
	})
	term.typeSteps(t, []keysOn{
		{"Run without a limit?", "n"},
		{"Start the run anyway, with 1 check failed?", "y"},
	})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Fix, []bool{false}) || !got.Force {
		t.Errorf("AskPreflight = %+v; want the fix declined and the run started anyway", got)
	}
}
