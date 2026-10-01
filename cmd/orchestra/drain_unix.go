//go:build unix

package main

import (
	"os"
	"syscall"
)

// drainSignals ask a run to stop after its running tickets, as s in the dashboard does: SIGUSR1,
// for -plain, scripts and the skill.
var drainSignals = []os.Signal{syscall.SIGUSR1}
