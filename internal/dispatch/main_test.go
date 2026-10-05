package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// No goroutine the loop starts outlives its run: a worker per ticket, a status watcher per worker,
// triage, the footprint predictor. Two checks hold the tests to that.

// TestMain fails the package's tests if any goroutine one of them started is still running once
// they have all returned. It covers every test, including those added later. It runs them through
// faketool, for the fake claudes some of them run.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(faketool.Main(m))
}

// testLabel is the profiler label that marks the goroutines a test started, directly or through
// the ones it started: the runtime hands a goroutine's labels down to those it starts.
const testLabel = "orchestra-test"

// noLeaks fails t if a goroutine it starts from here on, or one started by those, is still running
// once t and the cleanups it registers after this have returned. Whole runs call it, so a leak is
// named after the test that caused it, and caught even if the goroutine ends before the package's
// last test does. Unlike goleak.VerifyNone, it ignores the goroutines of the tests running in
// parallel with t.
func noLeaks(t *testing.T) {
	t.Helper()
	pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels(testLabel, t.Name())))
	t.Cleanup(func() {
		pprof.SetGoroutineLabels(context.Background()) // the cleanups run on the test's own goroutine
		var left []string
		for deadline := time.Now().Add(patience); ; time.Sleep(10 * time.Millisecond) {
			if left = labelled(t.Name()); len(left) == 0 {
				return
			}
			if time.Now().After(deadline) {
				break
			}
		}
		t.Errorf("goroutines started by %s are still running:\n\n%s", t.Name(), strings.Join(left, "\n\n"))
	})
}

// labelled returns the stacks of the running goroutines labelled with test, from the goroutine
// profile: one entry per distinct stack, led by how many goroutines share it.
func labelled(test string) []string {
	var b bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&b, 1)   // writing to a bytes.Buffer cannot fail
	_, profile, _ := strings.Cut(b.String(), "\n") // after the "goroutine profile: total N" header
	label := fmt.Sprintf("%q:%q", testLabel, test)
	var stacks []string
	for s := range strings.SplitSeq(profile, "\n\n") {
		for line := range strings.SplitSeq(s, "\n") {
			if strings.HasPrefix(line, "# labels: ") && strings.Contains(line, label) {
				stacks = append(stacks, s)
				break
			}
		}
	}
	return stacks
}
