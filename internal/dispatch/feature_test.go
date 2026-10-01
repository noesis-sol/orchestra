package dispatch

import (
	"context"
	"strings"
	"testing"
)

// A run planned from a feature request (--feature) names the feature and its epic in the START
// log and in the run report's title and evidence.
func TestFeatureRunNamesTheFeatureAndTheEpic(t *testing.T) {
	t.Parallel()
	h := scopedHarness(t, "E")
	h.cfg.Feature = "Add a --json flag\nto every   list command"
	h.beads.add("E", "JSON output", 1)
	h.beads.kind("E", "epic")
	h.beads.sub("E.1", "E", "Add the encoder", 1)
	h.worker("E.1", finishes("e1.txt"))
	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d\n%s", code, h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{" on main · feature E (", "feature: epic E, planned from: Add a --json flag to every list command"} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	in := o.reviewInput(context.Background(), code, o.Final())
	for _, want := range []string{"Run of epic E and its subtickets only, filed from a feature request",
		"## The feature request epic E was planned from\n\n<evidence id=\"", "\">\nAdd a --json flag\nto every   list command\n</evidence"} {
		if !strings.Contains(in, want) {
			t.Errorf("report input lacks %q:\n%s", want, in)
		}
	}
}

func TestScopeLabel(t *testing.T) {
	for _, tc := range []struct {
		c    Config
		want string
	}{
		{Config{}, ""},
		{Config{Ticket: "k-1"}, " · ticket k-1"},
		{Config{Ticket: "k-1", Feature: "Add a flag"}, " · feature k-1"},
	} {
		if got := ScopeLabel(tc.c); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.c, got, tc.want)
		}
	}
	if got := FeatureLine(strings.Repeat("é", 250)); got != strings.Repeat("é", 200)+"…" {
		t.Errorf("a long request is cut to %d characters", len([]rune(got)))
	}
}
