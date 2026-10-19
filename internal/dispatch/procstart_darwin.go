package dispatch

import (
	"time"

	"golang.org/x/sys/unix"
)

// processStart returns when process pid started, as the kernel keeps it.
func processStart(pid int) (time.Time, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(kp.Proc.P_starttime.Unix()), nil
}
