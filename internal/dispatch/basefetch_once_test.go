package dispatch

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/git"
)

// After the overnight pre-flight's fetch (FetchBase), the run doesn't fetch again.

func TestRunAfterThePreflightsFetchFetchesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := withRemote(h)
	commitTo(h, other, "b.md")
	h.git(other, "push", "-q", "origin", "main")
	before, tracked := head(h, "main"), head(h, "origin/main")
	h.cfg.BaseFetched = true

	runFetching(h)
	if got := head(h, "main"); got != before {
		t.Errorf("main moved to %s from %s", got, before)
	}
	if got := head(h, "origin/main"); got != tracked {
		t.Errorf("origin/main moved to %s from %s: the run fetched again", got, tracked)
	}
}

func TestFetchBaseFastForwardsBeforeTheLoop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := withRemote(h)
	commitTo(h, other, "b.md")
	commitTo(h, other, "c.md")
	h.git(other, "push", "-q", "origin", "main")
	pushed := strings.TrimSpace(h.git(other, "rev-parse", "HEAD"))

	f, err := FetchBase(t.Context(), h.cfg, git.Git{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Behind != 2 || f.Upstream != "origin/main" || f.Warning != "" ||
		f.Text != "fast-forwarded main to origin/main (2 commits)" {
		t.Errorf("FetchBase: %+v", f)
	}
	if got := head(h, "main"); got != pushed {
		t.Errorf("main is at %s, want the upstream's %s", got, pushed)
	}
}
