//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// An overnight run keeps the machine awake while it goes: its checklist says so, the system's
// keep-awake command starts with orchestra's PID once the pre-flight clears the run, and stops as it
// ends. Where there is none, the checklist warns, and the run starts all the same.

// An overnight run starts the keep-awake command with orchestra's PID, says so in its checklist and its
// log, and stops the command as the run ends.
func TestAnOvernightRunKeepsTheMachineAwake(t *testing.T) {
	t.Parallel()
	c, ok := keepAwakeOf(runtime.GOOS)
	if !ok {
		t.Skip("no keep-awake command on " + runtime.GOOS)
	}
	preflightWith(t, keepAwakeCheck(runtime.GOOS))
	dir := nothingTools(t, backlog{})
	repo, stdout, stderr, code := runNothing(t, func(string) {}, "--overnight")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if want := "  passed: the machine is kept awake while the run goes (" + c.name + ")\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	want := strings.Join(c.args(os.Getpid()), " ") + "\nstopped\n"
	if calls := read(t, filepath.Join(dir, c.name+"-calls")); calls != want {
		t.Errorf("%s's calls:\n%s\nwant (started with orchestra's PID, then stopped):\n%s", c.name, calls, want)
	}
	if lines := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(lines,
		"keeping the machine awake while the run goes: "+c.line(os.Getpid())+"\n") {
		t.Errorf("the log doesn't say the machine is kept awake:\n%s", lines)
	}
}

// A run that isn't overnight leaves the machine to its own settings.
func TestARunNotOvernightIsNotKeptAwake(t *testing.T) {
	t.Parallel()
	c, ok := keepAwakeOf(runtime.GOOS)
	if !ok {
		t.Skip("no keep-awake command on " + runtime.GOOS)
	}
	dir := nothingTools(t, backlog{})
	if _, stdout, _, code := runNothing(t, func(string) {}, "--tickets"); code != dispatch.ExitOK {
		t.Fatalf("exit %d; stdout:\n%s", code, stdout)
	}
	if b, err := os.ReadFile(filepath.Join(dir, c.name+"-calls")); !os.IsNotExist(err) {
		t.Errorf("%s was started (%v):\n%s", c.name, err, b)
	}
}

// Where nothing keeps the machine awake, the checklist warns, and the run starts all the same.
func TestNothingToKeepTheMachineAwakeIsAWarning(t *testing.T) {
	t.Parallel()
	preflightWith(t, keepAwakeCheck("plan9"))
	nothingTools(t, backlog{})
	_, stdout, stderr, code := runNothing(t, func(string) {}, "--overnight")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	want := "  warning: orchestra can't keep the machine awake on plan9: set it not to sleep, or the run sleeps with it\n"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}

// The check warns where the system has no keep-awake command, or it isn't on the PATH.
func TestKeepAwakeCheck(t *testing.T) {
	t.Parallel()
	ctx := command.WithEnv(context.Background(), "PATH="+t.TempDir())
	for _, tc := range []struct {
		goos, want string
	}{
		{"darwin", "caffeinate isn't on the PATH: nothing keeps the machine awake, and the run sleeps with it. " +
			"Set the machine not to sleep"},
		{"linux", "systemd-inhibit isn't on the PATH: nothing keeps the machine awake, and the run sleeps with it. " +
			"Set the machine not to sleep"},
		{"windows", "orchestra can't keep the machine awake on windows: set it not to sleep, or the run sleeps with it"},
	} {
		r := keepAwakeCheck(tc.goos)(ctx, nil)
		if r.verdict != tui.CheckWarned || r.text != tc.want || r.fix != nil {
			t.Errorf("%s: %v %q (fix %v), want a warning %q", tc.goos, r.verdict, r.text, r.fix, tc.want)
		}
	}
}
