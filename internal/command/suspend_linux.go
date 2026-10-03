package command

import (
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// cldStopped is si_code CLD_STOPPED, a child stopped by a signal, from <signal.h>.
const cldStopped = 5

// stopped reports whether the unreaped child pid is stopped, leaving the stop for wait to report.
func stopped(pid int) bool {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WSTOPPED|unix.WNOHANG|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err == nil && info.Code == cldStopped
		}
	}
}

// suspend stops orchestra's process group, as Ctrl+Z at the terminal does, and returns once
// orchestra is continued, or once exited is closed. SIGTSTP stops orchestra a moment after kill
// returns, once one of its threads takes it, so suspend waits for SIGCONT. No SIGCONT would come
// where SIGTSTP doesn't stop orchestra, so suspend doesn't send it where orchestra ignores it (as
// whatever started orchestra may have left it) or catches it.
func suspend(exited <-chan struct{}) {
	if !stopsOnTSTP() {
		return
	}
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	defer signal.Stop(cont)
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		return
	}
	select {
	case <-cont:
	case <-exited:
	}
}

// stopsOnTSTP reports whether SIGTSTP stops orchestra: /proc/self/status lists it neither as
// ignored (SigIgn) nor as caught (SigCgt). It reports false where it can't tell.
func stopsOnTSTP() bool {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	tstp := uint64(1) << (syscall.SIGTSTP - 1)
	for line := range strings.Lines(string(status)) {
		name, mask, _ := strings.Cut(line, ":")
		if name != "SigIgn" && name != "SigCgt" {
			continue
		}
		if m, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64); err != nil || m&tstp != 0 {
			return false
		}
	}
	return true
}
