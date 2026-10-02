package organ

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A ticket left in progress by a stopped run, or set aside, is still to be done: the plan sees
// every ticket that isn't closed, with its status, and is told not to repeat any of them.
func TestPlanSeesEveryUnclosedTicket(t *testing.T) {
	ev := planEvidence()
	ev.Unclosed = []UnclosedTicket{{"k-1", "open", "Faster listing"}, {"k-2", "in_progress", "Retry merges"},
		{"k-3", "blocked", "Colour output"}, {"k-4", "deferred", "Add --json to list"}}
	in := planInput(ev)
	want := "## Tickets not closed (ID, status and title)\n\n<evidence id=\"" + evidenceIDs(t, in)[0] + "\">\n" +
		"k-1  open  Faster listing\nk-2  in_progress  Retry merges\nk-3  blocked  Colour output\n" +
		"k-4  deferred  Add --json to list\n</evidence"
	if !strings.Contains(in, want) {
		t.Errorf("input lacks\n%s\nin\n%s", want, in)
	}
	if !strings.Contains(planSystem, "a ticket that isn't closed already covers, whatever its status: open, in "+
		"progress, blocked or deferred") {
		t.Error("the system prompt should say which tickets not to repeat")
	}
}

// An empty blocked_by entry names no ticket, so it orders nothing; it doesn't cost the plan.
func TestPlanIgnoresAnEmptyBlocker(t *testing.T) {
	plan := ticketsJSON(`{"blocked_by":[""]}`, `{"blocked_by":[" ","t1",""]}`)
	p, err := parsePlan(Result{Structured: []byte(plan)}, planTracked)
	if err != nil {
		t.Fatal(err)
	}
	if t1, t2 := p.Tickets[0].BlockedBy, p.Tickets[1].BlockedBy; len(t1) != 0 || !slices.Equal(t2, []string{"t1"}) {
		t.Errorf("blocked_by %q and %q, want none and t1", t1, t2)
	}
}

// A path with a line reference after it, the form footprints use, names the file.
func TestNamedPathsTakeOffALineReference(t *testing.T) {
	tracked := []string{"internal/x/y.go", "a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	got := NamedPaths("See internal/x/y.go:120. Also a.go:12-30, b.go:3:7, c.go#L5, (d.go#L5-L9) and e.go#L5-9:"+
		" not f.go:x or f.go#5.", tracked)
	if want := []string{"internal/x/y.go", "a.go", "b.go", "c.go", "d.go", "e.go"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "x", "y.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev, err := GatherFeature(context.Background(), dir, "Fix the merge at internal/x/y.go:120", tracked[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ev.Named, []NamedFile{{"internal/x/y.go", "package x\n"}}) {
		t.Errorf("named = %q", ev.Named)
	}
}

// Ctrl+C stops the gathering: once ctx is done, GatherFeature counts no more lines and returns
// ctx's error, whether or not there are files to count.
func TestGatherFeatureStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tracked := range [][]string{{"a.go"}, nil} {
		if _, err := GatherFeature(ctx, dir, "r", tracked, nil); !errors.Is(err, context.Canceled) {
			t.Errorf("tracked %q: err = %v, want context.Canceled", tracked, err)
		}
	}
}

// cancelledAfter is a context whose Err reports it cancelled from its call numbered n+1 on.
type cancelledAfter struct {
	context.Context
	n int
}

func (c *cancelledAfter) Err() error {
	if c.n--; c.n < 0 {
		return context.Canceled
	}
	return nil
}

// A file is counted a chunk at a time, stopping when ctx ends; one larger than maxCounted isn't
// counted at all, so large tracked files don't hold up the plan.
func TestCountLinesStopsEarly(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("line\n", size/5)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fits.txt", maxCounted)
	write("large.txt", maxCounted+5)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ctx := context.Background()
	if got, want := countLines(ctx, root, "fits.txt"), maxCounted/5; got != want {
		t.Errorf("fits.txt: %d lines, want %d", got, want)
	}
	if got := countLines(ctx, root, "large.txt"); got != -1 {
		t.Errorf("large.txt: %d lines, want -1 (not counted)", got)
	}
	if got := countLines(&cancelledAfter{ctx, 1}, root, "fits.txt"); got != -1 {
		t.Errorf("cancelled after the first chunk: %d lines, want -1", got)
	}
}
