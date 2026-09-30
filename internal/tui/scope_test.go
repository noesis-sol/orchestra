package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A run scoped to one ticket names it on the title line, after the branch.
func TestDashboardTitleShowsTheScope(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Ticket: "k-bl0"}, func() {}, func(bool) {})
	if v := ansi.Strip(m.titleLine(120)); !strings.Contains(v, "batch · ticket k-bl0 · ") {
		t.Errorf("title line %q should name the scope", v)
	}
	m = NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {})
	if v := ansi.Strip(m.titleLine(120)); strings.Contains(v, "ticket") {
		t.Errorf("title line %q names a scope the run doesn't have", v)
	}
}
