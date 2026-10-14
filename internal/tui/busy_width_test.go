package tui

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// A terminal that gives no width, as a fresh pseudo-terminal can say 0 columns, still has its busy
// line cut to one line, so that each frame is drawn over the last rather than below it.
func TestBusyLineWithoutAWidthIsCut(t *testing.T) {
	for _, width := range []int{0, 1} {
		synctest.Test(t, func(t *testing.T) {
			var b strings.Builder
			busy := Terminal(&b, width).Busy("", strings.Repeat("Writing the run report ", 10), "(Ctrl+C skips)")
			time.Sleep(500 * time.Millisecond)
			busy.Done("Run report written")
			frames := strings.Split(ansi.Strip(b.String()), "\r")
			for _, f := range frames[:len(frames)-1] {
				if w := ansi.StringWidth(f); w > 79 {
					t.Errorf("width %d: a frame %d columns wide: %q", width, w, f)
				}
			}
			if len(frames) < 3 {
				t.Errorf("width %d: drew %d frames", width, len(frames))
			}
		})
	}
}
