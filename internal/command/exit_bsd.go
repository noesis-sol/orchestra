//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package command

import "golang.org/x/sys/unix"

// awaitExit waits for the child pid to exit, leaving it unreaped.
func awaitExit(pid int) error {
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kq)
	ev := make([]unix.Kevent_t, 1)
	unix.SetKevent(&ev[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	ev[0].Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(kq, ev, nil, nil); err == unix.ESRCH {
		return nil // pid is our unreaped child, so it has exited already
	} else if err != nil {
		return err
	}
	for {
		n, err := unix.Kevent(kq, nil, ev, nil)
		if err == nil && n > 0 {
			return nil
		}
		if err != nil && err != unix.EINTR {
			return err
		}
	}
}
