package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/command"
)

// The commands orchestra gives the user to paste name each path as one word, quoted when it holds
// a space, so that a repository or WT_ROOT with one in its path gets the command it means.

// spacedRoot is a WT_ROOT whose path holds a space.
func spacedRoot(t *testing.T) string {
	return filepath.Join(t.TempDir(), "work trees")
}

// pasted runs line as a shell would, with git, cd and rm only printing the words they are given,
// one call a line, each word in <>.
func pasted(t *testing.T, line string) string {
	t.Helper()
	stubs := `say() { printf '%s' "$1"; shift; printf ' <%s>' "$@"; echo; }; ` +
		`git() { say git "$@"; }; cd() { say cd "$@"; }; rm() { say rm "$@"; }; `
	out, err := command.Output(context.Background(), command.ReadLimit, "", "sh", "-c", stubs+line)
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return strings.TrimSpace(out)
}

// between is the part of s after the first from and before the next to that follows it.
func between(t *testing.T, s, from, to string) string {
	t.Helper()
	_, after, ok := strings.Cut(s, from)
	if !ok {
		t.Fatalf("no %q in %q", from, s)
	}
	cut, _, ok := strings.Cut(after, to)
	if !ok {
		t.Fatalf("no %q after %q in %q", to, from, s)
	}
	return cut
}

func TestDeferredFreeCommandsQuoteAWorktreeWithASpace(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	deferredTickets(b)
	g := newFakeGit(c.Base)
	wt := filepath.Join(spacedRoot(t), "D")
	if _, err := g.NewWorktree(context.Background(), c.Repo, wt, "wt/D", c.Base); err != nil {
		t.Fatal(err)
	}
	n := checkNothingOn(t, c, b, g)
	if n == nil || len(n.DeferredFree) != 1 || n.DeferredFree[0].Worktree != wt {
		t.Fatalf("got %+v, want D's worktree %s free to remove", n, wt)
	}
	d := n.DeferredFree[0]
	if got, want := pasted(t, d.Cleanup()), "git <worktree> <remove> <"+wt+">\ngit <branch> <-d> <wt/D>"; got != want {
		t.Errorf("%s runs\n%s\nwant\n%s", d.Cleanup(), got, want)
	}
	if line := d.Line(c.Base); !strings.HasSuffix(line, "safe to remove: "+d.Cleanup()) {
		t.Errorf("line %q doesn't end with the cleanup %q", line, d.Cleanup())
	}

	d = DeferredFree{ID: "D", Blockers: []string{"A"}, Uncommitted: wt}
	if got, want := pasted(t, d.Changes()), "git <-C> <"+wt+"> <status>"; got != want {
		t.Errorf("%s runs %s, want %s", d.Changes(), got, want)
	}
}

// failingPrune is git in memory whose git worktree prune fails.
type failingPrune struct{ *fakeGit }

func (failingPrune) Prune(ctx context.Context, repo string) (string, error) {
	return "fatal: cannot lock the worktrees", errors.New("git worktree prune: exit status 128")
}

func TestFullCheckCleanupFailedQuotesAWorktreeWithASpace(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.WTRoot = spacedRoot(t)
		h.cfg.CheckFull = "true"
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o := h.loop()
		o.worktrees = failingPrune{h.mem}
		if code := o.Run(t.Context()); code != ExitOK || !o.FullCheckDue(code) {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		o.FullCheck(t.Context())
		var warned string
		for _, w := range h.sink.of(EvWarn) {
			if strings.Contains(w, "CLEANUP_FAILED: the full check's worktree") {
				warned = w
			}
		}
		if warned == "" {
			t.Fatalf("no CLEANUP_FAILED for the full check's worktree:\n%s", h.sink.text())
		}
		wt := between(t, warned, "the full check's worktree ", " is left")
		if filepath.Dir(wt) != h.cfg.WTRoot {
			t.Fatalf("the worktree %q is not in %s: %q", wt, h.cfg.WTRoot, warned)
		}
		cmd := between(t, warned, "(", " removes it)")
		if got, want := pasted(t, cmd), "git <worktree> <remove> <--force> <"+wt+">"; got != want {
			t.Errorf("%s runs %s, want %s", cmd, got, want)
		}
	})
}

// A returning ticket whose branch conflicts is noted with how to rebase it by hand, in its worktree.
func TestRebaseByHandNoteQuotesAWorktreeWithASpace(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.WTRoot = spacedRoot(t)
		wt := h.worktree("A")
		if _, err := h.mem.NewWorktree(t.Context(), h.repo, wt, "wt/A", "main"); err != nil {
			t.Fatal(err)
		}
		if err := h.mem.commitIn(wt, "A: earlier attempt"); err != nil {
			t.Fatal(err)
		}
		h.mem.commit("main", "main moves on", "shared.txt")
		h.mem.conflict("wt/A", "shared.txt")
		h.beads.add("A", "first", 1)
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if st := h.statusOf("A"); st != "deferred" {
			t.Fatalf("A is %s, want deferred\n%s", st, h.sink.text())
		}
		cmd := between(t, h.beads.notesOf("A"), "Rebase it by hand (", ", resolve")
		if got, want := pasted(t, cmd), "cd <"+wt+">\ngit <rebase> <main>"; got != want {
			t.Errorf("%s runs\n%s\nwant\n%s", cmd, got, want)
		}
	})
}

// A worktree whose .orchestra is a symlink is noted with how to remove the link.
func TestRunFilesLinkNoteQuotesAWorktreeWithASpace(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.WTRoot = spacedRoot(t)
		wt := h.worktree("A")
		if _, err := h.mem.NewWorktree(t.Context(), h.repo, wt, "wt/A", "main"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(wt, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("docs", filepath.Join(wt, ".orchestra")); err != nil {
			t.Fatal(err)
		}
		h.beads.add("A", "first", 1)
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if st := h.statusOf("A"); st != "deferred" {
			t.Fatalf("A is %s, want deferred\n%s", st, h.sink.text())
		}
		cmd := between(t, h.beads.notesOf("A"), "remove the link (", "), then")
		if got, want := pasted(t, cmd), "rm <"+filepath.Join(wt, ".orchestra")+">"; got != want {
			t.Errorf("%s runs %s, want %s", cmd, got, want)
		}
	})
}
