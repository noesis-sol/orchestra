package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// An overnight run keeps the machine from sleeping while it goes, so that no worker is cut off in
// the night, and so does every run with "keep_awake": true in settings.json: the system's keep-awake
// command, given orchestra's PID, starts once the pre-flight has cleared the run, and stops as the
// run's session closes, after the organ phase. Given the PID, the command ends by itself should
// orchestra end some other way. The pre-flight's checklist says whether the machine is kept awake, and
// warns where the command is missing.

// awakeCommand is the command that keeps a system awake: its name, and its arguments given the PID of
// the process it keeps the machine awake for, until that process exits or the command is stopped.
type awakeCommand struct {
	name string
	args func(pid int) []string
}

// line is the command as run for pid, as the log gives it.
func (c awakeCommand) line(pid int) string { return c.name + " " + strings.Join(c.args(pid), " ") }

// keepAwakeOf is the keep-awake command of the system goos, if it has one: caffeinate on macOS, which
// prevents idle sleep (-i) until the process -w names exits, and on Linux systemd-inhibit, which holds
// off idle and sleep while the command it runs goes, there a tail that ends with the process.
func keepAwakeOf(goos string) (awakeCommand, bool) {
	switch goos {
	case "darwin":
		return awakeCommand{name: "caffeinate", args: func(pid int) []string {
			return []string{"-i", "-w", strconv.Itoa(pid)}
		}}, true
	case "linux":
		return awakeCommand{name: "systemd-inhibit", args: func(pid int) []string {
			return []string{"--what=idle:sleep", "--who=orchestra", "--why=a run of workers", "--mode=block",
				"tail", "--pid=" + strconv.Itoa(pid), "-f", "/dev/null"}
		}}, true
	}
	return awakeCommand{}, false
}

// keepAwakeCheck says whether an overnight run on the system goos keeps the machine awake: it warns
// where the system has no keep-awake command, or it isn't on the PATH.
func keepAwakeCheck(goos string) preflightCheck {
	return func(ctx context.Context, _ *session) preflightResult {
		c, ok := keepAwakeOf(goos)
		if !ok {
			return preflightResult{verdict: tui.CheckWarned, text: "orchestra can't keep the machine awake on " +
				goos + ": set it not to sleep, or the run sleeps with it"}
		}
		if _, err := command.LookPath(ctx, c.name); err != nil {
			return preflightResult{verdict: tui.CheckWarned, text: c.name + " isn't on the PATH: nothing keeps " +
				"the machine awake, and the run sleeps with it. Set the machine not to sleep"}
		}
		return preflightResult{verdict: tui.CheckPassed, text: "the machine is kept awake while the run goes (" +
			c.name + ")"}
	}
}

// keepAwakeGrace is how long the keep-awake command has between SIGTERM and SIGKILL as it is stopped.
const keepAwakeGrace = time.Second

// keepAwake keeps the machine awake for an overnight run, or any run with keep_awake set, until its
// session closes: it starts this system's keep-awake command with orchestra's PID, and close stops it.
// A command missing, which an overnight run's checklist warned of, or ending by itself while the run
// goes, is a line in the log.
func (s *session) keepAwake(ctx context.Context) {
	if !s.cfg.Overnight && !s.cfg.keepAwake {
		return
	}
	c, ok := keepAwakeOf(runtime.GOOS)
	if !ok {
		s.log.Line(time.Now(), "nothing keeps the machine awake on "+runtime.GOOS)
		return
	}
	if _, err := command.LookPath(ctx, c.name); err != nil {
		s.log.Line(time.Now(), "nothing keeps the machine awake: "+c.name+" isn't on the PATH")
		return
	}
	// Only close stops it: the organ phase outlasts the run's context.
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	pid := os.Getpid()
	go func() {
		defer close(done)
		var out bytes.Buffer
		err := command.GroupRun(ctx, keepAwakeGrace, s.cfg.Repo, &out, c.name, c.args(pid)...)
		if ctx.Err() != nil { // stopped by close
			return
		}
		why := "exit status 0"
		if err != nil {
			why = err.Error()
		}
		if said := oneLine(out.String()); said != "" {
			why += ": " + said
		}
		s.log.Line(time.Now(), fmt.Sprintf("%s ended (%s): nothing keeps the machine awake", c.name, why))
	}()
	s.log.Line(time.Now(), "keeping the machine awake while the run goes: "+c.line(pid))
	s.onClose(func(error) {
		cancel()
		<-done
	})
}
