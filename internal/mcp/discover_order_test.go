package mcp

import (
	"path/filepath"
	"strings"
	"testing"
)

// A worktree and its main checkout can both have local-scope servers in ~/.claude.json: a server
// both define comes from the first root, every time, not from whichever the map gives first.
func TestDiscoverTakesALocalServerFromTheFirstRoot(t *testing.T) {
	home, worktree, main := t.TempDir(), t.TempDir(), t.TempDir()
	config := filepath.Join(home, ".claude.json")
	write(t, config, `{"projects": {
		"`+main+`": {"mcpServers": {"postgres": {"command": "main-pg"}}},
		"`+worktree+`": {"mcpServers": {"postgres": {"command": "worktree-pg"}}}
	}}`)
	// Go's map order is random per range: 50 runs would all pick the right one by chance once in 2^50.
	for range 50 {
		servers, err := Discover(config, worktree, main)
		if err != nil {
			t.Fatal(err)
		}
		if s, _ := Find(servers, "postgres"); !strings.Contains(string(s.Definition), "worktree-pg") {
			t.Fatalf("postgres definition = %s, want the worktree's", s.Definition)
		}
	}
}
