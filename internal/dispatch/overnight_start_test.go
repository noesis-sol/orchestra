package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// An overnight run (--overnight) says so on its START line, after its scope, and in its start record;
// another run says nothing of it.
func TestTheStartLineSaysTheRunIsOvernight(t *testing.T) {
	t.Parallel()
	for _, overnight := range []bool{true, false} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.Overnight = overnight
			h.run() // nothing ready: the run starts, and ends there
			logged := h.logged()
			if !strings.Contains(logged, "START orchestra ") {
				t.Fatalf("no START line:\n%s", logged)
			}
			if said := strings.Contains(logged, " on main · overnight ("); said != overnight {
				t.Errorf("overnight %v: the START line says overnight: %v\n%s", overnight, said, logged)
			}
		})
	}
}

func TestTheStartRecordSaysTheRunIsOvernight(t *testing.T) {
	t.Parallel()
	for _, overnight := range []bool{true, false} {
		repo := t.TempDir()
		l, _ := openLog(t)
		l.Begin(repo, RunStart{Started: time.Now(), Overnight: overnight, Concurrency: 2})
		l.End(0)
		recs := records(t, repo)
		if overnight {
			has(t, recs[0], map[string]any{"kind": "start", "overnight": true})
		} else {
			has(t, recs[0], map[string]any{"kind": "start"}, "overnight")
		}
	}
}
