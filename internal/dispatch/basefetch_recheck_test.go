package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/git"
)

// The start's fast-forward looks at the main checkout again under the lock, right before it: git moves
// whichever branch is checked out, and the fetch before it may take a minute.

// changedDuringFetch is git, with change run once Base's upstream is fetched, before Base is compared
// with it: what the maintainer does to the main checkout while the run fetches.
type changedDuringFetch struct {
	git.Git
	change func()
}

func (g changedDuringFetch) AheadBehind(ctx context.Context, repo, a, b string) (ahead, behind int, err error) {
	g.change()
	return g.Git.AheadBehind(ctx, repo, a, b)
}

// behindUpstream gives h's main a remote that has one commit more, and a branch, batch, at main, and
// returns where main and the upstream are.
func behindUpstream(h *harness) (before, pushed string) {
	h.t.Helper()
	other := withRemote(h)
	commitTo(h, other, "b.md")
	h.git(other, "push", "-q", "origin", "main")
	h.git(h.repo, "branch", "batch")
	return head(h, "main"), strings.TrimSpace(h.git(other, "rev-parse", "HEAD"))
}

func TestRunFastForwardsNothingWhenTheCheckoutChangesDuringTheFetch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(h *harness)
		want   string // in the run's final line
	}{
		{"switched", func(h *harness) { h.git(h.repo, "switch", "-q", "batch") }, "no longer on main"},
		{"dirtied", func(h *harness) { writeFile(h.t, h.repo, "a.md", "by hand\n") }, "uncommitted changes in "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			before, _ := behindUpstream(h)
			o := h.loop()
			o.upstream = changedDuringFetch{change: func() { tc.change(h) }}

			if code := o.Run(context.Background()); code != ExitDirty {
				t.Fatalf("exit %d, want %d (DIRTY_TREE); final %q\n%s", code, ExitDirty, o.Final(), h.sink.text())
			}
			if !strings.Contains(o.Final(), tc.want) {
				t.Errorf("final %q, want it to say %q", o.Final(), tc.want)
			}
			for _, br := range []string{"main", "batch"} {
				if got := head(h, br); got != before {
					t.Errorf("%s moved to %s from %s\n%s", br, got, before, h.mainLog())
				}
			}
			if o.startHead != before {
				t.Errorf("the run's start is %s, want %s: nothing was fast-forwarded", o.startHead, before)
			}
			if w := baseWarnings(h); len(w) > 0 || strings.Contains(h.logged(), "BASE:") {
				t.Errorf("nothing should be said of the base; got %v\n%s", w, h.logged())
			}
		})
	}
}

func TestFetchBaseFastForwardsNothingWhenTheCheckoutChangesDuringTheFetch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	before, _ := behindUpstream(h)
	g := changedDuringFetch{change: func() { h.git(h.repo, "switch", "-q", "batch") }}

	f, err := FetchBase(t.Context(), h.cfg, g)
	if err == nil || !strings.Contains(err.Error(), filepath.Base(h.repo)+" isn't on main") {
		t.Errorf("FetchBase: %+v, error %v; want it to say the checkout isn't on main", f, err)
	}
	if f != (BaseFetched{}) {
		t.Errorf("FetchBase: %+v, want nothing fetched", f)
	}
	for _, br := range []string{"main", "batch"} {
		if got := head(h, br); got != before {
			t.Errorf("%s moved to %s from %s", br, got, before)
		}
	}
}
