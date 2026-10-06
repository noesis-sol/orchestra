package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
)

// The run report's evidence says which tickets' workers waited on permission prompts or had their
// context compacted: a merged ticket's as read before its worktree was removed, with its notes, and a
// ticket set aside, whose worktree is left, as read when the report is written.
func TestReviewInputHasWhatTheHooksNoted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			hooks.note(w.wt, func(r *HookRecord) {
				r.Permissions, r.Compactions = []string{"Bash", "WebFetch"}, []string{"auto"}
			})
			w.commit("a.txt")
			w.close()
			return StateIdle
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			w.claim()
			w.deferIt()
			return StateIdle
		})
		h.worker("C", finishes("c.txt")) // noted nothing
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		hooks.note(h.worktree("A"), func(r *HookRecord) { *r = HookRecord{} }) // gone with A's worktree
		hooks.note(h.worktree("B"), func(r *HookRecord) { r.Compactions = []string{"manual"} })

		in := o.reviewInput(context.Background(), code, o.Final())
		section := "Permission prompts and compactions of this run's workers, as their hooks noted them"
		if !strings.Contains(in, section) {
			t.Fatalf("no section %q:\n%s", section, in)
		}
		for _, want := range []string{
			"A: its workers waited on 2 permission prompts (Bash, WebFetch); had their context compacted once (auto).",
			"B: its workers had their context compacted once (manual).",
		} {
			if !strings.Contains(in, want) {
				t.Errorf("the reviewer's evidence lacks %q:\n%s", want, in)
			}
		}
		if strings.Contains(in, "C: its workers") {
			t.Errorf("C's worker noted nothing:\n%s", in)
		}
	})
}

// A new worker on the ticket in the same worktree replaces the notes of the one before it in this
// run, which are kept; what a worker before this run noted is not this run's.
func TestHookTallyKeepsEarlierWorkersNotes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		o := h.loop()
		wt := h.worktree("A")
		set := func(rec HookRecord) { hooks.note(wt, func(r *HookRecord) { *r = rec }) }

		set(HookRecord{Permissions: []string{"Bash"}}) // the last run's worker
		o.newWorkerHooks("A", wt)
		set(HookRecord{Permissions: []string{"Edit"}, Compactions: []string{"auto"}})
		o.newWorkerHooks("A", wt) // resumed: its hooks start afresh
		set(HookRecord{Permissions: []string{"Write"}})

		want := "A: its workers waited on 2 permission prompts (Edit, Write); had their context compacted once (auto)."
		if got := o.hookEvidence(); got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})
}

func TestHookEvidenceWithoutHooks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		if got := h.loop().hookEvidence(); !strings.HasPrefix(got, "Not recorded") {
			t.Errorf("no reporter: %q", got)
		}
		h.reporter = &recordReporter{}
		if got := h.loop().hookEvidence(); !strings.HasPrefix(got, "None") {
			t.Errorf("nothing noted: %q", got)
		}
		h.cfg.AgentKind = "codex"
		if got := h.loop().hookEvidence(); !strings.HasPrefix(got, "Not recorded") {
			t.Errorf("workers that aren't Claude's: %q", got)
		}
	})
}
