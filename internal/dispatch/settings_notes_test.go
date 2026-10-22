package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// A key of settings.json orchestra doesn't know is named as the run starts, under the START line, once,
// and the run goes on.
func TestRunStartNamesUnknownSettings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		note := ".orchestra/settings.json: concurency is no setting this orchestra knows, and does nothing"
		h.cfg.SettingsNotes = []string{note}
		h.run() // nothing ready: the run starts, and ends there
		logged := h.logged()
		start := strings.Index(logged, "START orchestra")
		at := strings.Index(logged, "  settings: "+note+"\n")
		if start < 0 || at < start {
			t.Errorf("the log lacks the note after START:\n%s", logged)
		}
		if n := strings.Count(logged, "settings: .orchestra"); n != 1 {
			t.Errorf("the note is logged %d times:\n%s", n, logged)
		}
	})
}
