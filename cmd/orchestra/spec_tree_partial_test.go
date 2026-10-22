//go:build unix

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/beads"
)

// linkKeeper is a memFiler that keeps the parent and the issues each issue Create files waits for,
// by the ID it gives it.
type linkKeeper struct {
	*memFiler
	parent    map[string]string
	blockedBy map[string][]string
}

func (l *linkKeeper) Create(ctx context.Context, t beads.NewTicket) (string, error) {
	id, err := l.memFiler.Create(ctx, t)
	if err == nil {
		l.parent[id], l.blockedBy[id] = t.Parent, t.BlockedBy
	}
	return id, err
}

// orchestra-xj6z.5: bd failing at any issue of the answer leaves every issue filed with its parent and
// all its links to the issues it waits for, each filed before it: an issue that comes before one it
// waits for in the answer is filed after it, so that a rerun, which files only what is missing, can't
// leave it waiting for nothing.
func TestFileAnswerTreeFailingPartWayKeepsEveryLink(t *testing.T) {
	t.Parallel()
	env := issue("s-1.1", "task", "s-1", `{"spec":".orchestra/specs/app.md"}`, "Sets it up.")
	issues := []specIssue{
		{Key: "docs", Type: "chore", Title: "docs", WaitsFor: []string{"sort", "api"}},
		{Key: "sort", Parent: "listing", Type: "task", Title: "sort", WaitsFor: []string{"list", "s-1.1"}},
		{Key: "listing", Type: "epic", Title: "listing", WaitsFor: []string{"api"}},
		{Key: "list", Parent: "listing", Type: "task", Title: "list", WaitsFor: []string{"api"}},
		{Key: "api", Type: "task", Title: "api"},
		{Key: "ui", Parent: "listing", Type: "task", Title: "ui", WaitsFor: []string{"sort", "list"}},
	}
	for failAt := 1; failAt <= len(issues)+1; failAt++ {
		bd := &linkKeeper{memFiler: newMemFiler(treeRoot, env), parent: map[string]string{},
			blockedBy: map[string][]string{}}
		bd.failAt = failAt
		ids := map[string]string{"s-1.1": "s-1.1", "": "s-1"} // the issues filed, by key
		var order []string
		got, err := fileAnswerTree(context.Background(), bd, testSpec, specAnswer{Root: "s-1", Issues: issues},
			func(f filedIssue, id string) {
				ids[f.Title] = id
				order = append(order, f.Title)
			})
		if want := failAt - 1; failAt <= len(issues) && (err == nil || got.issues != want || len(order) != want) {
			t.Errorf("bd failing at create %d: filed %d (%v), %v; want %d filed and an error", failAt, got.issues,
				order, err, want)
		}
		if failAt > len(issues) && (err != nil || len(order) != len(issues)) {
			t.Errorf("bd not failing: filed %v, %v", order, err)
		}
		for _, is := range issues {
			id, ok := ids[is.Key]
			if !ok {
				continue
			}
			if p, ok := ids[is.Parent]; !ok || bd.parent[id] != p {
				t.Errorf("bd failing at create %d: %s (%s) was filed under %q, its parent %s not filed as %q",
					failAt, is.Key, id, bd.parent[id], is.Parent, p)
			}
			var want []string
			for _, w := range is.WaitsFor {
				b, ok := ids[w]
				if !ok || slices.Index(order, w) > slices.Index(order, is.Key) {
					t.Errorf("bd failing at create %d: %s was filed before %s, which it waits for (%v)", failAt,
						is.Key, w, order)
				}
				want = append(want, b)
			}
			if !slices.Equal(bd.blockedBy[id], want) {
				t.Errorf("bd failing at create %d: %s (%s) waits for %v, want %v", failAt, is.Key, id,
					bd.blockedBy[id], want)
			}
		}
	}
}
