package tui

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The MCP servers' names and types come from config files, .mcp.json a teammate's: init's
// multi-select shows them printable, without what a terminal would act on, and still chooses the
// server by its name as defined.
func TestMCPOptionsShowNamesPrintable(t *testing.T) {
	evil := "pg\x1b]52;c;cm0gLXJmIH4=\x07\nmcp"
	servers := []mcp.Server{{Name: evil, Scope: mcp.ScopeProject, Type: "stdio\x1b[2J"},
		{Name: "claude.ai Gm\x1b[2Jail", Scope: mcp.ScopeClaudeAI}}
	chosen := []string{"redis\x1b[2J"}
	opts, _ := mcpOptions(project.Choice{Servers: servers, MCP: &chosen})
	if len(opts) != 2 {
		t.Fatalf("options %v, want two", opts)
	}
	for _, o := range opts {
		if strings.ContainsAny(o.Key, "\x1b\x07\n") {
			t.Errorf("label %q isn't printable", o.Key)
		}
	}
	if opts[0].Key != "pg mcp  · project scope, stdio" || opts[0].Value != evil {
		t.Errorf("option %q for %q, want the printable name for the name as defined", opts[0].Key, opts[0].Value)
	}
	if d := mcpDescription(project.Choice{Servers: servers}); strings.ContainsAny(d, "\x1b\x07") {
		t.Errorf("description %q isn't printable", d)
	}
}
