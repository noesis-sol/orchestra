package dispatch

import (
	"path"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// FuzzGlobsMeet matches globs g and h against path p from the repository's root, as globMatch does a
// prefix of a path's parts (glob), and checks that what a footprint's globs are compared with never
// keeps apart what p shows to meet: a glob matching p holds p and its folder (globHolds), and two
// matching p meet (globsMeet). They may meet where no path shows it: two patterns in a part of a path
// are compared by their literal beginnings and ends alone. go test runs the seeds; docs/development.md
// says how to fuzz.
func FuzzGlobsMeet(f *testing.F) {
	for _, c := range [][3]string{
		{"web/src/*.ts", "web/**/*.ts", "web/src/cart.ts"},
		{"web/src/*.ts", "web/*/cart.ts", "web/src/cart.ts/x"},
		{"*.ts", "c*.ts", "cart.ts"},
		{"*.ts", "*.go", "cart.ts"},
		{"[a-c]?", "b*", "bx"},
		{`\[id].tsx`, "*.tsx", "[id].tsx"},
		{"pages/[id].tsx", "pages/*.tsx", "pages/[id].tsx"},
		{"src/routes/[slug]", `src/*/\[slug]/+page.svelte`, "src/routes/[slug]/+page.svelte"},
		{`a\b`, "a?b", `a\b`},
		{`*\.ts`, "a?.ts", "ab.ts"},
		{"**/x", "a/**", "a/b/x"},
		{"é*", "*é", "éé"},
	} {
		f.Add(c[0], c[1], c[2])
	}
	f.Fuzz(func(t *testing.T, g, h, p string) {
		// A file in the repository, as footprints name them, in NFC as pathParts makes it.
		if p = norm.NFC.String(p); !repoPath(p) || path.Clean(p) != p {
			return
		}
		parts := strings.Split(p, "/")
		matches := func(g string) bool {
			for end := 1; end <= len(parts); end++ {
				if globMatch(strings.Split(g, "/"), parts[:end]) {
					return true
				}
			}
			return false
		}
		if !matches(g) {
			return
		}
		if !globHolds(g, p) {
			t.Errorf("%q matches %q, yet doesn't hold it", g, p)
		}
		if d := path.Dir(p); d != "." && !globHolds(g, d+"/") {
			t.Errorf("%q matches %q, yet doesn't hold its folder %s/", g, p, d)
		}
		if matches(h) && !globsMeet(g, h) {
			t.Errorf("%q and %q both match %q, yet don't meet", g, h, p)
		}
	})
}
