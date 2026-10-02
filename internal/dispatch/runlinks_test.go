package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worktree whose .orchestra or .orchestra/run is a symlink that stays inside it has its ticket
// set aside without a worker too: git's ignore rules for the run files don't follow a link, so
// mcp.json, with the MCP servers' secrets, would be an untracked file where it points, for the
// worker to commit. Nothing is written there, the note names the link, and the other tickets run.
func TestRunFolderLinksInsideTheWorktreeSetTheTicketAside(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		link, target string // the link in the worktree, and where it points, relative to its folder
	}{
		{".orchestra", "docs"},
		{".orchestra/run", "../docs"},
	} {
		t.Run(tc.link, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.reporter = fakeReporter{}
			h.cfg.MCP = chosenServers()
			if err := os.MkdirAll(filepath.Join(h.repo, "docs"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.repo, "docs", "guide.md"), []byte("guide\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			h.git(h.repo, "add", "docs")
			h.git(h.repo, "commit", "-q", "-m", "docs")
			h.beads.add("A", "first", 1)
			h.beads.add("B", "second", 2)
			h.worker("A", finishes("a.txt")) // only if it is started: then the test fails rather than hangs
			h.worker("B", finishes("b.txt"))

			wt := h.worktree("A") // a returning ticket's worktree, as its earlier worker left it
			h.git(h.repo, "worktree", "add", "-q", "-b", "wt/A", wt, "main")
			link := filepath.Join(wt, filepath.FromSlash(tc.link))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.FromSlash(tc.target), link); err != nil {
				t.Fatal(err)
			}

			o, code := h.run()
			if code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if args := h.herdr.argsFor("A"); len(args) != 0 {
				t.Errorf("a worker was started for A: %q", args)
			}
			if st := h.statusOf("A"); st != "deferred" {
				t.Errorf("A is %s, want deferred", st)
			}
			resolved, err := filepath.EvalSymlinks(wt) // the loop names the worktree by its real path
			if err != nil {
				t.Fatal(err)
			}
			named := filepath.Join(resolved, filepath.FromSlash(tc.link))
			why := "its worktree's " + filepath.FromSlash(tc.link) + " is a symlink"
			notes := h.beads.notesOf("A")
			if !strings.Contains(notes, why) || !strings.Contains(notes, "rm "+named) || !strings.Contains(notes, "bd undefer A") {
				t.Errorf("A's notes don't say %q, how to remove the link and how to bring it back:\n%s", why, notes)
			}
			if out := h.sink.text(); !strings.Contains(out, "RUN_FILES_OUTSIDE: "+why) || !strings.Contains(out, named) {
				t.Errorf("no RUN_FILES_OUTSIDE line saying %q and naming %s:\n%s", why, named, out)
			}
			if out := h.git(wt, "status", "--porcelain", "--untracked-files=all", "--", "docs"); out != "" {
				t.Errorf("run files were written where the link points:\n%s", out)
			}
			if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the link %s was removed or replaced (%v)", tc.link, err)
			}
			if st := h.statusOf("B"); st != "closed" {
				t.Errorf("B is %s, want closed: the run goes on", st)
			}
		})
	}
}
