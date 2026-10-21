package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// An overnight run's pre-flight runs the merge check once on Base's head, in a worktree of its own
// made and removed for it, the setup first.

// baseConfig is a run's configuration for CheckBase, its repository and worktrees in t's folders.
func baseConfig(t *testing.T, check string) Config {
	t.Helper()
	return Config{Repo: t.TempDir(), Base: "main", WTRoot: t.TempDir(), Check: check}
}

// leftIn fails t where CheckBase left anything in the worktrees' folder.
func leftIn(t *testing.T, c Config) {
	t.Helper()
	if left, err := os.ReadDir(c.WTRoot); err != nil || len(left) > 0 {
		t.Errorf("the worktree was left: %v (%v)", left, err)
	}
}

func TestCheckBasePassesOnBasesHead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := baseConfig(t, "test -f installed && pwd > "+filepath.Join(dir, "check"))
	c.Setup = "touch installed"
	g := newFakeGit("main")
	b, err := CheckBase(t.Context(), c, g)
	if err != nil || b.Failed != "" || b.Output != "" {
		t.Fatalf("CheckBase: %+v, %v", b, err)
	}
	if want, _ := g.Head(t.Context(), c.Repo, "main"); b.At != want {
		t.Errorf("it ran at %q, want main's head %q", b.At, want)
	}
	if ran := read(t, filepath.Join(dir, "check")); !strings.HasPrefix(filepath.Base(strings.TrimSpace(ran)), "check-base-") {
		t.Errorf("the check ran in %s, not a worktree of its own", ran)
	}
	leftIn(t, c)
}

// A check that fails says how, what its output says of the failure, and where the whole of it is.
func TestCheckBaseFailing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, setup, check, failed, said string
	}{
		{name: "check", check: "echo '--- FAIL: TestMerge'; false",
			failed: "'echo '--- FAIL: TestMerge'; false' fails", said: "--- FAIL: TestMerge"},
		{name: "setup", setup: "echo 'npm ERR! offline'; false", check: "true",
			failed: "the setup 'echo 'npm ERR! offline'; false', run before it, fails", said: "npm ERR! offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := baseConfig(t, tc.check)
			c.Setup = tc.setup
			b, err := CheckBase(t.Context(), c, newFakeGit("main"))
			if err != nil {
				t.Fatal(err)
			}
			if b.Failed != tc.failed {
				t.Errorf("Failed is %q, want %q", b.Failed, tc.failed)
			}
			if !slices.Contains(b.Said, tc.said) {
				t.Errorf("Said is %q, want it to have %q", b.Said, tc.said)
			}
			if want := filepath.Join(c.Repo, ".orchestra", "run", BaseCheckLogName); b.Output != want {
				t.Errorf("Output is %q, want %q", b.Output, want)
			} else if out := read(t, b.Output); !strings.Contains(out, tc.said) {
				t.Errorf("the output kept lacks %q:\n%s", tc.said, out)
			}
			leftIn(t, c)
		})
	}
}

// A check that runs past the merge check's time limit is stopped, and fails.
func TestCheckBaseTimesOut(t *testing.T) {
	t.Parallel()
	c := baseConfig(t, "sleep 30")
	c.CheckTimeout = 100 * time.Millisecond
	b, err := CheckBase(t.Context(), c, newFakeGit("main"))
	if err != nil || !strings.HasSuffix(b.Failed, "did not finish within 100ms") {
		t.Errorf("CheckBase: %+v, %v", b, err)
	}
	leftIn(t, c)
}

// Without Base's head, or once ctx is cancelled, it doesn't say the check passed.
func TestCheckBaseThatCannotRun(t *testing.T) {
	t.Parallel()
	c := baseConfig(t, "true")
	c.Base = "trunk"
	if _, err := CheckBase(t.Context(), c, newFakeGit("main")); err == nil || !strings.Contains(err.Error(), "trunk") {
		t.Errorf("CheckBase on a branch with no head: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.Base = "main"
	if _, err := CheckBase(ctx, c, newFakeGit("main")); err == nil {
		t.Error("CheckBase passed with ctx cancelled")
	}
	leftIn(t, c)
}
