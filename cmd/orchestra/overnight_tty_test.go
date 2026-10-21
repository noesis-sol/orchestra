//go:build darwin || linux

package main

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// In a terminal, an overnight run's pre-flight offers each fix it can make, and while a failed check
// is left, asks whether to start anyway.

func TestOvernightOnATerminalMakesTheFixTaken(t *testing.T) {
	t.Parallel()
	limit := limitFix()
	preflightWith(t, limit.check)
	nothingTools(t, backlog{})
	term, repo, exit := runOnTerminal(t, "--overnight", "--limit", "10")
	term.waitFor(t, "Run without a limit?")
	term.typeKeys(t, "y", false)
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	out := screenOf(term)
	for _, want := range []string{"◆ Overnight pre-flight: 1 failed", "✗ LIMIT=10 is set",
		"◆ Fixed: the run has no ticket limit", "✓ All done"} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, out)
		}
	}
	if limit.fixed.Load() != 1 {
		t.Errorf("the fix was made %d times, want once", limit.fixed.Load())
	}
	if rec := startRecord(t, repo); rec["overnight"] != true {
		t.Errorf("the start record doesn't say overnight: %v", rec)
	}
}

func TestOvernightOnATerminalAsksWhetherToStartAnyway(t *testing.T) {
	t.Parallel()
	for _, anyway := range []bool{true, false} {
		t.Run(map[bool]string{true: "started", false: "refused"}[anyway], func(t *testing.T) {
			limit := limitFix()
			preflightWith(t, limit.check)
			nothingTools(t, backlog{})
			term, _, exit := runOnTerminal(t, "--overnight", "--limit", "10")
			term.waitFor(t, "Run without a limit?")
			term.typeKeys(t, "n", false)
			term.waitFor(t, "Start the run anyway, with 1 check failed?")
			term.typeKeys(t, map[bool]string{true: "y", false: "n"}[anyway], false)
			code, stderr := exit()
			want := dispatch.ExitOK
			if !anyway {
				want = dispatch.ExitSetup
			}
			if code != want {
				t.Fatalf("exit %d, want %d; stderr:\n%s\nthe terminal:\n%s", code, want, stderr, screenOf(term))
			}
			if !anyway && !strings.Contains(stderr, "orchestra --overnight cannot start: 1 check failed.") {
				t.Errorf("stderr:\n%s", stderr)
			}
			if anyway && !strings.Contains(screenOf(term), "overnight pre-flight: starting with 1 check failed (as answered)") {
				t.Errorf("the terminal doesn't say the run starts anyway:\n%s", screenOf(term))
			}
			if limit.fixed.Load() != 0 {
				t.Error("the fix declined was made")
			}
		})
	}
}
