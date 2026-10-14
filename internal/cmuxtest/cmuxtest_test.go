package cmuxtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// A test that needs cmux runs only when asked for, ORCHESTRA_CMUX_TEST=1 or ORCHESTRA_REQUIRE_CMUX=1,
// and cmux is there; without cmux, it skips, unless ORCHESTRA_REQUIRE_CMUX=1, when it fails. Off
// macOS, where cmux doesn't run, it is the same as without cmux.
func TestRequire(t *testing.T) {
	none, some := filepath.Join(t.TempDir(), "cmux.app"), filepath.Join(t.TempDir(), "cmux.app")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(some, "Contents", "MacOS", "cmux")), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only looked at, never run.
	if err := os.WriteFile(filepath.Join(some, "Contents", "MacOS", "cmux"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		app, optIn, require string
		want                string
	}{
		{none, "", "", "skipped"},
		{none, "1", "", "skipped"},
		{none, "", "0", "skipped"},
		{none, "", "1", "failed"},
		{some, "", "", "skipped"},
		{some, "0", "", "skipped"},
		{some, "1", "", "runs"},
		{some, "", "1", "runs"},
	} {
		t.Setenv(AppVar, c.app)
		t.Setenv(OptInVar, c.optIn)
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
		want := c.want
		if runtime.GOOS != "darwin" && want == "runs" {
			want = map[string]string{"1": "failed"}[c.require]
			if want == "" {
				want = "skipped"
			}
		}
		if got != want {
			t.Errorf("cmux installed %v, %s=%q, %s=%q: the test %s, want it %s", c.app == some, OptInVar, c.optIn,
				RequireVar, c.require, got, want)
		}
	}
}
