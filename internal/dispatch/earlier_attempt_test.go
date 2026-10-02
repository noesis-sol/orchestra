package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worker started on a branch an earlier attempt left work on is told what it left: the commits
// main doesn't have, uncommitted changes, or both, and how to see them. A fresh worktree's worker
// is told nothing.
func TestWorkerIsToldWhatAnEarlierAttemptLeft(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		commits int  // commits wt/A has that main doesn't, from the earlier attempt
		dirty   bool // the earlier attempt left wip.txt uncommitted
		removed bool // its worktree's folder is gone, with only the branch left
		want    string
	}{
		{name: "fresh"},
		{name: "commits", commits: 2,
			want: "left work on wt/A: 2 commits that main doesn't have (git log main..HEAD). Read it"},
		{name: "uncommitted", dirty: true, want: "left work on wt/A: uncommitted changes (git status). Read it"},
		{name: "both", commits: 1, dirty: true,
			want: "left work on wt/A: 1 commit that main doesn't have (git log main..HEAD) and uncommitted changes"},
		{name: "branch only", commits: 1, removed: true,
			want: "left work on wt/A: 1 commit that main doesn't have (git log main..HEAD). Read it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			if c.commits > 0 || c.dirty {
				wt := h.worktree("A")
				h.git(h.repo, "worktree", "add", "-q", "-b", "wt/A", wt)
				for i := range c.commits {
					file := fmt.Sprintf("earlier%d.txt", i)
					if err := os.WriteFile(filepath.Join(wt, file), []byte("earlier\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					h.git(wt, "add", file)
					h.git(wt, "commit", "-q", "-m", "A: earlier attempt at "+file)
				}
				if c.dirty {
					if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("half done\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if c.removed {
					h.git(h.repo, "worktree", "remove", wt)
				}
			}
			h.beads.add("A", "first", 1)
			p := &prompted{got: map[string]string{}}
			h.worker("A", p.then(func(w *fakeWorker) AgentState {
				w.claim()
				if c.dirty {
					w.commit("wip.txt") // builds on it
				}
				w.commit("a.txt")
				w.close()
				return "idle"
			}))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			got := p.got["A"]
			switch {
			case !strings.HasPrefix(got, "Work on A."):
				t.Errorf("no prompt, or not the ticket's:\n%s", got)
			case c.want == "" && strings.Contains(got, "earlier attempt"):
				t.Errorf("a fresh worktree, yet the prompt speaks of an earlier attempt:\n%s", got)
			case c.want != "" && !strings.Contains(got, c.want):
				t.Errorf("the prompt doesn't say %q:\n%s", c.want, got)
			}
			if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
				t.Errorf("A was not merged:\n%s", log)
			}
		})
	}
}
