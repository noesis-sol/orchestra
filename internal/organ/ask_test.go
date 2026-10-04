package organ

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeScript writes a stand-in for the claude CLI that runs body, from a folder of its own.
func fakeScript(t *testing.T, body string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// hung is how long a test lets Ask run before taking it for stuck on a process it should have stopped or
// left behind: far longer than Ask takes under the race detector on a machine loaded by other workers'
// tests, and far shorter than the fakes' processes sleep, so a slow Ask passes and only one that waits
// for such a process reaches it.
const hung = time.Minute

// askUnhung returns what ask returns, unless ask is still running after hung: then it kills the process
// whose PID the fake claude wrote to the file pid in dir, so that ask returns, and fails the test, which
// wanted ask back once claude had exited (or was stopped, as want says).
func askUnhung(t *testing.T, dir, want string, ask func() (Result, error)) (Result, error) {
	t.Helper()
	type answer struct {
		r   Result
		err error
	}
	done := make(chan answer, 1)
	go func() {
		r, err := ask()
		done <- answer{r, err}
	}()
	var a answer
	select {
	case a = <-done:
	case <-time.After(hung):
		killFake(dir)
		a = <-done
		t.Fatalf("Ask was still running after %s, want it back %s", hung, want)
	}
	return a.r, a.err
}

// killFake kills the process whose PID the fake claude wrote to the file pid in dir, if it wrote one.
func killFake(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		return // the fake never got that far
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill() // best effort: the process may have ended
		}
	}
}

// A claude that answers and exits, leaving a process behind that holds its output, gives its answer
// without waiting for that process to end.
func TestAskKeepsTheAnswerWhenALeftoverHoldsTheOutput(t *testing.T) {
	bin, dir := fakeScript(t, `sleep 600 &
echo $! > "$(dirname "$0")/pid"
cat > /dev/null
echo '{"type":"result","is_error":false,"result":"ok"}'
`)
	t.Cleanup(func() { killFake(dir) }) // the leftover would sleep on past the test
	r, err := askUnhung(t, dir, "once claude exited, with its leftover still running", func() (Result, error) {
		return Client{Bin: bin}.Ask(context.Background(), time.Minute, "low", "s", "i", "")
	})
	if err != nil || r.Result != "ok" {
		t.Fatalf("got %+v, %v; want the answer", r, err)
	}
}

// A claude stopped at its time limit fails with an error that says so.
func TestAskSaysItTimedOut(t *testing.T) {
	// The PID is claude's own, which nothing can take before Ask reaps it: a kill can't reach a stranger.
	bin, dir := fakeScript(t, "echo $$ > \"$(dirname \"$0\")/pid\"\nexec sleep 600\n")
	_, err := askUnhung(t, dir, "at its 200ms time limit", func() (Result, error) {
		return Client{Bin: bin}.Ask(context.Background(), 200*time.Millisecond, "low", "s", "i", "")
	})
	if err == nil || err.Error() != bin+": timed out after 200ms" {
		t.Errorf("error %v, want it to name the time limit", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false", err)
	}
}

// A claude stopped by its context fails with the context's cause.
func TestAskSaysWhyItWasStopped(t *testing.T) {
	bin, _ := fakeScript(t, "exec sleep 20\n")
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(errors.New("stopped with Ctrl+C")) })
	if _, err := (Client{Bin: bin}).Ask(ctx, time.Minute, "low", "s", "i", ""); err == nil ||
		err.Error() != bin+": stopped with Ctrl+C" {
		t.Errorf("error %v, want the cause", err)
	}
}

// A claude that fails says why on stderr, which the error keeps; the long arguments are left out.
func TestAskReportsAFailureWithItsStderr(t *testing.T) {
	bin, _ := fakeScript(t, "echo 'not logged in' >&2\nexit 1\n")
	_, err := Client{Bin: bin}.Ask(context.Background(), time.Minute, "low", "SYSTEM", "i", `{"type":"object"}`)
	if err == nil || err.Error() != bin+": exit status 1: not logged in" {
		t.Errorf("error %v, want claude's exit status and stderr alone", err)
	}
}
