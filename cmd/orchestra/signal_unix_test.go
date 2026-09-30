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
		syscall.Kill(os.Getpid(), sig)
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
