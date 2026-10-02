package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// failedReview is fakeOrgans whose reviewer fails.
type failedReview struct{ fakeOrgans }

func (failedReview) Review(context.Context, int, string) (string, error) {
	return "", errors.New("claude: timed out after 5m")
}

// On a terminal the organ phase's lines are sentences, and Claude and Ctrl+C are named as such;
// the log keeps its wording.
func TestOrganLinesAreSentencesOnATerminal(t *testing.T) {
	for _, tc := range []struct {
		orch    organs
		printed []string
		logged  string
	}{
		{fakeOrgans{}, []string{"◆ Finishing triage…\n", "◆ Writing the run report with Claude… (Ctrl+C skips)\n",
			"◆ Report saved to /reports/r.md\n"}, "REPORT written to /reports/r.md"},
		{fakeOrgans{errors.New("mkdir /reports: permission denied")},
			[]string{"◆ Report not saved: mkdir /reports: permission denied\n"},
			"report not saved: mkdir /reports: permission denied"},
		{failedReview{}, []string{"◆ REVIEW_FAILED: claude: timed out after 5m\n"},
			"REVIEW_FAILED: claude: timed out after 5m"},
	} {
		log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		stops := catchStops()
		organPhase(tc.orch, options{Triage: true, Review: true}, stops, log, dispatch.ExitOK, "",
			tui.Printer{Out: &b, Styled: true, Width: 80}, func() {})
		stops.release()
		out := ansi.Strip(b.String())
		for _, want := range tc.printed {
			if !strings.Contains(out, want) {
				t.Errorf("printed lacks %q:\n%s", want, out)
			}
		}
		if lines := strings.Join(log.RunLines(), "\n"); !strings.Contains(lines, tc.logged) {
			t.Errorf("log lacks %q:\n%s", tc.logged, lines)
		}
	}
}
