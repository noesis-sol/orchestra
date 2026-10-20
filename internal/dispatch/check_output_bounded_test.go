package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// What orchestra keeps of a check's output as the check prints it is bounded, whatever the check
// prints: the whole output goes to a file, and only what is said of it stays in memory.

// writeIn writes out to c in chunks of size bytes, as a command's pipe hands it over, and ends it.
func writeIn(c *checkOutput, out string, size int) {
	for b := []byte(out); len(b) > 0; {
		n := min(size, len(b))
		if _, err := c.Write(b[:n]); err != nil {
			panic(err) // Write never fails
		}
		b = b[n:]
	}
	c.end()
}

// However the output is cut as it arrives, what is kept of it is what the whole of it says.
func TestCheckOutputKeepsWhatTheWholeOutputSays(t *testing.T) {
	t.Parallel()
	var long strings.Builder
	for i := range 3 * keptLines {
		if i%7 == 0 {
			long.WriteString("\n\n")
		}
		fmt.Fprintf(&long, "line %d\r\n", i)
		if i%11 == 0 {
			fmt.Fprintf(&long, "--- FAIL: Test%d (0.00s)\nFLAKY: ./a Test%d\nSUITE: suite %d\n", i, i, i)
		}
	}
	for _, out := range []string{
		"", "\n", "\n\n\n", "a", "a\nb", "a\nb\n", "\n\na\n\n\nb\n\n", " \n\t\nend",
		"panic: test timed out after 10m0s\n\trunning tests:\n\t\tTestHang (10m0s)\n\nFAIL\texample.com/m/a\t600.1s\n",
		long.String(),
	} {
		for _, size := range []int{1, 3, 64, 1 << 20} {
			c := &checkOutput{}
			writeIn(c, out, size)
			for _, n := range []int{1, maxSaid, keptLines} {
				if got, want := c.lastLines(n), lastLines(out, n); got != want {
					t.Errorf("%q in chunks of %d: last %d lines %q, want %q", out, size, n, got, want)
				}
			}
			whole := &checkOutput{}
			writeIn(whole, out, len(out)+1)
			if got, want := c.failures(), whole.failures(); !slices.Equal(got, want) {
				t.Errorf("%q in chunks of %d: failures %q, want %q", out, size, got, want)
			}
			if !slices.Equal(c.flaky, whole.flaky) || c.suite != whole.suite {
				t.Errorf("%q in chunks of %d: flaky %q, suite %q; want %q, %q", out, size, c.flaky, c.suite,
					whole.flaky, whole.suite)
			}
		}
	}
}

// The plain searches that spare most lines the regexps leave out no line the regexps match.
func TestSaysFailedIsWhatTheRegexpsMatch(t *testing.T) {
	t.Parallel()
	words := []string{"", "FAIL", "FAIL\t./a", "FAIL:", "--- FAIL", "=== FAIL", "DATA RACE", "WARNING: DATA RACE",
		"race detected", "panic:", "panic", "error", "Error", "ERROR:", "errors", "error_x", "fatal:", "level=error",
		"level=warn", "DONE 3 tests, 1 failure", "DONE x fail", "12 issues:", "1 issue:", "a.go:1: x", "a.go:1:2: x",
		"a.go:x: ", "ok", "  ", "\t", ":", "x", "(fail)", "(fail) ", "fail", "failing", "failed", "FAILED", "✕ ", "× ",
		"✗", "●", "a::b", "test", "...", "test result:", "thread 'a'", "panicked at", "1)", ") ", "not ok", "1", "Tests:",
		"Test Files", "=", "==="}
	for _, a := range words {
		for _, b := range words {
			for _, line := range []string{a + b, a + " " + b, "  " + a + b} {
				t1 := []byte(strings.TrimSpace(line))
				want := failedAnywhere.Match(t1) || failedTest.Match(t1) || failedAtStart.Match([]byte(line))
				if got := saysFailed(t1, []byte(line)); got != want {
					t.Errorf("%q: saysFailed %v, the regexps %v", line, got, want)
				}
			}
		}
	}
}

