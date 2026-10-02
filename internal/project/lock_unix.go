//go:build unix

package project

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an flock on f, exclusive or shared, without waiting: errLocked when another open
// file holds a lock that conflicts. Closing f releases it, as the process ending does.
func lockFile(f *os.File, exclusive bool) error {
	how := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		how = syscall.LOCK_EX | syscall.LOCK_NB
	}
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lockErr error
	if err := conn.Control(func(fd uintptr) {
		for {
			if lockErr = syscall.Flock(int(fd), how); !errors.Is(lockErr, syscall.EINTR) {
				return
			}
		}
	}); err != nil {
		return err
	}
	if errors.Is(lockErr, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return lockErr
}
