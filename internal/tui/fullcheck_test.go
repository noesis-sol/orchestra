package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The full check's line after the run: a tick when it passed, a warning otherwise.
func TestFullCheckLines(t *testing.T) {
	for detail, mark := range map[string]string{
		dispatch.FullCheckPassed:  "✓ FULL_CHECK",
		dispatch.FullCheckFailed:  "! FULL_CHECK",
		dispatch.FullCheckSkipped: "! FULL_CHECK",
	} {
		line := ansi.Strip(renderEvent(dispatch.Event{Kind: dispatch.EvFullCheck, Detail: detail, Time: time.Now(),
			Text: "  FULL_CHECK something"}))
		if !strings.Contains(line, mark+" something") {
			t.Errorf("%s: %q", detail, line)
		}
	}
}
