package dispatch

import (
	"maps"
	"slices"
	"testing"
)

// Each scanner of a ticket's text finds its own kind of name: paths, calls with their parentheses,
// and code spans.
func TestFootprintScanners(t *testing.T) {
	t.Parallel()
	repo := newRepoFiles([]string{"internal/dispatch/run.go", "internal/dispatch/loop.go", "scripts/check.sh"},
		"scripts/check.sh")
	cases := []struct {
		name, text          string
		files, funcs, calls []string
		spans               []string
	}{
		{name: "nothing", text: "Make the dashboard calmer."},
		{name: "bare and partial paths", text: "Split run.go and dispatch/loop.go; scripts/check.sh passes.",
			files: []string{"internal/dispatch/loop.go", "internal/dispatch/run.go"}},
		{name: "a new file in a known folder", text: "Add internal/dispatch/solo.go.",
			files: []string{"internal/dispatch/solo.go"}},
		{name: "a method named like a path", text: "Loop.merge holds the lock.", funcs: []string{"Loop.merge"}},
		{name: "calls", text: "refreshBranch() and o.log.Raw() and Loop.merge(), not len() or os.WriteFile().",
			funcs: []string{"Loop.merge"}, calls: []string{"Loop.merge", "Raw", "refreshBranch"}},
		{name: "code spans", text: "`waitSettled`, `o.agentName(id)`, `solo`, `GitHub`.",
			spans: []string{"agentName", "waitSettled"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, funcs := pathsIn(c.text, repo, "")
			for _, got := range []struct {
				what string
				set  map[string]bool
				want []string
			}{{"files", files, c.files}, {"Type.method", funcs, c.funcs}, {"calls", callsIn(c.text), c.calls},
				{"code spans", codeSpansIn(c.text), c.spans}} {
				if s := slices.Sorted(maps.Keys(got.set)); !slices.Equal(s, got.want) {
					t.Errorf("%s: got %q, want %q", got.what, s, got.want)
				}
			}
		})
	}
}

// Two running tickets' workers editing the same file are found once per pair and file, in the
// order of the tickets' IDs.
func TestSharedEdits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		edited map[string][]string
		want   []edit
	}{
		{name: "none running"},
		{name: "one running", edited: map[string][]string{"a": {"x.go"}}},
		{name: "apart", edited: map[string][]string{"a": {"x.go"}, "b": {"y.go"}}},
		{name: "one file", edited: map[string][]string{"b": {"y.go", "x.go"}, "a": {"x.go"}},
			want: []edit{{"a", "b", "x.go"}}},
		{name: "as a lists them", edited: map[string][]string{"a": {"y.go", "x.go"}, "b": {"x.go", "y.go"}},
			want: []edit{{"a", "b", "y.go"}, {"a", "b", "x.go"}}},
		{name: "three tickets", edited: map[string][]string{"c": {"x.go"}, "a": {"x.go"}, "b": {"x.go", "z.go"}},
			want: []edit{{"a", "b", "x.go"}, {"a", "c", "x.go"}, {"b", "c", "x.go"}}},
		{name: "nothing edited yet", edited: map[string][]string{"a": nil, "b": nil}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sharedEdits(c.edited); !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
