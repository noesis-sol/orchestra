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

// unlockFile releases the flock on f, then closes f. Closing f alone would release it only once every
// copy of f is closed: a process forked meanwhile holds one until it execs its command, which under
// load takes a while, and another run in the process, as in tests, would find the lock still held.
// Unlocked, the lock is released at once, whatever holds a copy.
func unlockFile(f *os.File) error {
	if conn, err := f.SyscallConn(); err == nil {
		_ = conn.Control(func(fd uintptr) { _ = syscall.Flock(int(fd), syscall.LOCK_UN) }) // closing releases it too
	}
	return f.Close()
}
