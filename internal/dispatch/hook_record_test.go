package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// recordReporter is hookReporter with the tool in each report and what the hooks noted besides.
type recordReporter struct {
	hookReporter
	records map[string]HookRecord
}

func (r *recordReporter) use(wt string, u ToolUse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]ToolUse{}
	}
	u.At = time.Now()
	r.last[wt] = u
}

func (r *recordReporter) note(wt string, f func(*HookRecord)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.records == nil {
		r.records = map[string]HookRecord{}
	}
	rec := r.records[wt]
	f(&rec)
	r.records[wt] = rec
}

func (r *recordReporter) HookRecord(wt string) HookRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.records[wt]
}

// A worker waiting on a permission prompt, which Herdr shows as idle, waits for the maintainer as a
// blocked one does: the dashboard says so, the log says once what it asks, and the run stops after
// the blocked limit rather than the idle grace.
func TestPermissionPromptShownAsIdleStopsAsBlocked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		var shown Status
		var asked time.Time
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			hooks.use(w.wt, ToolUse{Event: "PreToolUse", Tool: "Bash", Command: "rm -rf build"})
			hooks.use(w.wt, ToolUse{Event: EventPermission, Tool: "Bash", Command: "rm -rf build"})
			hooks.note(w.wt, func(r *HookRecord) { r.Permissions = append(r.Permissions, "Bash") })
			asked = time.Now()
			w.shows(StateIdle)
			time.Sleep(3 * statusPoll)
			shown = h.sink.lastStatus("A")
			return StateIdle
		})
		o, code := h.run()
		want := "BLOCKED >4min: tab tab1 (A) needs attention: its worker waits on a permission prompt"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if took := time.Since(asked); took <= blockedLimit || took > blockedLimit+2*statusPoll {
			t.Errorf("stopped %s after the prompt, want just past %s", took, blockedLimit)
		}
		if !shown.Permission || shown.Agent != StateIdle {
			t.Errorf("the dashboard should show the worker waiting on its prompt: %+v", shown)
		}
		got := h.sink.of(EvWarn)
		n := 0
		for _, l := range got {
			if strings.Contains(l, "PERMISSION_PROMPT: A's worker waits on a permission prompt for Bash (rm -rf build) "+
				"in tab tab1; allow or deny it there") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("want the prompt logged once: %q", got)
		}
	})
}

// A prompt the maintainer allowed leaves the hooks' record at the PermissionRequest while the tool
// runs: the worker, which Herdr shows working, is using the tool, not waiting.
func TestAllowedPermissionPromptShowsTheTool(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		var shown Status
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			hooks.use(w.wt, ToolUse{Event: EventPermission, Tool: "Bash", Command: "go test ./..."})
			time.Sleep(3 * statusPoll)
			shown = h.sink.lastStatus("A")
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return StateIdle
		})
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if shown.Permission || shown.Doing != "testing" {
			t.Errorf("a worker running the tool it was allowed should show what it runs: %+v", shown)
		}
		if got := h.sink.of(EvWarn); len(got) != 0 {
			t.Errorf("nothing to warn about: %q", got)
		}
	})
}

// A worker running a subagent shows it between the subagent's tools, and each compaction of its
// context is logged once, as it happens.
func TestSubagentsAndCompactionsAreShown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		var between, after Status
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents = 1 })
			hooks.report(w.wt, "PostToolUse") // between the subagent's tools
			time.Sleep(3 * statusPoll)
			between = h.sink.lastStatus("A")
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents, r.Compactions = 0, []string{"auto"} })
			time.Sleep(3 * statusPoll)
			after = h.sink.lastStatus("A")
			hooks.note(w.wt, func(r *HookRecord) { r.Compactions = append(r.Compactions, "manual") })
			time.Sleep(3 * statusPoll)
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return StateIdle
		})
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if between.Doing != DoingSubagent || after.Doing != "" {
			t.Errorf("want subagent while one runs, then nothing more precise: %q, %q", between.Doing, after.Doing)
		}
		logged := h.logged()
		for _, trigger := range []string{"auto", "manual"} {
			if line := "A's worker's context was compacted (" + trigger + ")"; strings.Count(logged, line) != 1 {
				t.Errorf("want %q logged once:\n%s", line, logged)
			}
		}
	})
}

func TestHookRecordEvidence(t *testing.T) {
	if got := (HookRecord{Subagents: 2}).Evidence(); got != "" {
		t.Errorf("nothing for triage in running subagents: %q", got)
	}
	got := HookRecord{Permissions: []string{"Bash", "WebFetch"}, Compactions: []string{"auto"}}.Evidence()
	want := "It waited on a permission prompt twice, Claude Code asking to allow: Bash, WebFetch.\n" +
		"Its context was compacted once (auto): the summary that replaced its earlier messages may have lost " +
		"some of what it was told or found."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Triage's evidence names the permission prompts and compactions the worker's hooks noted.
func TestTriageEvidenceHasWhatTheHooksNoted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		hooks.note(h.worktree("A"), func(r *HookRecord) { r.Permissions, r.Compactions = []string{"Bash"}, []string{"auto"} })
		d := h.loop().gatherDeferral(context.Background(), "A", "the worker deferred it", h.worktree("A"))
		if !strings.Contains(d.Hooks, "permission prompt once") || !strings.Contains(d.Hooks, "compacted once (auto)") {
			t.Errorf("hooks evidence %q", d.Hooks)
		}
	})
}
