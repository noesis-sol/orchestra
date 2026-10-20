//go:build unix

package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// showCounter is a memFiler that keeps the IDs Show is asked for, and the titles of the issues
// Create is given that aren't Checked.
type showCounter struct {
	*memFiler
	shown, unchecked []string
}

func (s *showCounter) Show(ctx context.Context, id string) (dispatch.Ticket, error) {
	s.shown = append(s.shown, id)
	return s.memFiler.Show(ctx, id)
}

func (s *showCounter) Create(ctx context.Context, t beads.NewTicket) (string, error) {
	if !t.Checked {
		s.unchecked = append(s.unchecked, t.Title)
	}
	return s.memFiler.Create(ctx, t)
}

// orchestra-h5gs: bd is asked once about the root epic and once about each issue filed before the
// answer that it names; the issues it files are Checked, so that Create doesn't ask about their
// parents and the issues they wait for again.
func TestFileAnswerTreeShowsEachFiledIssueOnce(t *testing.T) {
	t.Parallel()
	env := issue("s-1.1", "task", "s-1", `{"spec":".orchestra/specs/app.md"}`, "Sets it up.")
	bd := &showCounter{memFiler: newMemFiler(treeRoot, env)}
	a := specAnswer{Root: "s-1", Issues: []specIssue{
		{Key: "listing", Type: "epic", Priority: 1, Title: "Listing"},
		{Key: "list", Parent: "listing", Type: "task", Priority: 1, Title: "List", WaitsFor: []string{"s-1.1"}},
		{Key: "sort", Parent: "listing", Type: "task", Priority: 2, Title: "Sort", WaitsFor: []string{"list", "s-1.1"}},
		{Key: "docs", Type: "chore", Priority: 3, Title: "Document", WaitsFor: []string{"sort", "later"}},
		{Key: "later", Parent: "s-1.1", Type: "task", Priority: 3, Title: "Later"},
	}}
	got, err := fileAnswerTree(context.Background(), bd, testSpec, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.issues != 5 || got.links != 1 {
		t.Errorf("got %+v; want 5 issues and 1 later link", got)
	}
	if want := []string{"s-1", "s-1.1"}; !reflect.DeepEqual(bd.shown, want) {
		t.Errorf("bd showed %v; want %v", bd.shown, want)
	}
	if len(bd.unchecked) > 0 {
		t.Errorf("filed %q without Checked", bd.unchecked)
	}
}
