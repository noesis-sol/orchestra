//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// kill sends sig to the test's own process.
func kill(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
}

// SIGTERM and SIGHUP are caught like Ctrl+C: nothing kills orchestra before it logs the stop.
func TestStopSignalsAreCaught(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		stops := catchStops()
		stopped := make(chan os.Signal, 1)
		stops.on(func(s os.Signal) { stopped <- s })
		kill(t, sig)
		select {
		case s := <-stopped:
			if s != sig || !stops.leaving() {
				t.Errorf("%v: the phase got %v, leaving %v", sig, s, stops.leaving())
			}
		case <-time.After(5 * time.Second):
			stops.release()
			t.Fatalf("%v was not caught", sig)
		}
		stops.release()
		if !leaves(sig) {
			t.Errorf("%v should skip the organ phase", sig)
		}
	}
	if leaves(os.Interrupt) || leaves(nil) {
		t.Error("Ctrl+C and the loop's own end keep the organ phase")
	}
}

// Once the loop has been stopped, more stop signals stay caught while it winds down, which may be
// finishing a merge: the test process would die here if they didn't. Until the watch knows how to
// quit (quitWith), they wait for the next phase, the first of them stopping it.
func TestStopSignalsStayCaughtWhileTheLoopWindsDown(t *testing.T) {
	stops := catchStops()
	defer stops.release()
	stopped := make(chan os.Signal, 2)
	stops.on(func(s os.Signal) { stopped <- s })
	kill(t, syscall.SIGINT)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT was not caught")
	}
	stops.on(nil) // the dashboard has closed: the loop winds down
	waitFor := func(what string, done func() bool) {
		t.Helper()
		for start := time.Now(); !done(); time.Sleep(10 * time.Millisecond) {
			if time.Since(start) > 5*time.Second {
				t.Fatalf("%s was not caught", what)
			}
		}
	}
	kill(t, syscall.SIGINT)
	waitFor("the second SIGINT", func() bool {
		stops.mu.Lock()
		defer stops.mu.Unlock()
		return stops.pending != nil
	})
	kill(t, syscall.SIGTERM)
	kill(t, syscall.SIGHUP)
	waitFor("SIGTERM or SIGHUP", stops.leaving)
	if len(stopped) > 0 {
		t.Errorf("%v went to the stopped loop", <-stopped)
	}
	var next os.Signal
	stops.on(func(s os.Signal) { next = s })
	if next != syscall.SIGINT {
		t.Errorf("the next phase got %v, want the second SIGINT", next)
	}
}

// SIGUSR1 asks the run to stop after its running tickets, each time it comes.
func TestDrainSignalIsCaught(t *testing.T) {
	drained := make(chan struct{}, 2)
	stop := watchDrain(func() { drained <- struct{}{} })
	defer stop()
	for i := range 2 {
		kill(t, syscall.SIGUSR1)
		select {
		case <-drained:
		case <-time.After(5 * time.Second):
			t.Fatalf("SIGUSR1 %d was not caught", i+1)
		}
	}
	if name := signalName(syscall.SIGUSR1); name != "SIGUSR1" {
		t.Errorf("signalName = %q", name)
	}
}
