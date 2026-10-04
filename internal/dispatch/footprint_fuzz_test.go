package dispatch

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// FuzzTicketFootprint reads the footprint of a ticket with any text, metadata and labels (separated
// by commas), against any repository files (one per line, or unknown when known is false) and check
// command. It doesn't panic: funcName indexes a qualifier's first byte, which only the regular
// expressions guarantee. Its files and functions come out sorted, without repeats, and its areas
// sorted, each an area label naming an area. go test runs the seeds; docs/development.md says how
// to fuzz.
func FuzzTicketFootprint(f *testing.F) {
	tracked := strings.Join(trackedHere, "\n")
	for _, c := range []struct{ text, meta, labels string }{
		{"When `Loop.merge` (internal/dispatch/loop.go:1125) aborts, call `o.agentName(id)` and `deliverPrompt`, " +
			"then refreshBranch() and `os.WriteFile(p, b, 0o644)`. The hooks write .orchestra/run/activity.json " +
			"(internal/claude/claude.go). See https://github.com/gastownhall/beads/blob/main/README.md, " +
			"e.g. `go test ./...` and v0.2.0. Not /Users/me/Projects/x/y.go, nor (`solo`, `RESOLVING`).", "", ""},
		{"Picking lives in run.go; `scripts/check.sh` passes.", "", ""},
		{"`synctest.Test(t, func(t *testing.T){…})`, synctest.Wait(), `orDefault(o.wait…)`, func(), go func().", "", ""},
		{"Have `o.close()` and l.len() say so; fix Queue.len() and `Pool.close()`, `n := len(l)`, `[]byte(s)`.", "", ""},
		{"Say hello", `{"files": ["internal/tui/run.go", "docs/new.md"], "team": "x"}`, "scheduling,area:tui,area:"},
		{"Say hello", `"{\"files\":\"internal/tui/run.go docs/new.md\"}"`, "area:b,area:a"},
		{"Say hello", `{"files": "[\"internal/tui/run.go\", 'docs/new.md'"}`, ""},
		{"Say hello", `{"predicted_files": "internal/dispatch/run.go,internal/nowhere/z.go", "files": []}`, ""},
		{"Say hello", `{"files": 3}`, ""},
	} {
		f.Add(c.text, c.meta, c.labels, tracked, true, "scripts/check.sh")
		f.Add(c.text, c.meta, c.labels, "", false, "")
	}
	f.Fuzz(func(t *testing.T, text, meta, labels, files string, known bool, check string) {
		var tracked []string
		if known {
			tracked = strings.Split(files, "\n")
		}
		tk := Ticket{ID: "x", Description: text, Metadata: json.RawMessage(meta), Labels: strings.Split(labels, ",")}
		fp := TicketFootprint(tk, tracked, check)
		for what, l := range map[string][]string{"files": fp.Files, "functions": fp.Funcs} {
			if !slices.IsSorted(l) || len(slices.Compact(slices.Clone(l))) != len(l) {
				t.Errorf("the %s %q are unsorted or repeat", what, l)
			}
		}
		if !slices.IsSorted(fp.Areas) ||
			slices.ContainsFunc(fp.Areas, func(a string) bool { return !strings.HasPrefix(a, AreaPrefix) || a == AreaPrefix }) {
			t.Errorf("the areas %q are unsorted or not area labels", fp.Areas)
		}
	})
}
