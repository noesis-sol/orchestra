package dispatch

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// leaveFor leaves a closed ticket unmerged for its kind, labelled, and warns with the kind first.
func TestLeaveForLabelsAndWarnsWithTheKind(t *testing.T) {
	t.Parallel()
	b := newFakeBeads()
	b.add("A", "a", 2)
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &runSink{held: make(chan struct{})}
	o := New(Config{Base: "main"}, log, "", Deps{Notes: b, Merger: upToDate{}})
	o.SetSink(sink)

	o.leaveFor(context.Background(), "A", asideConflict, "conflicts with main", "merge conflict in a.go",
		"A closed, but wt/A conflicts with main")
	want := Event{Kind: EvWarn, Ticket: "A", Aside: true, Detail: "conflicts with main",
		Text: "  MERGE_CONFLICT: A closed, but wt/A conflicts with main"}
	if len(sink.events) != 1 || !sameEvent(sink.events[0], want) || sink.events[0].Blocked != "merge conflict in a.go" {
		t.Errorf("events %+v, want only %+v, blocked by a merge conflict in a.go", sink.events, want)
	}
	if got := o.unmergedWhy("A"); got != "MERGE_CONFLICT" {
		t.Errorf("A is unmerged for %q, want MERGE_CONFLICT", got)
	}
	if a, _ := b.Show(context.Background(), "A"); !slices.Contains(a.Labels, UnmergedLabel) {
		t.Errorf("A's labels %v, without %s", a.Labels, UnmergedLabel)
	}
	if got := o.asideList(); !equal(got, []string{"A"}) {
		t.Errorf("set aside: %v", got)
	}
}
