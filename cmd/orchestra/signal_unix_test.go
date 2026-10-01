package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// SIGTERM and SIGHUP are caught like Ctrl+C: nothing kills orchestra before it logs the stop.
func TestStopSignalsAreCaught(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		stopped := make(chan os.Signal, 1)
		stop := watchSignals(func(s os.Signal) { stopped <- s })
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatal(err)
		}
		select {
		case s := <-stopped:
			if got := stop(); s != sig || got != sig {
				t.Errorf("%v: onStop got %v, stop returned %v", sig, s, got)
			}
		case <-time.After(5 * time.Second):
			stop()
			t.Fatalf("%v was not caught", sig)
		}
		if !leaving(sig) {
			t.Errorf("%v should skip the organ phase", sig)
		}
	}
	if stop := watchSignals(func(os.Signal) {}); stop() != nil {
		t.Error("no signal came, but stop returned one")
	}
	if leaving(os.Interrupt) || leaving(nil) {
		t.Error("Ctrl+C and the loop's own end keep the organ phase")
	}
}

// SIGUSR1 asks the run to stop after its running tickets, each time it comes.
func TestDrainSignalIsCaught(t *testing.T) {
	drained := make(chan struct{}, 2)
	stop := watchDrain(func() { drained <- struct{}{} })
	defer stop()
	for i := 0; i < 2; i++ {
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
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
