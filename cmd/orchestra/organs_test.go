package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// fakeOrgans writes a report and fails to save it when saveErr is set.
type fakeOrgans struct{ saveErr error }

func (fakeOrgans) FinishTriage(context.Context) {}
func (fakeOrgans) Review(context.Context, int, string) (string, error) {
	return "# Orchestra run\n\nALL MERGED\n", nil
}
func (f fakeOrgans) SaveReport(string) (string, error) {
	if f.saveErr != nil {
		return "", f.saveErr
	}
	return "/reports/r.md", nil
}

func TestReportIsShownWhenItCannotBeSaved(t *testing.T) {
	for _, tc := range []struct {
		saveErr         error
		printed, logged string
	}{
		{nil, "report saved to /reports/r.md", "REPORT written to /reports/r.md"},
		{errors.New("mkdir /reports: permission denied"), "report not saved: mkdir /reports: permission denied",
			"report not saved: mkdir /reports: permission denied"},
	} {
		log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		stops := catchStops()
		organPhase(fakeOrgans{tc.saveErr}, options{Review: true}, stops, log, dispatch.ExitOK, "", tui.Printer{Out: &b},
			func() {})
		stops.release()
		if out := b.String(); !strings.Contains(out, "ALL MERGED") || !strings.Contains(out, tc.printed) {
			t.Errorf("save error %v: printed\n%s", tc.saveErr, out)
		}
		if lines := strings.Join(log.RunLines(), "\n"); !strings.Contains(lines, tc.logged) {
			t.Errorf("save error %v: logged\n%s", tc.saveErr, lines)
		}
	}
}
