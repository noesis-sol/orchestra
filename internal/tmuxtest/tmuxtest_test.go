package tmuxtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verdict is a test as Require leaves it: failed, skipped, or neither, and why.
type verdict struct {
	testing.TB
	failed, skipped string
}

func (v *verdict) Fatalf(format string, args ...any) { v.failed = fmt.Sprintf(format, args...) }
func (v *verdict) Skipf(format string, args ...any)  { v.skipped = fmt.Sprintf(format, args...) }

// Without tmux, a test that needs it skips, unless ORCHESTRA_REQUIRE_TMUX=1, when it fails; with
// tmux it runs either way.
func TestRequire(t *testing.T) {
	none, some := t.TempDir(), t.TempDir()
	// Only found on PATH, never run.
	if err := os.WriteFile(filepath.Join(some, "tmux"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path, require string
		want          string
	}{
		{none, "", "skipped"},
		{none, "0", "skipped"},
		{none, "1", "failed"},
		{some, "", "runs"},
		{some, "1", "runs"},
	} {
		t.Setenv("PATH", c.path)
		t.Setenv(RequireVar, c.require)
		v := &verdict{TB: t}
		Require(v)
		got := "runs"
		switch {
		case v.failed != "" && v.skipped == "":
			got = "failed"
			if !strings.Contains(v.failed, RequireVar+"=1 requires") {
				t.Errorf("failed with %q", v.failed)
			}
		case v.skipped != "" && v.failed == "":
			got = "skipped"
		case v.skipped != "" || v.failed != "":
			got = "failed and skipped"
		}
		if got != c.want {
			t.Errorf("tmux on PATH %v, %s=%q: the test %s, want it %s", c.path == some, RequireVar, c.require, got,
				c.want)
		}
	}
}
