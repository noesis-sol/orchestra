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

// soon is how long Ask may take to return once the fake claude has exited or been stopped, under the
// race detector on a machine loaded by other tests: well short of the 20 seconds the fakes sleep, so an
// Ask that waits for one still fails.
const soon = 10 * time.Second

// A claude that answers and exits, leaving a process behind that holds its output, gives its answer
// at once.
func TestAskKeepsTheAnswerWhenALeftoverHoldsTheOutput(t *testing.T) {
	bin, dir := fakeScript(t, `sleep 20 &
echo $! > "$(dirname "$0")/pid"
cat > /dev/null
echo '{"type":"result","is_error":false,"result":"ok"}'
`)
	t.Cleanup(func() {
		b, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil {
			return // the fake never started its leftover
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill() // best effort: the leftover would end by itself
			}
		}
	})
	start := time.Now()
	r, err := Client{Bin: bin}.Ask(context.Background(), time.Minute, "low", "s", "i", "")
	if err != nil || r.Result != "ok" {
		t.Fatalf("got %+v, %v; want the answer", r, err)
	}
	if took := time.Since(start); took > soon {
		t.Errorf("returned after %s, want soon after claude exited", took)
	}
}

// A claude stopped at its time limit fails with an error that says so.
func TestAskSaysItTimedOut(t *testing.T) {
	bin, _ := fakeScript(t, "exec sleep 20\n")
	start := time.Now()
	_, err := Client{Bin: bin}.Ask(context.Background(), 200*time.Millisecond, "low", "s", "i", "")
	if err == nil || err.Error() != bin+": timed out after 200ms" {
		t.Errorf("error %v, want it to name the time limit", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false", err)
	}
	if took := time.Since(start); took > soon {
		t.Errorf("returned after %s", took)
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
