package dispatch

import (
	"encoding/json"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// FuzzTicketFootprint reads the footprint of a ticket with any text, metadata and labels (separated
// by commas), against any repository files (one per line, or unknown when known is false) and check
// command. It doesn't panic: funcName indexes a qualifier's first byte, which only the regular
// expressions guarantee. Its files and functions come out sorted, without repeats, its files each
// a path in the repository (not empty, not its root, neither absolute nor outside it) and none a
// spec under .orchestra/specs/, the spec its metadata names or a folder (ending in /) unless its
// files metadata lists something, with the repository's files unknown each cleaned (path.Clean, a
// folder's with one /) and none an extension alone (.tsx) unless its files metadata lists something,
// its globs sorted, without repeats, only when its files metadata lists something, each a clean path
// in the repository in NFC holding none of the repository's files (globHolds agrees with glob), and
// its areas sorted, each an area label naming an area. go test runs the seeds; docs/development.md
// says how to fuzz.
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
		{"Say hello", `{"files": ["", "./", ".", "a/..", "/etc/passwd", "../x.go"]}`, ""},
		{"Fix ../x.go and .//y.go", "", ""},
		{"Lay out web/src/cart.ts", `{"files": ["web/src/*.ts", "./web/**/*.ts", "web/src/*/", "web//lib/[a-c]?.ts",
			"web/[src/a.ts", "*.swift", "internal/dispatch/*.go", "internal/dispatch/*.rs", "../web/*.ts"]}`, ""},
	} {
		f.Add(c.text, c.meta, c.labels, tracked, true, "scripts/check.sh")
		f.Add(c.text, c.meta, c.labels, "", false, "")
	}
	// Routes of a Next.js or SvelteKit project, named with brackets, listed with the files known and unknown.
	routes := `{"files": ["pages/[id].tsx", "src/routes/[slug]/", "pages/[id].ts", "src/routes/[a", "pages/\\[x].tsx"]}`
	f.Add("Fix pages/[id].tsx", routes, "", strings.Join(trackedRoutes, "\n"), true, "")
	f.Add("Fix pages/[id].tsx", routes, "", "", false, "")
	// Routes named in a ticket's text, beside a Markdown link, index expressions and an extension alone.
	text := "Fix pages/[id].tsx, app/[[...slug]]/page.tsx and src/routes/[page=fruit]/[a]-[b].ts, not " +
		"[README.md](/README.md), `files[i]`, Footprint.Files[0], a[b c] or the .tsx files (..tsx, x[1:2])."
	f.Add(text, "", "", strings.Join(trackedRoutes, "\n"), true, "")
	f.Add(text, "", "", "", false, "")
	// Tickets broken down from a spec, under .orchestra/specs/ or elsewhere in the repository: citing
	// it, listing it, and listing files in folders that don't exist yet, in a repository with files
	// and in one with none.
	withSpec := strings.Join(append(slices.Clone(trackedWithSpec), "docs/shop-spec.md"), "\n")
	for _, c := range []struct{ text, meta string }{
		{"Build the cart, as docs/shop-spec.md says under Cart (shop-spec.md, ./docs/shop-spec.md:40).",
			`{"spec": "./docs/shop-spec.md"}`},
		{"Revise the spec", `{"spec": "docs/shop-spec.md", "files": "docs/shop-spec.md"}`},
		{"Say hello", `{"spec": "docs/shop-spec.md", "predicted_files": "docs/shop-spec.md,README.md"}`},
		{"Build the cart, as .orchestra/specs/shop.md says under Cart (specs/shop.md, shop.md).", ""},
		{"Revise the spec", `{"files": [".orchestra/specs/shop.md", ".orchestra/specs/"]}`},
		{"Say hello", `{"predicted_files": ".orchestra/specs/shop.md,README.md"}`},
		{"Add the cart in web/src/cart.ts", `{"files": ["web/src/cart.ts", "./web/src/", "Makefile", "../web/x.ts", "/srv/x.ts"]}`},
		{"Lay out web/srcx/a.ts", `{"files": ["web//src/", "web/lib/../src/.", "web/src/ui/..", "web/src", "internal/dispatch/"]}`},
	} {
		f.Add(c.text, c.meta, "", withSpec, true, "")
		f.Add(c.text, c.meta, "", "", true, "")
	}
	f.Fuzz(func(t *testing.T, text, meta, labels, files string, known bool, check string) {
		var tracked []string
		if known {
			tracked = strings.Split(files, "\n")
		}
		tk := Ticket{ID: "x", Description: text, Metadata: json.RawMessage(meta), Labels: strings.Split(labels, ",")}
		fp := TicketFootprint(tk, tracked, check)
		for what, l := range map[string][]string{"files": fp.Files, "globs": fp.Globs, "functions": fp.Funcs} {
			if !slices.IsSorted(l) || len(slices.Compact(slices.Clone(l))) != len(l) {
				t.Errorf("the %s %q are unsorted or repeat", what, l)
			}
		}
		if i := slices.IndexFunc(fp.Files, func(f string) bool { return !filepath.IsLocal(f) || path.Clean(f) == "." }); i >= 0 {
			t.Errorf("the file %q is no path in the repository", fp.Files[i])
		}
		own := specFile(tk)
		spec := func(f string) bool {
			c := path.Clean(f)
			return strings.HasPrefix(c, specsDir) || own != "" && norm.NFC.String(c) == own
		}
		if i := slices.IndexFunc(fp.Files, spec); i >= 0 && len(TicketFiles(tk)) == 0 {
			t.Errorf("the spec %q counts, yet the files metadata lists nothing", fp.Files[i])
		}
		for _, f := range fp.Files {
			folder := strings.HasSuffix(f, "/")
			if folder && len(TicketFiles(tk)) == 0 {
				t.Errorf("the folder %q counts, yet the files metadata lists nothing", f)
			}
			if clean := path.Clean(f); !known && (folder && clean+"/" != f || !folder && clean != f) {
				t.Errorf("the name %q isn't clean, with the repository's files unknown", f)
			}
			ext := strings.TrimPrefix(path.Ext(f), ".")
			if !known && len(TicketFiles(tk)) == 0 && strings.EqualFold(strings.TrimLeft(f, "."), ext) {
				t.Errorf("the extension %q counts as a file, with the repository's files unknown", f)
			}
		}
		for _, g := range fp.Globs {
			switch {
			case len(TicketFiles(tk)) == 0:
				t.Errorf("the glob %q counts, yet the files metadata lists nothing", g)
			case !isGlob(g) || !repoPath(g) || path.Clean(g) != g || !norm.NFC.IsNormalString(g):
				t.Errorf("the glob %q is no glob, no path in the repository, not clean or not in NFC", g)
			}
			// It is kept for matching none of the repository's files, as glob matches them, and holds none.
			for _, f := range tracked {
				if n := norm.NFC.String(f); repoPath(n) && !strings.HasSuffix(n, "/") && globHolds(g, f) {
					t.Errorf("the glob %q is kept, yet it holds %q, one of the repository's files", g, f)
				}
			}
		}
		if !slices.IsSorted(fp.Areas) ||
			slices.ContainsFunc(fp.Areas, func(a string) bool { return !strings.HasPrefix(a, AreaPrefix) || a == AreaPrefix }) {
			t.Errorf("the areas %q are unsorted or not area labels", fp.Areas)
		}
	})
}
