package dispatch

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// With ProbeHooks, as for a terminal that reads its workers' state from their hooks, the probe's
// Claude worker reports through hooks writing to its own folder of the main checkout's
// .orchestra/run/; without, it starts as it always has, its arguments untouched by the reporter.
func TestTheProbeReportsThroughHooksWhenAsked(t *testing.T) {
	t.Parallel()
	for _, hooks := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.reporter = fakeReporter{}
			h.holdForEnvironment(time.Minute)
			h.cfg.EnvProbe = time.Minute
			h.cfg.ProbeHooks = hooks
			h.beads.add("A", "a", 1)
			h.beads.add("B", "b", 2)
			h.worker("A", givesUp, finishes("a.txt"))
			h.worker("B", givesUp, finishes("b.txt"))
			h.worker(probeID, runsProbe)

			if _, code := h.run(); code != ExitOK {
				t.Fatalf("hooks %v: exit code %d, want %d:\n%s", hooks, code, ExitOK, h.logged())
			}
			want := []string{"start", "--strict-mcp-config"}
			if hooks {
				want = append(want, "--settings", filepath.Join(h.repo, project.ProbeRun, "hooks.json"))
			}
			if got := h.herdr.argsFor(probeID); len(got) != 1 || !equal(got[0], want) {
				t.Errorf("hooks %v: probe started with %q, want once with %q", hooks, got, want)
			}
		})
	}
}
