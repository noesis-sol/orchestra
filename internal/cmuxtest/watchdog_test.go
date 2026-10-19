package cmuxtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// leftVar names, in the environment of the test binary started by TestAKilledTestLeavesNoApp, the
// file where TestKilledChild writes its app's process ID.
const leftVar = "ORCHESTRA_CMUXTEST_LEFT"

// A test killed before its cleanups run, as one past its timeout is, leaves no cmux app behind.
func TestAKilledTestLeavesNoApp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: starts the cmux app")
	}
	Require(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(t.TempDir(), "left")
	out, err := command.OutputWithEnv(context.Background(), turnLimit+startLimit, "", []string{leftVar + "=" + left},
		self, "-test.run=^TestKilledChild$", "-test.v")
	b, readErr := os.ReadFile(left)
	switch {
	case readErr != nil && err == nil:
		t.Skipf("the test to be killed skipped:\n%s", out) // or failed, with ORCHESTRA_REQUIRE_CMUX=1
	case readErr != nil:
		t.Fatalf("the test to be killed wrote nothing (%v): %v\n%s", readErr, err, out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("the test killed wrote %q", b)
	}
	for deadline := time.Now().Add(quitLimit); !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH); time.Sleep(100 *
		time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL) // this test's own
			t.Fatalf("%s after its test was killed, its cmux (process %d) runs", quitLimit, pid)
		}
	}
}

// TestKilledChild runs, as the test binary started by TestAKilledTestLeavesNoApp, a test with a
// cmux app, which writes the app's process ID and is killed.
func TestKilledChild(t *testing.T) {
	left := os.Getenv(leftVar)
	if left == "" {
		t.Skip("run by TestAKilledTestLeavesNoApp")
	}
	a := Start(t)
	if err := os.WriteFile(left, []byte(strconv.Itoa(a.cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL) // never returns
}
