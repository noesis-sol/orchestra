package tmux

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Screen gives at most dispatch.ScreenLines lines, the last ones, as every terminal does: tmux gives
// the visible screen with the lines above it.
func TestScreenGivesItsLastLines(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"})
	f.answer("capture-pane", "first\n"+strings.Repeat("line\n", dispatch.ScreenLines+20)+"❯ \n\n\n")
	got := (Terminal{}).Screen(bg, "x-1", dispatch.StateIdle)
	if n := strings.Count(got, "\n"); n != dispatch.ScreenLines || !strings.HasSuffix(got, "line\n❯ \n") {
		t.Errorf("Screen gave %d lines, ending %q", n, got[max(0, len(got)-20):])
	}
}
