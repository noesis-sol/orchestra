package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"go.uber.org/goleak"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// slowReview is fakeOrgans whose reviewer works until it is stopped, saying when it has started.
type slowReview struct {
	fakeOrgans
	started chan struct{}
}

func (r slowReview) Review(ctx context.Context, _ int, _ string) (string, error) {
	close(r.started)
	<-ctx.Done()
	return "", ctx.Err()
}

// On a terminal the run report's line spins while the reviewer works; Ctrl+C still skips the report
// and leaves the line marked skipped, with no goroutine left drawing it.
func TestCtrlCSkipsTheReportUnderItsBusyLine(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	orch := slowReview{started: make(chan struct{})}
	stops := catchStops()
	defer stops.release()
	go func() {
		<-orch.started
		time.Sleep(300 * time.Millisecond) // a few of the spinner's frames
		stops.deliver(os.Interrupt)
	}()
	var b strings.Builder
	organPhase(orch, options{Triage: true, Review: true}, stops, log, dispatch.ExitOK, "", tui.Terminal(&b, 80),
		func() {})
	out := ansi.Strip(b.String())
	for _, want := range []string{"◆ Triage finished\n", "Writing the run report with Claude… 0:00 (Ctrl+C skips)",
		"\r◆ Run report skipped\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("printed lacks %q:\n%q", want, out)
		}
	}
	if strings.Contains(out, "REVIEW_FAILED") {
		t.Errorf("a skipped report is reported as failed:\n%q", out)
	}
}
