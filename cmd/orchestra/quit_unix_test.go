//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A real second SIGINT, caught while the stopped loop winds down with nothing to finish, ends
// orchestra from the watch's own goroutine.
func TestASecondRealSignalQuits(t *testing.T) {
	r := quitRigFor(t, &windingLoop{})
	exited := make(chan int, 2)
	q := r.quitter()
	q.exit = func(code int) { exited <- code }
	r.stops.quitWith(q)
	stopped := make(chan os.Signal, 1)
	r.stops.on(func(s os.Signal) { stopped <- s })

	kill(t, syscall.SIGINT)
	select {
	case <-stopped:
	case code := <-exited:
		t.Fatalf("the first SIGINT quit, exit %d", code)
	case <-time.After(5 * time.Second):
		t.Fatal("the first SIGINT was not caught")
	}
	kill(t, syscall.SIGINT)
	select {
	case code := <-exited:
		if code != dispatch.ExitInterrupted {
			t.Errorf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second SIGINT didn't quit")
	}
}
