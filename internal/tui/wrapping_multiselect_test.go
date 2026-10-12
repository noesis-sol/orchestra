package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// mcpForm is the MCP server question as init asks it, drawn on a terminal width columns wide.
func mcpForm(width int, servers *[]string) (*huh.Form, *wrappingMultiSelect[string]) {
	chrome := append(slices.Clip(testServers), mcp.Server{Name: mcp.Chrome, Scope: mcp.ScopeBuiltIn})
	opts, _ := mcpOptions(project.Choice{Servers: chrome})
	field := newWrappingMultiSelect(huh.NewMultiSelect[string]().Title("MCP servers for workers").
		Filterable(false), servers, opts...)
	form := huh.NewForm(huh.NewGroup(&formField{Field: field}).Title("Step 1 of 2 · Workers")).
		WithTheme(huh.ThemeCharm())
	form.Init()
	form.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	return form, field
}

func TestMCPOptionsWrapUnderTheirLabel(t *testing.T) {
	const label = "claude-in-chrome  · built into Claude Code, drives Chrome on this machine"
	for width := 60; width <= 100; width++ {
		form, _ := mcpForm(width, &[]string{"postgres"})
		lines := strings.Split(ansi.Strip(form.View()), "\n")
		start := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "claude-in-chrome") })
		if start < 0 {
			t.Fatalf("width %d: no claude-in-chrome option:\n%s", width, strings.Join(lines, "\n"))
		}
		// The label's column, counted in runes: each of the field's is one column wide.
		column := len([]rune(lines[start][:strings.Index(lines[start], "claude-in-chrome")]))
		text := []string{string([]rune(lines[start])[column:])}
		for _, l := range lines[start+1:] {
			r := []rune(l)
			if len(r) <= column || strings.TrimSpace(string(r[1:column])) != "" || r[column] == ' ' {
				break // not a line of the label: wrapped under it, it has only spaces left of it
			}
			text = append(text, string(r[column:]))
		}
		got := strings.Join(strings.Fields(strings.Join(text, " ")), " ")
		if want := strings.Join(strings.Fields(label), " "); got != want {
			t.Errorf("width %d: the option reads %q, want %q:\n%s", width, got, want, strings.Join(lines, "\n"))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > width {
				t.Errorf("width %d: line wider than the terminal: %q", width, l)
			}
		}
	}
}

func TestMCPOptionsKeepTheCursorWhenTheyWrapAgain(t *testing.T) {
	servers := []string{"postgres"}
	form, field := mcpForm(100, &servers)
	for range 2 { // from exa, the first, to claude-in-chrome, the last
		form.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if hovered, _ := field.Hovered(); hovered != mcp.Chrome {
		t.Fatalf("hovered %q, want %q", hovered, mcp.Chrome)
	}
	form.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	if hovered, _ := field.Hovered(); hovered != mcp.Chrome {
		t.Errorf("after the resize, hovered %q, want %q", hovered, mcp.Chrome)
	}
	if !slices.Equal(servers, []string{"postgres"}) {
		t.Errorf("after the resize, selected %v", servers)
	}
}

func TestMCPOptionsAreWholeInTheAccessibleForm(t *testing.T) {
	_, field := mcpForm(60, &[]string{})
	field.WithAccessible(true) //nolint:staticcheck // as huh's accessible form calls it
	var out strings.Builder
	_ = field.RunAccessible(&out, strings.NewReader("0\n")) // answered with 0: confirm the selection
	if !strings.Contains(out.String(), "claude-in-chrome  · built into Claude Code, drives Chrome on this machine\n") {
		t.Errorf("the accessible form's options:\n%s", out.String())
	}
}
