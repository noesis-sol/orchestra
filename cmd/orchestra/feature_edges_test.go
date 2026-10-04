package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// "Nothing was filed." only when bd exited with an error of its own: stopped, killed, or exiting 0
// with output that can't be read, it may have filed what it was asked to, and the note says how
// to check.
func TestFeatureSaysNothingWasFiledOnlyWhenBdRefused(t *testing.T) {
	ok := fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}
	unread := fmt.Errorf("'bd create --json' gave no ID: %q", "Created f-1")
	stopped := &command.Error{Name: "bd", Args: []string{"create"}, Err: errors.New("timed out after 2m"), Stopped: true}
	for _, tc := range []struct {
		name       string
		tracker    *fakeFeatureTracker
		want, lack string
	}{
		{"epic, bd exited 1", &fakeFeatureTracker{failCreate: 1}, "Nothing was filed.", "may have filed"},
		{"epic, output unread", &fakeFeatureTracker{failCreate: 1, failWith: unread},
			"bd may have filed the epic all the same: check with: bd list --type epic\n", "Nothing was filed."},
		{"epic, bd stopped", &fakeFeatureTracker{failCreate: 1, failWith: stopped},
			"bd was stopped and may have filed the epic before it did: check with: bd list --type epic\n",
			"Nothing was filed."},
		{"ticket, bd exited 1", &fakeFeatureTracker{failCreate: 3}, "Filed before it:", "may have filed"},
		{"ticket, output unread", &fakeFeatureTracker{failCreate: 3, failWith: unread},
			"bd may have filed ticket t2 all the same: check with: bd list --parent f-1\n", "Nothing was filed."},
	} {
		f, _, errOut := featureFixture(t, ok, tc.tracker, true, false, "")
		if epic, code := f.run(context.Background()); epic != "" || code != dispatch.ExitTool {
			t.Errorf("%s: epic %q, exit %d", tc.name, epic, code)
		}
		if !strings.Contains(errOut.String(), tc.want) || strings.Contains(errOut.String(), tc.lack) {
			t.Errorf("%s: stderr should have %q and not %q:\n%s", tc.name, tc.want, tc.lack, errOut)
		}
	}
}

// A bd killed by a signal, not stopped by orchestra, may have filed the epic before it went. The
// shell kills itself with SIGTERM, not SIGINT: a background job (cmd &) starts with SIGINT ignored,
// and a shell that inherits that survives its own SIGINT.
func TestFeatureBdKilledMayHaveFiled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals")
	}
	_, killed := command.Output(context.Background(), 0, "", "sh", "-c", "kill -TERM $$")
	var cmdErr *command.Error
	if !errors.As(killed, &cmdErr) || cmdErr.Stopped {
		t.Fatalf("not a killed command: %#v", killed)
	}
	ok := fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan}
	f, _, errOut := featureFixture(t, ok, &fakeFeatureTracker{failCreate: 1, failWith: killed}, true, false, "")
	if epic, code := f.run(context.Background()); epic != "" || code != dispatch.ExitTool {
		t.Errorf("epic %q, exit %d", epic, code)
	}
	if s := errOut.String(); !strings.Contains(s, "signal: terminated") ||
		!strings.Contains(s, "check with: bd list --type epic") || strings.Contains(s, "Nothing was filed.") {
		t.Errorf("stderr:\n%s", s)
	}
}

// A link that couldn't be added is named by its tickets' IDs, and every link not added, the
// failing one included, comes with the bd command that adds it.
func TestFeatureFailedLinkListsTheLinksToAdd(t *testing.T) {
	plan := featurePlan
	plan.Tickets = append(plan.Tickets[:2:2], organ.PlannedTicket{Key: "t3", Title: "Document --json",
		Type: "task", Priority: 3, BlockedBy: []string{"t1", "t2"}})
	tracker := &fakeFeatureTracker{failDep: 2}
	f, _, errOut := featureFixture(t, fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, plan},
		tracker, true, false, "")
	if epic, code := f.run(context.Background()); epic != "" || code != dispatch.ExitTool {
		t.Errorf("epic %q, exit %d", epic, code)
	}
	s := errOut.String()
	for _, want := range []string{
		"orchestra couldn't file the link f-1.3 (t3) after f-1.1 (t1): bd dep add f-1.3 f-1.1: exit status 1",
		"Filed before it:\n  f-1 (epic) JSON output\n  f-1.1 (t1) Add the JSON encoder\n" +
			"  f-1.2 (t2) Add the --json flag\n  f-1.3 (t3) Document --json\n",
		"Remove them with: bd delete f-1.3 f-1.2 f-1.1 f-1 --force\n",
		"Or add the links that are missing and carry on:\n  bd dep add f-1.3 f-1.1\n  bd dep add f-1.3 f-1.2\n" +
			"  orchestra --ticket f-1\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stderr lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "bd dep add f-1.2 f-1.1") || strings.Contains(s, "file the rest") {
		t.Errorf("stderr lists the link that was added, or tickets to file:\n%s", s)
	}
	if len(tracker.blocks) != 1 || tracker.blocks[0] != [2]string{"f-1.1", "f-1.2"} {
		t.Errorf("links added: %v", tracker.blocks)
	}
}

// Ctrl+C while the main checkout's state is read stops the feature run as it does elsewhere.
func TestFeatureCtrlCDuringTheDirtyCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f, out, errOut := featureFixture(t, fakeOrganFeature{organ.Screening{Verdict: organ.ScreenOK}, featurePlan},
		&fakeFeatureTracker{}, true, false, "")
	if epic, code := f.run(ctx); epic != "" || code != dispatch.ExitInterrupted {
		t.Errorf("epic %q, exit %d, want %d", epic, code, dispatch.ExitInterrupted)
	}
	if s := errOut.String(); s != "orchestra: stopped before filing; nothing was filed.\n" {
		t.Errorf("stderr:\n%s", s)
	}
	if strings.Contains(out.String(), "screening") {
		t.Errorf("screened after Ctrl+C:\n%s", out)
	}
}

// A feature run with no ticket left under the limit would file its plan and start none of it, so
// it doesn't start; a run without --feature still ends at once with LIMIT_REACHED.
func TestFeatureNeedsRoomUnderTheLimit(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	const want = "--feature would file a plan and run none of it"
	for _, tc := range []struct {
		env  map[string]string
		args []string
	}{
		{map[string]string{"DONE_SO_FAR": "40"}, nil},
		{map[string]string{"LIMIT": "0"}, nil},
		{nil, []string{"--limit", "3", "--done-so-far", "5"}},
	} {
		for k, v := range tc.env {
			t.Setenv(k, v)
		}
		_, p := loadWith(t, append([]string{"--feature", "Add a flag", "--yes"}, tc.args...)...)
		if len(p) != 1 || !strings.Contains(p[0], want) {
			t.Errorf("%v %q: problems %q", tc.env, tc.args, p)
		}
		if _, p := loadWith(t, tc.args...); len(p) > 0 {
			t.Errorf("%v %q without --feature: problems %q", tc.env, tc.args, p)
		}
		for k := range tc.env {
			t.Setenv(k, "")
		}
	}
	if _, p := loadWith(t, "--feature", "Add a flag", "--limit", "3", "--done-so-far", "2"); len(p) > 0 {
		t.Errorf("one ticket left: problems %q", p)
	}
}
