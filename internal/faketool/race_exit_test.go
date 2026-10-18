//go:build unix

package faketool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// childDirVar names, in the environment of the test binary started by TestAChildHasNoRaceExitSleep,
// the folder where TestRaceExitChild writes what GORACE its fake tool got.
const childDirVar = "ORCHESTRA_FAKETOOL_RACE_EXIT_DIR"

// The race runtime's options get atexit_sleep_ms=0, after the others, unless they set it already.
func TestQuietRaceExit(t *testing.T) {
	for gorace, want := range map[string]string{
		"":                                    "atexit_sleep_ms=0",
		"log_path=/tmp/race halt_on_error=1":  "log_path=/tmp/race halt_on_error=1 atexit_sleep_ms=0",
		"halt_on_error=1 atexit_sleep_ms=250": "halt_on_error=1 atexit_sleep_ms=250",
		"halt_on_error=1,atexit_sleep_ms=5":   "halt_on_error=1,atexit_sleep_ms=5",
		"halt_on_error=1:atexit_sleep_ms=5":   "halt_on_error=1:atexit_sleep_ms=5",
	} {
		if got := quietRaceExit(gorace); got != want {
			t.Errorf("quietRaceExit(%q) = %q, want %q", gorace, got, want)
		}
	}
}

// A process the tests start gets atexit_sleep_ms=0 with the other options GORACE had when they
// started, and keeps an atexit_sleep_ms the user set.
func TestAChildHasNoRaceExitSleep(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ gorace, want string }{
		{"atexit_sleep_ms=1 halt_on_error=1", "atexit_sleep_ms=1 halt_on_error=1"},
		{"log_path=/tmp/race halt_on_error=1", "log_path=/tmp/race halt_on_error=1 atexit_sleep_ms=0"},
	} {
		if c.gorace != c.want && testing.Short() {
			continue // the test binary started here sleeps for a second as it exits, under -race
		}
		dir := t.TempDir()
		out, err := command.OutputWithEnv(context.Background(), command.ReadLimit, dir,
			[]string{"GORACE=" + c.gorace, childDirVar + "=" + dir}, self, "-test.run=^TestRaceExitChild$")
		if err != nil {
			t.Fatalf("GORACE=%q: %v\n%s", c.gorace, err, out)
		}
		got, err := os.ReadFile(filepath.Join(dir, "gorace"))
		if err != nil {
			t.Fatalf("GORACE=%q: %v\n%s", c.gorace, err, out)
		}
		if string(got) != c.want {
			t.Errorf("GORACE=%q: the tests' fake tool got GORACE=%q, want %q", c.gorace, got, c.want)
		}
	}
}

// TestRaceExitChild runs, as the tests started by TestAChildHasNoRaceExitSleep, a fake tool that
// writes the GORACE it got.
func TestRaceExitChild(t *testing.T) {
	dir := os.Getenv(childDirVar)
	if dir == "" {
		t.Skip("run by TestAChildHasNoRaceExitSleep")
	}
	bin := Write(t, t.TempDir(), "race-exit", `printf %s "$GORACE" > "$1"`)
	out, err := command.Output(context.Background(), command.ReadLimit, dir, bin, filepath.Join(dir, "gorace"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
