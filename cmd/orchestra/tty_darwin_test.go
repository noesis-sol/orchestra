package main

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// openTerminal opens a pseudo-terminal and returns its terminal end, which orchestra takes for a
// terminal, and its master end, which types at it. Both are closed when the test ends; the test is
// skipped where no pseudo-terminal can be opened.
func openTerminal(t *testing.T) (tty, master *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pseudo-terminal:", err)
	}
	t.Cleanup(func() { _ = master.Close() }) // nothing to flush
	fd := int(master.Fd())
	var st unix.Stat_t
	for _, err := range []error{unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0), unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0),
		unix.Fstat(fd, &st)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	// The terminal end is named after the master's minor device number, as ptsname(3) has it.
	tty, err = os.OpenFile(fmt.Sprintf("/dev/ttys%03d", unix.Minor(uint64(st.Rdev))), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tty.Close() }) // nothing to flush
	return tty, master
}
