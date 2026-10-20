package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A dashboard that opens after its run's loop started counts the run's time from the loop's start.
func TestDashboardStartedAtCountsFromTheLoopsStart(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "main"}, func() {}, func(bool) {}, func(string) {}).
		StartedAt(time.Now().Add(-21 * time.Minute))
	if v := ansi.Strip(m.titleLine(120)); !strings.Contains(v, "21m") {
		t.Errorf("title line %q should count 21m from the loop's start", v)
	}
}
