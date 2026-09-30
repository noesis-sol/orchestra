package command

import "golang.org/x/sys/unix"

// awaitExit waits for the child pid to exit, leaving it unreaped.
func awaitExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			return err
		}
	}
}
