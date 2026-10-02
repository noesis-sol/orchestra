package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// recordingOrgans answers as fakeOrganFeature does and keeps the evidence each plan was asked from.
type recordingOrgans struct {
	fakeOrganFeature
	asked *[]organ.FeatureEvidence
}

func (o recordingOrgans) PlanFeature(ctx context.Context, ev organ.FeatureEvidence) (organ.FeaturePlan, error) {
	*o.asked = append(*o.asked, ev)
	return o.fakeOrganFeature.PlanFeature(ctx, ev)
}

// A ticket left in progress by a stopped run, or set aside with bd defer, is still to be done:
// the plan organ sees every ticket that isn't closed, with its status.
func TestFeaturePlansAgainstEveryUnclosedTicket(t *testing.T) {
	tracker := &fakeFeatureTracker{unclosed: []dispatch.Ticket{
		{ID: "k-1", Title: "Faster listing", Status: "open"},
		{ID: "k-2", Title: "Add --json to list", Status: "in_progress"},
		{ID: "k-3", Title: "Colour output", Status: "deferred"},
		{ID: "k-4", Title: "Retry merges", Status: "blocked"},
	}}
	var asked []organ.FeatureEvidence
	organs := recordingOrgans{fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}, &asked}
	f, _, errOut := featureFixture(t, organs, tracker, true, false, "")
	if _, code := f.run(context.Background()); code != dispatch.ExitOK {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	want := []organ.UnclosedTicket{{ID: "k-1", Status: "open", Title: "Faster listing"},
		{ID: "k-2", Status: "in_progress", Title: "Add --json to list"},
		{ID: "k-3", Status: "deferred", Title: "Colour output"}, {ID: "k-4", Status: "blocked", Title: "Retry merges"}}
	if len(asked) != 1 || !slices.Equal(asked[0].Unclosed, want) {
		t.Errorf("the plan was asked from %+v, want unclosed %+v", asked, want)
	}
}

// interruptingTracker lists the unclosed tickets as Ctrl+C comes.
type interruptingTracker struct {
	*fakeFeatureTracker
	cancel context.CancelFunc
}

func (t interruptingTracker) Unclosed(ctx context.Context) ([]dispatch.Ticket, error) {
	t.cancel()
	return t.fakeFeatureTracker.Unclosed(ctx)
}

// Ctrl+C while the evidence is gathered stops the feature run there, as an interruption: the plan
// isn't asked for and nothing is filed.
func TestFeatureStopsWhileGatheringOnCtrlC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := &fakeFeatureTracker{}
	var asked []organ.FeatureEvidence
	organs := recordingOrgans{fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}, &asked}
	f, out, errOut := featureFixture(t, organs, interruptingTracker{tracker, cancel}, true, false, "")
	if epic, code := f.run(ctx); epic != "" || code != dispatch.ExitInterrupted {
		t.Fatalf("epic %q, exit %d\n%s", epic, code, errOut)
	}
	if len(asked) != 0 || len(tracker.created) != 0 || strings.Contains(out.String(), "planning the feature") {
		t.Errorf("planned %d times, filed %+v\n%s", len(asked), tracker.created, out)
	}
	if errOut.String() != "orchestra: stopped before filing; nothing was filed.\n" {
		t.Errorf("stderr:\n%s", errOut)
	}
}
