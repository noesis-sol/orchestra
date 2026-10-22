package dispatch

import (
	"context"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra-xj6z.9: a ticket stopped at its merge (DIRTY_TREE, GIT_FAILED, MERGE_FAILED) is left
// unmerged with no warning that sets it aside: the hold or stop over it went out, which ended nothing,
// so no record gave its phases or its workers' tokens, and the end record's totals left them out. The
// first record with blocked over it, the hold while another ticket runs or else the run's stop, ends its
// part in the run.
func TestATicketStoppedAtItsMergeGivesItsPhasesAndTokens(t *testing.T) {
	t.Parallel()
	for _, beside := range []bool{false, true} {
		name, want := "alone", "stop"
		if beside {
			name, want = "beside another ticket", "hold"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				var dirty atomic.Bool
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					w.claim()
					time.Sleep(time.Minute) // B starts meanwhile
					dirty.Store(true)
					return finishes("a.txt")(w)
				})
				if beside {
					h.cfg.Concurrency = 2
					h.beads.add("B", "second", 2)
					h.worker("B", busyFor(time.Hour, "b.txt"))
				}
				a := session("a")
				used := Tokens{"claude-opus-5-5": {Input: 10, Output: 200, CacheWrite: 3000, CacheRead: 40000}}
				h.reporter = &tokensReporter{sessions: map[string][]Session{h.worktree("A"): {a}},
					used: map[string]Tokens{a.Transcript: used}}
				o := h.loop()
				o.checkout = dirtyCheckout{fakeGit: h.mem, dirty: &dirty}
				started := time.Now()
				o.log.Begin(h.repo, RunStart{Started: started, Repo: h.repo, Branch: "main", Concurrency: h.cfg.Concurrency})
				code := o.Run(context.Background())
				o.log.End(code)
				if err := o.log.Close(); err != nil {
					t.Fatal(err)
				}
				if code != ExitDirty {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				run := timeString(t, started)
				recs := slices.DeleteFunc(records(t, h.repo), func(r map[string]any) bool { return r["run"] != run })
				var ends []map[string]any
				for _, r := range recs {
					if r["ticket"] == "A" && r["phases"] != nil {
						ends = append(ends, r)
					}
				}
				if len(ends) != 1 {
					t.Fatalf("%d records give A's phases, want one:\n%v", len(ends), recs)
				}
				r := ends[0]
				has(t, r, map[string]any{"kind": want, "blocked": "main checkout has uncommitted changes"})
				checkPhases(t, r, map[string]float64{"working": 60, "merging": 0}, 0, 0)
				wantTokens := map[string]any{"claude-opus-5-5": model(10, 200, 3000, 40000)}
				if got := tokensOn(t, r); !reflect.DeepEqual(got, wantTokens) {
					t.Errorf("A's tokens %v, want %v", got, wantTokens)
				}
				end := recs[len(recs)-1]
				has(t, end, map[string]any{"kind": "end"})
				if got := tokensOn(t, end); !reflect.DeepEqual(got, wantTokens) {
					t.Errorf("the end's tokens %v, want A's, %v", got, wantTokens)
				}
			})
		})
	}
}
