package gittest

import (
	"flag"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The cap sets -parallel when the test binary isn't given it, and keeps it when it is.
func TestCapParallelKeepsAGivenParallel(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{{nil, "3"}, {[]string{"-test.parallel=7"}, "7"}, {[]string{"-test.parallel", "3"}, "3"}} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		parallel := fs.Int(parallelFlag, 10, "")
		if err := fs.Parse(c.args); err != nil {
			t.Fatal(err)
		}
		if err := capParallelIn(fs, 3); err != nil {
			t.Fatal(err)
		}
		if got := strconv.Itoa(*parallel); got != c.want {
			t.Errorf("args %q: -parallel %s, want %s", c.args, got, c.want)
		}
	}
}

// Flags without -parallel are left as they are.
func TestCapParallelWithoutTheFlag(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if err := capParallelIn(fs, 3); err != nil {
		t.Errorf("flags without -parallel: %v", err)
	}
}

// On macOS, Main runs half as many tests at once as go test would, and at least two, unless the test
// binary is given -parallel.
func TestMainCapsParallelOnMacOS(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("the cap is macOS's")
	}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-"+parallelFlag) || strings.HasPrefix(arg, "--"+parallelFlag) {
			t.Skip("the test binary was given -parallel, which the cap keeps")
		}
	}
	want := strconv.Itoa(max(2, runtime.GOMAXPROCS(0)/2))
	if got := flag.Lookup(parallelFlag).Value.String(); got != want {
		t.Errorf("-parallel is %s, want %s", got, want)
	}
}
