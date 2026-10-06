package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worktree whose .orchestra/run is a symlink, or a file orchestra writes there one leading out of
// the worktree, has its ticket set aside without a worker: nothing is written or removed where the
// link points, and the other tickets still run.
func TestRunFilesOutsideTheWorktreeSetTheTicketAside(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		link string // in the worktree, to the folder outside, or to the file "untouched" in it
		mcp  bool
	}{
		{"run folder", ".orchestra/run", true},
		{"run folder, no MCP servers", ".orchestra/run", false},
		{"mcp.json", ".orchestra/run/mcp.json", true},
		{"prompt.md", ".orchestra/run/prompt.md", false},
		{"rules.md", ".orchestra/run/rules.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.reporter = fakeReporter{}
			if tc.mcp {
				h.cfg.MCP = chosenServers()
			}
			h.beads.add("A", "first", 1)
			h.beads.add("B", "second", 2)
			h.worker("B", finishes("b.txt"))

			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "untouched"), []byte("kept"), 0o644); err != nil {
				t.Fatal(err)
			}
			wt := h.worktree("A") // a returning ticket's worktree, as its earlier worker left it
			h.git(h.repo, "worktree", "add", "-q", "-b", "wt/A", wt, "main")
			target := outside
			if filepath.Base(tc.link) != "run" {
				target = filepath.Join(outside, "untouched")
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, tc.link)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(wt, tc.link)); err != nil {
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
			why := "its worktree's " + filepath.FromSlash(tc.link) + " points outside the worktree"
			if filepath.Base(tc.link) == "run" {
				why = "its worktree's " + filepath.FromSlash(tc.link) + " is a symlink"
			}
			if notes := h.beads.notesOf("A"); !strings.Contains(notes, why) || !strings.Contains(notes, "bd undefer A") {
				t.Errorf("A's notes don't say %q and how to bring it back:\n%s", why, notes)
			}
			if out := h.sink.text(); !strings.Contains(out, "RUN_FILES_OUTSIDE: "+why) {
				t.Errorf("no RUN_FILES_OUTSIDE line saying %q:\n%s", why, out)
			}
			if !strings.Contains(h.logged(), filepath.FromSlash(tc.link)) {
				t.Errorf("the log doesn't name %s:\n%s", tc.link, h.logged())
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "untouched" || read(t, filepath.Join(outside, "untouched")) != "kept" {
				t.Errorf("the folder outside was changed: %v", entries)
			}
			if fi, err := os.Lstat(filepath.Join(wt, tc.link)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the link %s was removed or replaced (%v)", tc.link, err)
			}
			if st := h.statusOf("B"); st != "closed" {
				t.Errorf("B is %s, want closed: the run goes on", st)
			}
		})
	}
}

// The probe's file in the main checkout is reached through an os.Root too: with the main
// checkout's .orchestra/run a symlink, here out of it, the probe fails without touching what it
// points to.
func TestProbeKeepsInsideTheMainCheckout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(h.repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.repo, ".orchestra", "run")); err != nil {
		t.Fatal(err)
	}
	o := h.loop()
	_, err := o.probe(t.Context())
	if err == nil || !strings.Contains(err.Error(), filepath.Join(".orchestra", "run")+" in "+h.repo+" is a symlink") {
		t.Errorf("probe: %v, want a failure naming the link", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the probe wrote outside the main checkout: %v", entries)
	}
	if len(h.herdr.argsFor(probeID)) != 0 {
		t.Error("a probe worker was started")
	}
}
