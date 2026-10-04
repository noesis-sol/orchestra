package main

import (
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// openTerminal opens a pseudo-terminal and returns its terminal end, which orchestra takes for a
// terminal, and its master end, which types at it. Both are closed when the test ends; the test is
// skipped where no pseudo-terminal can be opened, and by go test -short: a run on a terminal takes
// seconds.
func openTerminal(t *testing.T) (tty, master *os.File) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped by -short: runs on a pseudo-terminal")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminal:", err)
	}
	t.Cleanup(func() { _ = master.Close() }) // nothing to flush
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil { // unlockpt(3)
		t.Fatal(err)
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN) // ptsname(3)
	if err != nil {
		t.Fatal(err)
	}
	tty, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(n), 10), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tty.Close() }) // nothing to flush
	return tty, master
}
