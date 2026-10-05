package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"go.uber.org/goleak"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// slowFeatureOrgans is fakeOrganFeature taking a few of the spinner's frames to answer; with stop,
// the plan organ instead stops the run, as Ctrl+C does, and works until it is stopped.
type slowFeatureOrgans struct {
	fakeOrganFeature
	stop context.CancelFunc
}

func (o slowFeatureOrgans) Screen(ctx context.Context, r organ.Request) (organ.Screening, error) {
	select {
	case <-time.After(300 * time.Millisecond):
	case <-ctx.Done():
		return organ.Screening{}, ctx.Err()
	}
	return o.fakeOrganFeature.Screen(ctx, r)
}

func (o slowFeatureOrgans) PlanFeature(ctx context.Context, ev organ.FeatureEvidence) (organ.FeaturePlan, error) {
	if o.stop != nil {
		o.stop()
		<-ctx.Done()
		return organ.FeaturePlan{}, ctx.Err()
	}
	select {
	case <-time.After(300 * time.Millisecond):
	case <-ctx.Done():
		return organ.FeaturePlan{}, ctx.Err()
	}
	return o.fakeOrganFeature.PlanFeature(ctx, ev)
}

// On a terminal, screening and planning are busy lines, each ending as its outcome, and the plan
// follows them; no goroutine is left drawing.
func TestFeatureClaudeStepsAreBusyLines(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	organs := slowFeatureOrgans{fakeOrganFeature: fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}}
	f, out, errOut := featureFixture(t, organs, &fakeFeatureTracker{}, true, false, "")
	f.say = tui.Terminal(out, 120)
	if epic, code := f.run(context.Background()); epic != "f-1" || code != dispatch.ExitOK {
		t.Fatalf("epic %q, exit %d\n%s", epic, code, errOut)
	}
	printed := ansi.Strip(out.String())
	at := 0
	for _, want := range []string{"Screening the request with Claude… 0:00", "\r◆ Request screened\n",
		"Planning the feature with Claude… 0:00 (this can take a few minutes; Ctrl+C stops it)",
		"\r◆ Feature planned\n", "Epic: JSON output", "filed epic f-1 with 2 tickets"} {
		i := strings.Index(printed[at:], want)
		if i < 0 {
			t.Fatalf("printed lacks %q after %d:\n%q", want, at, printed)
		}
		at += i + len(want)
	}
	if strings.Contains(printed, "screening the request with claude") {
		t.Errorf("a terminal got the plain line too:\n%q", printed)
	}
}

// Ctrl+C while the plan organ works leaves its line marked stopped, before saying nothing was filed.
func TestFeatureCtrlCStopsThePlanUnderItsBusyLine(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	organs := slowFeatureOrgans{fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}, cancel}
	tracker := &fakeFeatureTracker{}
	f, out, errOut := featureFixture(t, organs, tracker, true, false, "")
	f.say = tui.Terminal(out, 120)
	if epic, code := f.run(ctx); epic != "" || code != dispatch.ExitInterrupted || len(tracker.created) > 0 {
		t.Fatalf("epic %q, exit %d, filed %+v\n%s", epic, code, tracker.created, errOut)
	}
	if printed := ansi.Strip(out.String()); !strings.HasSuffix(printed, "◆ Planning stopped\n") {
		t.Errorf("printed:\n%q", printed)
	}
	if errOut.String() != "orchestra: stopped before filing; nothing was filed.\n" {
		t.Errorf("stderr:\n%s", errOut)
	}
}

// Without a terminal, each step is its plain line as before, without the time.
func TestFeatureClaudeStepsArePlainLinesOffATerminal(t *testing.T) {
	f, out, errOut := featureFixture(t, fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan},
		&fakeFeatureTracker{}, true, false, "")
	if epic, code := f.run(context.Background()); epic != "f-1" || code != dispatch.ExitOK {
		t.Fatalf("epic %q, exit %d\n%s", epic, code, errOut)
	}
	if want := "screening the request with claude…\n" +
		"planning the feature with claude… (this can take a few minutes; ctrl+c stops it)\n\nEpic: "; !strings.HasPrefix(
		out.String(), want) {
		t.Errorf("printed:\n%q\nwant it to begin with %q", out, want)
	}
}
