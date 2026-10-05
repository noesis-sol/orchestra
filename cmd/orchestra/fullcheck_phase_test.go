package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// dueFullCheck is fakeOrgans whose full check is due, recording the steps of the phase in order. With
// stops set, a stop signal comes as the full check runs.
type dueFullCheck struct {
	fakeOrgans
	steps *[]string
	stops *stopWatch
}

func (o dueFullCheck) FullCheckDue(code int) bool { return code == dispatch.ExitOK }

func (o dueFullCheck) FullCheck(ctx context.Context) {
	if o.stops != nil {
		o.stops.deliver(os.Interrupt)
	}
	*o.steps = append(*o.steps, "full check")
	if ctx.Err() != nil {
		*o.steps = append(*o.steps, "stopped")
	}
}

func (o dueFullCheck) FinishTriage(ctx context.Context) { *o.steps = append(*o.steps, "triage") }

func (o dueFullCheck) Review(ctx context.Context, code int, final string) (string, error) {
	*o.steps = append(*o.steps, "review")
	return o.fakeOrgans.Review(ctx, code, final)
}

// fullCheckPhase runs the organ phase on orch with opts after a run that ended with code, and returns
// what it printed on a terminal.
func fullCheckPhase(t *testing.T, orch organs, opts options, stops *stopWatch, code int) string {
	t.Helper()
	log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	organPhase(orch, opts, stops, log, code, "", tui.Printer{Out: &b, Styled: true, Width: 80}, func() {})
	return b.String()
}

// The full check runs after the run, before triage finishes and the run report, and with the organs
// off too; not after a run that stopped.
func TestFullCheckRunsBeforeTheOrgans(t *testing.T) {
	stops := catchStops()
	defer stops.release()
	var steps []string
	out := fullCheckPhase(t, dueFullCheck{steps: &steps}, options{Triage: true, Review: true}, stops, dispatch.ExitOK)
	if got := strings.Join(steps, ", "); got != "full check, triage, review" {
		t.Errorf("steps: %s", got)
	}
	if !strings.Contains(out, "◆ Running the full check… (Ctrl+C skips)\n") ||
		!strings.Contains(out, "◆ Full check finished") {
		t.Errorf("printed:\n%s", out)
	}

	steps = nil
	fullCheckPhase(t, dueFullCheck{steps: &steps}, options{}, stops, dispatch.ExitOK)
	if got := strings.Join(steps, ", "); got != "full check" {
		t.Errorf("with the organs off, steps: %s", got)
	}

	steps = nil
	fullCheckPhase(t, dueFullCheck{steps: &steps}, options{Triage: true, Review: true}, stops, dispatch.ExitStuck)
	if got := strings.Join(steps, ", "); got != "triage, review" {
		t.Errorf("after a hold, steps: %s", got)
	}
}

// Ctrl+C while the full check runs stops it and skips the organs after it.
func TestCtrlCDuringTheFullCheckSkipsTheRest(t *testing.T) {
	stops := catchStops()
	defer stops.release()
	var steps []string
	out := fullCheckPhase(t, dueFullCheck{steps: &steps, stops: stops}, options{Triage: true, Review: true}, stops,
		dispatch.ExitOK)
	if got := strings.Join(steps, ", "); got != "full check, stopped" {
		t.Errorf("steps: %s", got)
	}
	if !strings.Contains(out, "◆ Full check skipped") {
		t.Errorf("printed:\n%s", out)
	}
}
