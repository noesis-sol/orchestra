package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// featurePlan is a two-ticket plan whose second ticket waits for the first; one of its files was
// dropped when it was checked.
var featurePlan = organ.FeaturePlan{
	Epic: organ.PlannedEpic{Title: "JSON output", Description: "Machine-readable output.\nFor scripts."},
	Tickets: []organ.PlannedTicket{
		{Key: "t1", Title: "Add the JSON encoder", Type: "feature", Priority: 1, Description: "d1", Acceptance: "a1",
			Files: []string{"enc.go"}},
		{Key: "t2", Title: "Add the --json flag", Type: "task", Priority: 2, Description: "d2", Acceptance: "a2",
			Files: []string{"main.go"}, BlockedBy: []string{"t1"}},
	},
	Notes: []string{`dropped "nowhere/x.go" from t2's files: not in the repository`},
}

// --feature makes its own scope, so --ticket with it is a setup problem, as are an empty request
// and --yes without one.
func TestConfigFeatureFlags(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	if c, p := loadWith(t, "--feature", "  Add a --json flag \n", "--yes"); len(p) > 0 || c.Feature != "Add a --json flag" || !c.Yes {
		t.Errorf("feature: %q yes %v, problems %v", c.Feature, c.Yes, p)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--feature", ""}, "--feature needs the request"},
		{[]string{"--feature", " \t\n"}, "--feature needs the request"},
		{[]string{"--feature", "Add a flag", "--ticket", "k-1"}, "--feature can't be combined with --ticket"},
		{[]string{"--yes"}, "--yes only applies with --feature"},
	} {
		if _, p := loadWith(t, tc.args...); !strings.Contains(strings.Join(p, "\n"), tc.want) {
			t.Errorf("%q: problems %q, want %q", tc.args, p, tc.want)
		}
	}
	t.Setenv("ORCHESTRA_TICKET", "k-1")
	if _, p := loadWith(t, "--feature", "Add a flag"); !strings.Contains(strings.Join(p, "\n"), "can't be combined") {
		t.Errorf("ORCHESTRA_TICKET with --feature: problems %q", p)
	}
}

func TestShowPlanIsCompact(t *testing.T) {
	var b strings.Builder
	showPlan(&b, featurePlan)
	want := "\nEpic: JSON output\n  Machine-readable output.\n  For scripts.\n\n2 tickets:\n" +
		"  t1  feature P1  Add the JSON encoder\n      files: enc.go\n" +
		"  t2  task    P2  Add the --json flag\n      files: main.go\n      after: t1\n" +
		"\nChecking the plan:\n  - dropped \"nowhere/x.go\" from t2's files: not in the repository\n\n"
	if b.String() != want {
		t.Errorf("plan shown as\n%s\nwant\n%s", b.String(), want)
	}
}

func TestConfirmTakesYesOnlyAndLeavesTheRest(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, " Yes \nq": true, "n\n": false, "\n": false, "": false, "yep\n": false} {
		r := strings.NewReader(in)
		var out strings.Builder
		got, err := confirm(context.Background(), r, &out, "File these 2 tickets?")
		if err != nil || got != want || out.String() != "File these 2 tickets? [y/N] " {
			t.Errorf("%q: %v %v, asked %q", in, got, err, out.String())
		}
		if in == " Yes \nq" && r.Len() != 1 {
			t.Errorf("confirm read past the answer's line: %d bytes left", r.Len())
		}
	}

	// Ctrl+C at the question stops it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }() // ends the reader left waiting
	if ok, err := confirm(ctx, pr, io.Discard, "File?"); ok || err == nil {
		t.Errorf("cancelled: %v %v", ok, err)
	}
}

// fakeOrganFeature answers the screen and plan organs as given.
type fakeOrganFeature struct {
	screen organ.Screening
	plan   organ.FeaturePlan
}

func (f fakeOrganFeature) Screen(context.Context, organ.Request) (organ.Screening, error) {
	return f.screen, nil
}

func (f fakeOrganFeature) PlanFeature(context.Context, organ.FeatureEvidence) (organ.FeaturePlan, error) {
	return f.plan, nil
}

// fakeFeatureTracker files tickets as f-1 (the epic), f-1.1 and so on, failing the create numbered
// failCreate (from 1) or any dep add when failDep is set.
type fakeFeatureTracker struct {
	created    []beads.NewTicket
	blocks     [][2]string
	failCreate int
	failDep    bool
}

func (*fakeFeatureTracker) Open(context.Context) ([]dispatch.Ticket, []dispatch.Link, error) {
	return nil, nil, nil
}

func (f *fakeFeatureTracker) Create(_ context.Context, t beads.NewTicket) (string, error) {
	if len(f.created)+1 == f.failCreate {
		return "", errors.New("bd create: exit status 1: database is locked")
	}
	f.created = append(f.created, t)
	if len(f.created) == 1 {
		return "f-1", nil
	}
	return "f-1." + string(rune('0'+len(f.created)-1)), nil
}

