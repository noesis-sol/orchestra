package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// A project whose worker prompt is older than orchestra's template hears of it as the run starts, under
// the START line, once.
func TestRunStartSaysTheTemplatesAreNewer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		note := ".orchestra/worker-prompt.md is older than orchestra's worker prompt template, which has since " +
			"changed Close (…): orchestra init --update rewrites those sections"
		h.cfg.TemplateNotes = []string{note}
		h.run() // nothing ready: the run starts, and ends there
		logged := h.logged()
		start := strings.Index(logged, "START orchestra")
		at := strings.Index(logged, "  templates: "+note+"\n")
		if start < 0 || at < start {
			t.Errorf("the log lacks the note after START:\n%s", logged)
		}
		if n := strings.Count(logged, "templates: "); n != 1 {
			t.Errorf("the note is logged %d times:\n%s", n, logged)
		}
	})
}