// A line without end, a flood of failures each said once, and a flood of FLAKY: lines are each held
// to a bound, and still counted.
func TestCheckOutputBoundsWhatItHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: writes floods of output through the check's reader")
	}
	t.Parallel()
	c := &checkOutput{}
	endless := strings.Repeat("x", 4*maxLineBytes)
	writeIn(c, endless+"\n--- FAIL: TestLast (0.00s)\n", 1000)
	if got := c.lastLines(2); got != endless[:maxLineBytes]+"\n--- FAIL: TestLast (0.00s)" {
		t.Errorf("a long line should be cut to %d bytes: %d bytes kept", maxLineBytes, len(got))
	}
	if cap(c.line) > 2*maxLineBytes {
		t.Errorf("the line being printed holds %d bytes", cap(c.line))
	}

	// Each failure is said twice: the first maxSeen are told apart and counted once, the others twice.
	c = &checkOutput{}
	var out strings.Builder
	for i := range maxSeen + 10 {
		fmt.Fprintf(&out, "--- FAIL: Test%d (0.00s)\n--- FAIL: Test%d (0.00s)\n", i, i)
	}
	for i := range 2 * maxFlaky {
		fmt.Fprintf(&out, "FLAKY: ./a Test%d\n", i)
	}
	writeIn(c, out.String(), 1<<16)
	said := c.failures()
	if len(said) != maxSaid || said[0] != "--- FAIL: Test0 (0.00s)" ||
		said[maxSaid-1] != fmt.Sprintf("(… and %d more lines like these)", maxSeen+20-maxSaid+1) {
		t.Errorf("said: %q … %q", said[0], said[len(said)-1])
	}
	if len(c.seen) != maxSeen || len(c.said) != maxSaid {
		t.Errorf("%d failures told apart and %d kept, want %d and %d", len(c.seen), len(c.said), maxSeen, maxSaid)
	}
	if len(c.flaky) != maxFlaky || c.flakyN != 2*maxFlaky || c.flaky[0] != "./a Test0" {
		t.Errorf("%d FLAKY: lines kept of %d, the first %q; want %d of %d", len(c.flaky), c.flakyN, c.flaky[0],
			maxFlaky, 2*maxFlaky)
	}
}

// orchestra-74b2.24 on 2026-10-14: the check's whole output was read into memory, and copied again
// as it was searched and saved, so a check that loops printing held all of it until its time limit.
// A check that prints 32 MB has orchestra allocate a fraction of that, and its whole output is
// still kept in the worktree, its end in the log and what failed for the reviewer.
func TestAChecksEndlessOutputIsNotHeldInMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("prints 32 MB")
	}
	// Not parallel: the allocations measured are the whole test binary's, so this test runs alone.
	const size = 32 << 20
	f := newMergeFixture(t, fmt.Sprintf(`l=$(printf '%%0999d' 0); yes "$l" | head -c %d; echo; `+
		`echo '--- FAIL: TestLoop (0.00s)'; echo 'FAIL ./a'; exit 1`, size))
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	runtime.ReadMemStats(&after)
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("orchestra allocated %d KB for a check that printed %d MB", alloc>>10, size>>20)
	if alloc > size/4 {
		t.Errorf("orchestra allocated %d MB for a check that printed %d MB", alloc>>20, size>>20)
	}

	saved := filepath.Join(wt, project.RunPath(checkLogName))
	want := int64(size + len("\n--- FAIL: TestLoop (0.00s)\nFAIL ./a\n"))
	if fi, err := os.Stat(saved); err != nil || fi.Size() != want {
		t.Errorf("%s should hold the whole output, %d bytes: %v %v", saved, want, fi, err)
	}
	if !f.sink.warnedWith("CHECKS_FAILED", "k-1", "(output is in "+saved+")") {
		t.Errorf("CHECKS_FAILED should name %s; events:\n%s", saved, f.sink.text())
	}
	r, _ := f.orch.book.get("k-1")
	if said := r.failedCheck; said == nil || !slices.Equal(said.said, []string{"--- FAIL: TestLoop (0.00s)", "FAIL ./a"}) {
		t.Errorf("the reviewer should be told what failed: %+v", said)
	}
	log := read(t, f.orch.log.f.Name())
	if !strings.Contains(log, "--- FAIL: TestLoop (0.00s)\nFAIL ./a") {
		t.Errorf("the log should have the output's end")
	}
	if len(log) > 2*keptLines*maxLineBytes {
		t.Errorf("the log holds %d bytes: it should have the output's end alone", len(log))
	}
}
