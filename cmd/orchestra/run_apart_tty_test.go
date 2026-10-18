//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// A test whose orchestra is sent a signal, as Ctrl+C at the terminal would send it, runs orchestra
// in a process of its own: the test binary running runMain. In the test's process, the signal would
// reach the runs of the tests beside it too.

var _ = faketool.Func("orchestra", func(args []string) int { return runMain(append([]string{"orchestra"}, args...)) })

// runApartOnTerminal is runOnTerminal with orchestra in a process of its own.
func runApartOnTerminal(t *testing.T, args ...string) (term *fakeTerminal, repo string, exit func() (int, string)) {
	t.Helper()
	return onTerminal(t, apart, args)
}

// apart starts orchestra in a process of its own (startOrchestra), in t's environment, its variables
// over the process's, and working directory. ctx's end kills it.
func apart(ctx context.Context, t *testing.T, args []string, tty *os.File, stderr io.Writer) (<-chan int,
	<-chan struct{}) {
	t.Helper()
	cmd := exec.CommandContext(ctx, faketool.WriteFunc(t, t.TempDir(), "orchestra"), args...)
	cmd.Dir, cmd.Env = workDir(t), append(os.Environ(), testVars(t)...) // the last of a variable wins
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, stderr
	cmd.WaitDelay = 10 * time.Second // for what it started that still holds its stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		err := cmd.Wait()
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			done <- exit.ExitCode() // -1 when a signal ended it
		case err != nil:
			done <- -1
		default:
			done <- 0
		}
	}()
	return done, ended
}
