//go:build darwin || linux

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The pre-flight's fix for a permission mode that asks starts the run's workers in auto: the run
// it starts, and its START line, have the mode the fix set, not the one its configuration settled.
func TestOvernightPermissionFixStartsWorkersInAuto(t *testing.T) {
	t.Parallel()
	preflightWith(t, permissionCheck)
	nothingTools(t, backlog{})
	term, repo, exit := runOnTerminal(t, "--overnight", "--done-so-far", "1", "--limit", "1")
	term.waitFor(t, "Start workers in auto permission mode?")
	term.typeKeys(t, "y", false)
	code, stderr := exit() // the loop ends at once, its limit reached
	lines := read(t, filepath.Join(repo, project.Dir, "orchestra.log"))
	if code != dispatch.ExitOK || !strings.Contains(lines, "LIMIT_REACHED") {
		t.Errorf("exit %d, stderr:\n%s\nthe log:\n%s", code, stderr, lines)
	}
	if !strings.Contains(lines, "overnight pre-flight: fixed: workers start in auto permission mode\n") {
		t.Errorf("the log doesn't say the fix was made:\n%s\nstderr:\n%s", lines, stderr)
	}
	start := ""
	for line := range strings.Lines(lines) {
		if strings.Contains(line, "START orchestra") {
			start = line
		}
	}
	if !strings.Contains(start, ", permission mode: auto,") {
		t.Errorf("the START line doesn't name auto: %q\nthe log:\n%s\nstderr:\n%s", start, lines, stderr)
	}
}
