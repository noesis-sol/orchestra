package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

// The hook runs in orchestra's own binary on every event of every worker, after the init of every
// package the binary links: none of the modules whose init took most of its start-up (Glamour's
// syntax highlighting, Chroma's lexers and styles, and its HTML and Markdown libraries) is linked.
func TestBinaryLinksNoSlowInit(t *testing.T) {
	t.Parallel()
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("the test binary has no build information")
	}
	if len(bi.Deps) == 0 {
		t.Fatal("the build information lists no modules")
	}
	for _, d := range bi.Deps {
		for _, slow := range []string{"github.com/alecthomas/chroma", "github.com/charmbracelet/glamour",
			"github.com/yuin/goldmark", "github.com/microcosm-cc/bluemonday"} {
			if strings.HasPrefix(d.Path, slow) {
				t.Errorf("orchestra links %s, whose package init every hook would wait for", d.Path)
			}
		}
	}
}
