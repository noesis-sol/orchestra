package mcp

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Another repository's record in ~/.claude.json, in a shape orchestra doesn't understand, doesn't
// fail Discover: only the repository's own is read.
func TestDiscoverIgnoresAnotherProjectsRecord(t *testing.T) {
	config, repo := filepath.Join(t.TempDir(), ".claude.json"), t.TempDir()
	write(t, config, `{"projects": {
		"/elsewhere": {"mcpServers": ["postgres"]},
		"/other": "a string",
		"`+repo+`": {"mcpServers": {"exa": {"command": "exa"}}}
	}}`)
	servers, err := Discover(config, repo)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(servers), []string{"exa " + ScopeLocal}; !reflect.DeepEqual(got, want) {
		t.Errorf("servers = %q, want %q", got, want)
	}
}
