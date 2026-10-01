package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestPrerequisitesLeadWithACrossWhenOneIsMissing(t *testing.T) {
	for _, tc := range []struct {
		pre  []project.Step
		lead string
	}{
		{[]project.Step{{Kind: project.StepDone, Label: "bd"}}, "✓"},
		{[]project.Step{{Kind: project.StepDone, Label: "bd"}, {Kind: project.StepMissing, Label: "herdr", Detail: "install Herdr"}}, "✗"},
	} {
		var b strings.Builder
		InitScreen{out: &b, width: 80}.Prerequisites(tc.pre)
		if got := strings.TrimSpace(ansi.Strip(b.String())); !strings.HasPrefix(got, tc.lead+" needs") {
			t.Errorf("want %s first: %q", tc.lead, got)
		}
	}
}

func TestConcurrencyOptionsOfferOnlyValidSettings(t *testing.T) {
	has := func(opts []string, n string) bool {
		for _, o := range opts {
			if strings.HasPrefix(o, n+" ") {
				return true
			}
		}
		return false
	}
	keys := func(current int) []string {
		var ks []string
		for _, o := range concurrencyOptions(current) {
			ks = append(ks, o.Key)
		}
		return ks
	}
	if !has(keys(12), "12") {
		t.Error("a valid current setting is offered")
	}
	if has(keys(project.MaxConcurrency+4), "20") {
		t.Error("an out-of-range setting is offered")
	}
}

var testServers = []mcp.Server{
	{Name: "exa", Scope: mcp.ScopeUser, Type: "http"},
	{Name: "postgres", Scope: mcp.ScopeLocal, Type: "stdio"},
	{Name: "claude.ai Gmail", Scope: mcp.ScopeClaudeAI},
}

func TestMCPOptionsPreselectTheCurrentSetting(t *testing.T) {
	keys := func(opts []huh.Option[string]) string {
		var ks []string
		for _, o := range opts {
			ks = append(ks, o.Key)
		}
		return strings.Join(ks, "\n")
	}
	opts, selected := mcpOptions(project.Choice{Servers: testServers})
	if want := "exa  · user scope, http\npostgres  · local scope, stdio"; keys(opts) != want || len(selected) != 0 {
		t.Errorf("first run: options\n%s\nselected %v", keys(opts), selected)
	}
	current := []string{"postgres", "redis", "claude.ai Gmail"}
	opts, selected = mcpOptions(project.Choice{Servers: testServers, MCP: &current})
	if !strings.Contains(keys(opts), "redis  · not defined on this machine") || strings.Contains(keys(opts), "Gmail") {
		t.Errorf("options:\n%s", keys(opts))
	}
	if strings.Join(selected, ",") != "postgres,redis" {
		t.Errorf("selected = %v", selected)
	}
	// The field shows the current setting selected.
	view := ansi.Strip(huh.NewMultiSelect[string]().Options(opts...).Value(&selected).WithTheme(huh.ThemeBase()).View())
	if !strings.Contains(view, "[•] postgres") || !strings.Contains(view, "[ ] exa") {
		t.Errorf("the current setting isn't shown selected:\n%s", view)
	}
}

func TestMCPDescriptionNamesTheConnectors(t *testing.T) {
	d := mcpDescription(project.Choice{Servers: testServers})
	if !strings.Contains(d, "Not available to workers (claude.ai connector): Gmail.") {
		t.Errorf("description: %q", d)
	}
	if strings.Contains(mcpDescription(project.Choice{Servers: testServers[:2]}), "claude.ai") {
		t.Error("connectors mentioned without any")
	}
}

func TestAskInitWithNoMCPServersToOfferChoosesNone(t *testing.T) {
	c := project.Choice{Servers: testServers[2:]}
	if err := AskInit(strings.NewReader(""), io.Discard, &c, false, false, false, false, true); err != nil {
		t.Fatal(err)
	}
	if c.MCP == nil || len(*c.MCP) != 0 {
		t.Errorf("MCP = %v, want none", c.MCP)
	}
}
