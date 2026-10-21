package main

import (
	"context"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// spike is t labelled spike, as a breakdown files a ticket that checks an unverified fact.
func spike(t dispatch.Ticket) dispatch.Ticket {
	t.Labels = []string{"spike"}
	return t
}

// A spike changes nothing in the repository and carries no files metadata: neither a spec's
// breakdown nor a feature's warns of it, while the other tickets without files metadata are still
// listed.
func TestCheckLeavesSpikesOutOfTheFilesWarning(t *testing.T) {
	t.Parallel()
	spec := `{"spec":".orchestra/specs/app.md"}`
	tree := &specTree{issues: []dispatch.Ticket{
		issue("s-1", "epic", "", spec, ""),
		spike(issue("s-1.1", "task", "s-1", spec, "Says whether the API pages.")),
		issue("s-1.2", "task", "s-1", spec, "Does b."),
	}}
	warnings, err := checkBreakdown(context.Background(), tree, testSpec, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "tickets under s-1 without files metadata, whose files orchestra guesses (1): s-1.2"
	if !slices.Contains(warnings, want) {
		t.Errorf("the breakdown's warnings\n%q\nlack %q", warnings, want)
	}

	f := newFeatureTree()
	f.children["f-1"] = append(f.children["f-1"], "f-1.4")
	f.issues["f-1.4"] = spike(ticketOf("f-1.4", "task", "Check the API pages", "Says whether it pages."))
	_, warnings, err = checkFeature(runCtx(t), f, "f-1")
	if err != nil {
		t.Fatal(err)
	}
	want = "tickets under f-1 without files metadata, whose files orchestra guesses (1): f-1.2"
	if !slices.Contains(warnings, want) {
		t.Errorf("the feature's warnings\n%q\nlack %q", warnings, want)
	}
}
