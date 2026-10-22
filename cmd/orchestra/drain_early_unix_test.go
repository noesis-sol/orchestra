//go:build unix

package main

import (
	"syscall"
	"testing"
	"time"
)

// orchestra-xj6z.10: a real SIGUSR1 that comes before the loop listens, during the pre-flight or the
// start question, is caught rather than ignored, and drains the loop as soon as it begins.
func TestARealDrainSignalBeforeTheLoopIsKept(t *testing.T) {
	// Not parallel: it signals the test's own process, which the runs of tests beside it would catch too.
	drains := catchDrains()
	defer drains.release()
	kill(t, syscall.SIGUSR1)
	for start := time.Now(); ; time.Sleep(10 * time.Millisecond) {
		drains.mu.Lock()
		pending := drains.pending
		drains.mu.Unlock()
		if pending {
			break
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("SIGUSR1 before the loop was not caught")
		}
	}
	drained := 0
	drains.to(func() { drained++ })
	if drained != 1 {
		t.Errorf("the loop got %d drains as it began; want the SIGUSR1 before it", drained)
	}
}
