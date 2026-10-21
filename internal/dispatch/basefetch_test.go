package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/git"
)

// withRemote gives h's repository a bare remote, origin, that main follows, and a clone of it elsewhere
// to push from, returned with its path.
func withRemote(h *harness) (other string) {
	h.t.Helper()
	bare := filepath.Join(h.t.TempDir(), "remote.git")
	h.git(h.repo, "init", "-q", "--bare", "-b", "main", bare)
	h.git(h.repo, "remote", "add", "origin", bare)
	h.git(h.repo, "push", "-q", "-u", "origin", "main")
	other = filepath.Join(h.t.TempDir(), "other")
	h.git(h.repo, "clone", "-q", bare, other)
	return other
}

// commitTo makes a commit in the checkout at dir, its message naming name: the run only counts commits.
func commitTo(h *harness, dir, name string) {
	h.t.Helper()
	h.git(dir, "commit", "-q", "--allow-empty", "-m", "Add "+name)
}

// runFetching runs h's loop with git asked about Base's upstream, and wants it to end as an empty
// backlog does.
func runFetching(h *harness) *Loop {
	h.t.Helper()
	o := h.loop()
	o.upstream = git.Git{}
	if code := o.Run(context.Background()); code != ExitOK {
		h.t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	return o
}

func head(h *harness, rev string) string { return strings.TrimSpace(h.git(h.repo, "rev-parse", rev)) }

// baseWarnings are the warnings the run gave about Base, by the word their lines start with.
func baseWarnings(h *harness) []string {
	var kinds []string
	for _, ev := range h.sink.eventsOf(EvWarn) {
		if strings.HasPrefix(ev.Detail, "BASE_") {
			kinds = append(kinds, ev.Detail)
		}
	}
	return kinds
}

func TestRunFastForwardsBaseBehindItsUpstream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := withRemote(h)
	commitTo(h, other, "b.md")
	commitTo(h, other, "c.md")
	h.git(other, "push", "-q", "origin", "main")
	pushed := strings.TrimSpace(h.git(other, "rev-parse", "HEAD"))

	o := runFetching(h)
	if got := head(h, "main"); got != pushed {
		t.Errorf("main is at %s, want the upstream's %s\n%s", got, pushed, h.mainLog())
	}
	if line := "BASE: fast-forwarded main to origin/main (2 commits)"; !strings.Contains(h.logged(), line) {
		t.Errorf("the log should say %q:\n%s", line, h.logged())
	}
	if w := baseWarnings(h); len(w) > 0 {
		t.Errorf("no warning wanted, got %v", w)
	}
	if o.startHead != pushed {
		t.Errorf("the run's start is %s, want %s, where it fast-forwarded main to", o.startHead, pushed)
	}
}

func TestRunLeavesDivergedBaseAsItIs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := withRemote(h)
	commitTo(h, other, "b.md")
	h.git(other, "push", "-q", "origin", "main")
	commitTo(h, h.repo, "c.md")
	commitTo(h, h.repo, "d.md")
	before := head(h, "main")

	runFetching(h)
	if got := head(h, "main"); got != before {
		t.Errorf("main moved to %s from %s\n%s", got, before, h.mainLog())
	}
	want := "main is 2 ahead of and 1 behind origin/main; the run builds on main as it is"
	if !h.sink.warnedWith("BASE_DIVERGED", "", want) || !strings.Contains(h.logged(), "BASE_DIVERGED: "+want) {
		t.Errorf("the run should warn BASE_DIVERGED: %s\n%s", want, h.logged())
	}
	if w := baseWarnings(h); len(w) != 1 || w[0] != "BASE_DIVERGED" {
		t.Errorf("the warning's detail should be BASE_DIVERGED, once; got %v", w)
	}
}

func TestRunStartsWhenTheUpstreamCannotBeFetched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	withRemote(h)
	h.git(h.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	before := head(h, "main")

	runFetching(h)
	if got := head(h, "main"); got != before {
		t.Errorf("main moved to %s from %s", got, before)
	}
	if !h.sink.warnedWith("BASE_UNFETCHED", "", "could not fetch origin/main") ||
		!h.sink.warnedWith("BASE_UNFETCHED", "", "the run builds on main as it is") {
		t.Errorf("the run should warn BASE_UNFETCHED, naming origin/main:\n%s", h.logged())
	}
	if w := baseWarnings(h); len(w) != 1 || w[0] != "BASE_UNFETCHED" {
		t.Errorf("the warning's detail should be BASE_UNFETCHED, once; got %v", w)
	}
}

func TestRunWithoutAnUpstreamFetchesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	before := head(h, "main")

	runFetching(h)
	if got := head(h, "main"); got != before {
		t.Errorf("main moved to %s from %s", got, before)
	}
	if w := baseWarnings(h); len(w) > 0 || strings.Contains(h.logged(), "BASE:") {
		t.Errorf("nothing should be said of the base with no upstream; got %v\n%s", w, h.logged())
	}
}

func TestNoFetchLeavesBaseAndItsUpstreamUnfetched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := withRemote(h)
	commitTo(h, other, "b.md")
	h.git(other, "push", "-q", "origin", "main")
	before, tracked := head(h, "main"), head(h, "origin/main")
	h.cfg.NoFetch = true

	runFetching(h)
	if got := head(h, "main"); got != before {
		t.Errorf("main moved to %s from %s", got, before)
	}
	if got := head(h, "origin/main"); got != tracked {
		t.Errorf("origin/main moved to %s from %s: --no-fetch fetches nothing", got, tracked)
	}
	if w := baseWarnings(h); len(w) > 0 || strings.Contains(h.logged(), "BASE:") {
		t.Errorf("nothing should be said of the base with --no-fetch; got %v\n%s", w, h.logged())
	}
}
