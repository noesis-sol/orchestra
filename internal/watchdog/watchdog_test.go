package watchdog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }

// childVar names, in the environment of the test binary started by TestTheScriptRunsOnceTheTestIsGone,
// the file TestWatchdogChild's watchdog writes, and endVar how that test ends.
const (
	childVar = "ORCHESTRA_WATCHDOG_FILE"
	endVar   = "ORCHESTRA_WATCHDOG_END"
)

// A watchdog runs its script once the process that started it has exited, even killed, before any
// cleanup of its could run; stopped first, it never does.
func TestTheScriptRunsOnceTheTestIsGone(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		end  string
		runs bool
	}{{"killed", true}, {"exits", true}, {"stopped", false}} {
		file := filepath.Join(t.TempDir(), "ran")
		out, err := command.OutputWithEnv(context.Background(), command.ReadLimit, "",
			[]string{childVar + "=" + file, endVar + "=" + c.end}, self, "-test.run=^TestWatchdogChild$")
		if c.end != "killed" && err != nil {
			t.Fatalf("%s: %v\n%s", c.end, err, out)
		}
		if !c.runs {
			// Stop waits for the watchdog to exit, and the test binary for Stop.
			if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s: the watchdog's script ran (%v)", c.end, err)
			}
			continue
		}
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(file); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the watchdog's script hadn't run 10s after the test binary exited\n%s", c.end, out)
			}
		}
	}
}

// TestWatchdogChild runs, as the test binary started by TestTheScriptRunsOnceTheTestIsGone, a
// watchdog that writes the file it is given, and ends as that test asks: killed, exiting, or
// stopping the watchdog first.
func TestWatchdogChild(t *testing.T) {
	file := os.Getenv(childVar)
	if file == "" {
		t.Skip("run by TestTheScriptRunsOnceTheTestIsGone")
	}
	d, err := Start("touch " + command.ShellQuote(file))
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv(endVar) {
	case "killed":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL) // never returns
	case "stopped":
		d.Stop()
	}
}
