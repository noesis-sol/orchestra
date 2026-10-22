package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// narrowSpecTree is a spec's tree under s-1, amiss in nothing else, whose links make a chain of five, s-1.1 > … > s-1.5, with
// s-1.6 and s-1.7 waiting on s-1.1 as well: seven tickets, at most 1.4 at a time.
func narrowSpecTree() *specTree {
	spec := `{"spec":".orchestra/specs/app.md","files":["a.go"]}`
	root := fmt.Sprintf(`{"spec":%q,"spec_sha256":%q,"spec_commit":%q}`, testSpec.Path, testSpec.SHA256,
		testSpec.Commit)
	tree := &specTree{issues: []dispatch.Ticket{issue("s-1", "epic", "", root, "")}}
	for _, id := range []string{"s-1.1", "s-1.2", "s-1.3", "s-1.4", "s-1.5", "s-1.6", "s-1.7"} {
		tree.issues = append(tree.issues, issue(id, "task", "s-1", spec, "Does it."))
	}
	tree.links = waitsFor("s-1.2", "s-1.1", "s-1.3", "s-1.2", "s-1.4", "s-1.3", "s-1.5", "s-1.4",
		"s-1.6", "s-1.1", "s-1.7", "s-1.1")
	return tree
}

// waitsFor links each pair of IDs, the blocked ticket first.
func waitsFor(pairs ...string) []dispatch.Link {
	var links []dispatch.Link
	for i := 0; i+1 < len(pairs); i += 2 {
		links = append(links, dispatch.Link{Blocked: pairs[i], Blocker: pairs[i+1]})
	}
	return links
}

// A breakdown whose links chain more than three tickets, or let fewer run at a time than the run has
// workers, is warned of, naming the chain and the hubs; the run goes ahead.
func TestCheckBreakdownWarnsOfANarrowTree(t *testing.T) {
	t.Parallel()
	tree := narrowSpecTree()
	warnings, err := checkBreakdown(context.Background(), tree, testSpec, "s-1", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := "a narrow ticket tree, its longest chain over 3 and fewer than the run's 4 tickets at a time: " +
		"s-1: 7 tickets, longest chain 5 (1.1 > 1.2 > 1.3 > 1.4 > 1.5), at most 1.4 at a time; s-1.1 holds 3. " +
		"A ticket needs a blocks link only to one whose code it builds on, so that the rest run side by side"
	if !slices.Equal(warnings, []string{want}) {
		t.Errorf("warnings\n%q\nwant\n%q", warnings, want)
	}

	// With one worker, the chain alone is warned of.
	warnings, _ = checkBreakdown(context.Background(), tree, testSpec, "s-1", 1)
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "a narrow ticket tree, its longest chain over 3: ") {
		t.Errorf("one worker: %q", warnings)
	}

	// Without the chain, s-1.1 holds six and the tree runs 3.5 at a time: under 4.
	tree.links = waitsFor("s-1.2", "s-1.1", "s-1.3", "s-1.1", "s-1.4", "s-1.1", "s-1.5", "s-1.1", "s-1.6", "s-1.1",
		"s-1.7", "s-1.1")
	warnings, _ = checkBreakdown(context.Background(), tree, testSpec, "s-1", 4)
	if want := "a narrow ticket tree, fewer than the run's 4 tickets at a time: s-1: 7 tickets, longest chain 2 " +
		"(1.1 > 1.2), at most 3.5 at a time; s-1.1 holds 6."; len(warnings) != 1 || !strings.HasPrefix(warnings[0], want) {
		t.Errorf("a hub: %q, want %q…", warnings, want)
	}
	if warnings, _ = checkBreakdown(context.Background(), tree, testSpec, "s-1", 3); len(warnings) != 0 {
		t.Errorf("3.5 at a time for 3 workers: %q", warnings)
	}

	// Links read wrong are said, after the cycles.
	tree.fail = "Children"
	warnings, _ = checkBreakdown(context.Background(), tree, testSpec, "s-1", 4)
	if want := []string{"can't read the blocks links under s-1: bd list: database is locked"}; !slices.Equal(warnings,
		want) {
		t.Errorf("links unread: %q, want %q", warnings, want)
	}
}

// A tree without links, or with fewer tickets than workers and none waiting, isn't narrow; closed
// tickets don't count.
func TestShapeWarningsLeaveAWideTreeAlone(t *testing.T) {
	t.Parallel()
	tickets := []dispatch.Ticket{issue("s-1.1", "task", "s-1", "{}", ""), issue("s-1.2", "task", "s-1", "{}", "")}
	closed := issue("s-1.3", "task", "s-1", "{}", "")
	closed.Status = dispatch.StatusClosed
	links := waitsFor("s-1.2", "s-1.3")
	for _, workers := range []int{0, 1, 2, 4} {
		if w := shapeWarnings(dispatch.ShapeOf("s-1", append(tickets, closed), links), workers); len(w) != 0 {
			t.Errorf("%d workers: %q", workers, w)
		}
	}
	if w := shapeWarnings(dispatch.ShapeOf("s-1", nil, nil), 4); len(w) != 0 {
		t.Errorf("no tickets: %q", w)
	}
}

// A feature's epic is checked for its shape as a spec's is, at every level under it.
func TestCheckFeatureWarnsOfANarrowTree(t *testing.T) {
	t.Parallel()
	tree := newFeatureTree()
	tree.issues["f-1.2"] = ticketOf("f-1.2", "task", "Add the flag", "It flags.", "flag.go")
	tree.issues["f-1.3.1"] = ticketOf("f-1.3.1", "feature", "Add CSV", "Writes CSV.", "csv.go")
	tree.links = waitsFor("f-1.2", "f-1.1", "f-1.3.1", "f-1.2")
	_, warnings, err := checkFeature(runCtx(t), tree, "f-1", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := "a narrow ticket tree, fewer than the run's 4 tickets at a time: f-1: 3 tickets, longest chain 3 " +
		"(1.1 > 1.2 > 1.3.1), at most 1 at a time. A ticket needs a blocks link only to one whose code it builds on, " +
		"so that the rest run side by side"
	if !slices.Equal(warnings, []string{want}) {
		t.Errorf("warnings\n%q\nwant\n%q", warnings, want)
	}
}
