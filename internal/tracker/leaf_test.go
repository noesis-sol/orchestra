package tracker

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Beads adapter depends on this package, not on the run loop: none of its files, tests included,
// imports internal/dispatch, and this package imports no other of orchestra's.
func TestBeadsDoesntImportTheRunLoop(t *testing.T) {
	t.Parallel()
	const module = "github.com/noesis-sol/orchestra/"
	for _, dir := range []string{".", "../beads", "../beads/bdjson"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no Go files in %s: %v", dir, err)
		}
		for _, f := range files {
			parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				path, _ := strconv.Unquote(imp.Path.Value) // the parser has checked it is a string
				switch {
				case path == module+"internal/dispatch":
					t.Errorf("%s imports the run loop, %s", f, path)
				case dir == "." && strings.HasPrefix(path, module):
					t.Errorf("%s imports %s: tracker is a leaf", f, path)
				}
			}
		}
	}
}
