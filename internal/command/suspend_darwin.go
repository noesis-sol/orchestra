package command

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// sstop is p_stat SSTOP, a process stopped by a signal, from <sys/proc.h>.
const sstop = 4

// stopped reports whether the unreaped child pid is stopped.
func stopped(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && kp.Proc.P_stat == sstop
}

// suspend stops orchestra's process group, as Ctrl+Z at the terminal does, and returns once
// orchestra is continued: SIGTSTP suspends orchestra before kill returns. Where SIGTSTP doesn't
// stop orchestra (ignored, or a process group no shell is left to continue), it returns at once.
func suspend(<-chan struct{}) {
	_ = syscall.Kill(0, syscall.SIGTSTP) // best effort: the group is orchestra's own, and if not, nothing more can be done
}
