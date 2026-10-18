//go:build unix

package project

import (
	"os"
	"syscall"
	"testing"
)

// A run lock let go of is free at once, though a copy of its file is still open, as one is in a
// process forked meanwhile until it execs its command: its flock would otherwise last until the copy
// closed.
func TestRunLockIsReleasedThoughACopyIsOpen(t *testing.T) {
	repo, _ := gitRepo(t)
	l := lockedBy(t, repo, Holder{PID: 1})
	copied, err := syscall.Dup(int(l.f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	copyOf := os.NewFile(uintptr(copied), "the lock's copy")
	defer func() { _ = copyOf.Close() }() // only held

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if h, held, err := RunHolder(t.Context(), repo); err != nil || held {
		t.Errorf("RunHolder with a copy open: %+v, held %v, %v", h, held, err)
	}
	second, err := LockRun(t.Context(), repo, Holder{PID: 2})
	if err != nil {
		t.Fatalf("a second run with a copy of the first's lock open: %v", err)
	}
	_ = second.Close()
}