func (f *fakeFeatureTracker) AddBlock(_ context.Context, blocker, blocked string) error {
	if f.failDep {
		return errors.New("bd dep add: exit status 1: database is locked")
	}
	f.blocks = append(f.blocks, [2]string{blocker, blocked})
	return nil
}

// featureFixture is a feature run of the request in a clean repository, with these organs and
// tracker, and its output.
func featureFixture(t *testing.T, organs featureOrgans, tracker featureTracker, yes, terminal bool, in string) (
	featureRun, *strings.Builder, *strings.Builder,
) {
	t.Helper()
	repo, _ := gitRepo(t)
	var out, errOut strings.Builder
	return featureRun{request: "Add a --json flag", repo: repo, yes: yes, terminal: terminal, organs: organs,
		tracker: tracker, in: strings.NewReader(in), out: &out, err: &errOut}, &out, &errOut
}

func TestFeatureFilesTheConfirmedPlan(t *testing.T) {
	tracker := &fakeFeatureTracker{}
	f, out, errOut := featureFixture(t, fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan},
		tracker, false, true, "y\n")
	epic, code := f.run(context.Background())
	if epic != "f-1" || code != dispatch.ExitOK {
		t.Fatalf("epic %q, exit %d\n%s", epic, code, errOut)
	}
	if !strings.Contains(out.String(), "File these 2 tickets and start the run? [y/N] ") ||
		!strings.Contains(out.String(), "filed epic f-1 with 2 tickets") {
		t.Errorf("output:\n%s", out)
	}
	if len(tracker.created) != 3 {
		t.Fatalf("created %+v", tracker.created)
	}
	e, t1, t2 := tracker.created[0], tracker.created[1], tracker.created[2]
	if e.Type != "epic" || e.Title != "JSON output" || e.Priority != 1 || e.Parent != "" {
		t.Errorf("epic filed as %+v, want an epic at its tickets' highest priority", e)
	}
	if t1.Parent != "f-1" || t1.Type != "feature" || t1.Priority != 1 || t1.Acceptance != "a1" || t1.Files[0] != "enc.go" ||
		t2.Parent != "f-1" || t2.Description != "d2" || t2.Files[0] != "main.go" {
		t.Errorf("tickets filed as %+v and %+v", t1, t2)
	}
	if len(tracker.blocks) != 1 || tracker.blocks[0] != [2]string{"f-1.1", "f-1.2"} {
		t.Errorf("blocks links %v, want f-1.2 after f-1.1", tracker.blocks)
	}
}

func TestFeatureDeclinedFilesNothing(t *testing.T) {
	tracker := &fakeFeatureTracker{}
	f, out, _ := featureFixture(t, fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan},
		tracker, false, true, "n\n")
	if epic, code := f.run(context.Background()); epic != "" || code != dispatch.ExitOK || len(tracker.created) > 0 {
		t.Errorf("declined: epic %q, exit %d, created %v", epic, code, tracker.created)
	}
	if !strings.Contains(out.String(), "Nothing was filed.") {
		t.Errorf("output:\n%s", out)
	}
}

// A bd failure while filing lists what was filed, children first in the delete command, and how to
// carry on.
func TestFeatureFilingFailureListsWhatWasFiled(t *testing.T) {
	ok := fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}
	for _, tc := range []struct {
		name    string
		tracker *fakeFeatureTracker
		want    []string
	}{
		{"epic", &fakeFeatureTracker{failCreate: 1}, []string{"couldn't file the epic: bd create", "Nothing was filed."}},
		{"second ticket", &fakeFeatureTracker{failCreate: 3}, []string{
			"couldn't file ticket t2: bd create: exit status 1: database is locked",
			"Filed before it:\n  f-1 (epic) JSON output\n  f-1.1 Add the JSON encoder\n",
			"Remove them with: bd delete f-1.1 f-1 --force", "carry on with: orchestra --ticket f-1"}},
		{"link", &fakeFeatureTracker{failDep: true}, []string{
			"couldn't file the link t2 after t1: bd dep add", "Remove them with: bd delete f-1.2 f-1.1 f-1 --force"}},
	} {
		f, _, errOut := featureFixture(t, ok, tc.tracker, true, false, "")
		if epic, code := f.run(context.Background()); epic != "" || code != dispatch.ExitTool {
			t.Errorf("%s: epic %q, exit %d", tc.name, epic, code)
		}
		for _, w := range tc.want {
			if !strings.Contains(errOut.String(), w) {
				t.Errorf("%s: stderr lacks %q:\n%s", tc.name, w, errOut)
			}
		}
	}
}
