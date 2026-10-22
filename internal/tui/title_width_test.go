package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The title line takes the pane's full width: the blank line above it, the title pill's margin, isn't
// counted as part of it, so a pane exactly as wide as the line shows it whole, badge, branch and time,
// and a pane a column narrower cuts it.
func TestTitleLineFitsItsFullWidth(t *testing.T) {
	for _, overnight := range []bool{false, true} {
		m := NewDashboard(dispatch.Config{Version: "0.9.0", Base: "main", Concurrency: 2, Overnight: overnight},
			func() {}, func(bool) {}, func(string) {}).StartedAt(time.Now().Add(-21 * time.Minute))
		whole := ansi.Strip(m.titleLine(500))
		margin, line, ok := strings.Cut(whole, "\n")
		if !ok || strings.TrimSpace(margin) != "" || strings.Contains(line, "\n") {
			t.Fatalf("title %q: want a blank line, then the title line", whole)
		}
		w := ansi.StringWidth(line)
		if got := ansi.Strip(m.titleLine(w)); got != whole {
			t.Errorf("overnight %v: at the line's own width %d the title is %q; want it whole, %q", overnight, w, got, whole)
		}
		_, short, _ := strings.Cut(ansi.Strip(m.titleLine(w-1)), "\n")
		if short == line || ansi.StringWidth(short) > w-1 {
			t.Errorf("overnight %v: a column short, at %d, the title line is %q; want it cut to fit", overnight, w-1, short)
		}
	}
}
