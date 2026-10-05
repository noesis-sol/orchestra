package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What the reviewer is told of a failed check: the lines of its output that say what failed, and the
// directories the ticket's own commits change, so the run report can tell a failure in the ticket's
// code from one elsewhere.

// changesPackage claims the ticket, has main move on while it works, so its branch is rebased and
// checked before it merges, then commits internal/a/a.go and CHANGELOG.md and closes it.
func changesPackage(repo string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		landsOnMain(w, repo, "landed/landed.txt", "landed\n")
		if err := os.MkdirAll(filepath.Join(w.wt, "internal", "a"), 0o755); err != nil {
			w.t.Error(err)
		}
		w.write(w.wt, "internal/a/a.go", "package a\n")
		w.write(w.wt, "CHANGELOG.md", "- a\n")
		w.gitIn(w.wt, "add", "internal/a/a.go", "CHANGELOG.md")
		w.gitIn(w.wt, "commit", "-q", "-m", w.id+": change internal/a")
		w.close()
		return "idle"
	}
}

// orchestra-2e1e on 2026-10-04: the run report said a ticket's check failed because it needed
// another ticket's change, when the check failed on a flaky test in a package neither touched; the
// reviewer saw only the CHECKS_FAILED line. Its evidence now names the failing test, which the end
// of the output (all the log keeps) had pushed out, and the directories the ticket changed.
func TestTheReviewerIsToldWhatAFailedCheckSaid(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	out.WriteString("FAIL\nFAIL\texample.com/m/cmd/orchestra\t83.061s\nok  \texample.com/m/internal/a\t0.512s\nFAIL\n\n" +
		"=== Failed\n=== FAIL: cmd/orchestra TestTerminalShowsThePlan (2.00s)\n" +
		"    plan_tty_test.go:41: the screen never showed the plan\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&out, "        screen line %d\n", i)
	}
	out.WriteString("\nDONE 2 runs, 120 tests, 1 failure in 90.1s\nThe tests ran in the order of -shuffle=123\n")
	output := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(output, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.repo, "landed"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.cfg.Check = "cat " + output + "; exit 1"
	h.beads.add("A", "first", 1)
	h.worker("A", changesPackage(h.repo))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "CHECKS_FAILED: A closed") {
		t.Fatalf("A should be set aside for its failed check; events:\n%s", ev)
	}

	in := o.reviewInput(t.Context(), code, o.Final())
	saved := filepath.Join(h.worktree("A"), ".orchestra", "run", checkLogName)
	for _, want := range []string{
		"## What the check said of each ticket set aside after its check failed",
		"A: '" + h.cfg.Check + "' fails on wt/A rebased onto main at ",
		"its output is in " + saved + ".\n",
		"The lines of its output that say what failed:\n" +
			"    FAIL\texample.com/m/cmd/orchestra\t83.061s\n" +
			"    === FAIL: cmd/orchestra TestTerminalShowsThePlan (2.00s)\n" +
			"    DONE 2 runs, 120 tests, 1 failure in 90.1s\n",
		"Directories that A's own commits change (",
		"..wt/A): the top level (.), internal/a\n",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("the reviewer's evidence lacks %q:\n%s", want, in)
		}
	}
	if strings.Contains(in, "screen line") {
		t.Errorf("the reviewer's evidence should hold only the lines that say what failed:\n%s", in)
	}
	if outside := taggedBody.ReplaceAllString(in, ""); strings.Contains(outside, "TestTerminalShowsThePlan") {
		t.Errorf("the check's output is outside the evidence tags:\n%s", outside)
	}
}

// A check that fails on a resolution its worker made after a hand-back sets the ticket aside too,
// and the reviewer is told what it said: here nothing, which it says.
func TestTheReviewerIsToldWhatAFailedCheckSaidOfAResolution(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo), resolvesWith("<<<<<<< ours\nmain\n=======\nA\n>>>>>>> theirs\n"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	in := o.reviewInput(t.Context(), code, o.Final())
	for _, want := range []string{
		"A: '" + h.cfg.Check + "' fails on wt/A rebased onto main at ",
		"\nIt printed nothing.\n",
		"own commits change (",
		"..wt/A): the top level (.)\n",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("the reviewer's evidence lacks %q:\n%s", want, in)
		}
	}
}

// Without a failed check, the reviewer's evidence has no section for one.
func TestTheReviewerIsToldOfNoFailedCheckWithoutOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Check = "true"
	h.beads.add("A", "first", 1)
	h.worker("A", landsOnMainMeanwhile(h.repo, "a.txt"))
	o, code := h.run()
	if in := o.reviewInput(t.Context(), code, o.Final()); strings.Contains(in, "check failed") {
		t.Errorf("the reviewer's evidence speaks of a failed check:\n%s", in)
	}
}

func TestFailureLinesSayWhatFailed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, out string
		want      []string
	}{
		{"go test", "=== RUN   TestA\n--- FAIL: TestA (0.00s)\n    a_test.go:6: got 1, want 2\n" +
			"    --- FAIL: TestA/inner (0.00s)\nFAIL\nFAIL\texample.com/m/a\t0.857s\nok  \texample.com/m/b\t0.1s\n",
			[]string{"--- FAIL: TestA (0.00s)", "--- FAIL: TestA/inner (0.00s)", "FAIL\texample.com/m/a\t0.857s"}},
		{"data race under gotestsum", "FAIL\texample.com/m/b\t0.496s\n\n=== Failed\n=== FAIL: b TestRace (0.00s)\n" +
			"==================\nWARNING: DATA RACE\nRead at 0x00c000012298 by goroutine 8:\n" +
			"  example.com/m/b.TestRace.func1()\n      /src/b/b_test.go:8 +0x30\n==================\n" +
			"    testing.go:1865: race detected during execution of test\n\n" +
			"DONE 5 tests, 4 failures in 0.857s\nERROR rerun aborted because previous run had a data race\nexit status 3\n",
			[]string{"FAIL\texample.com/m/b\t0.496s", "=== FAIL: b TestRace (0.00s)", "WARNING: DATA RACE",
				"testing.go:1865: race detected during execution of test", "DONE 5 tests, 4 failures in 0.857s",
				"ERROR rerun aborted because previous run had a data race"}},
		// orchestra-4wb.25 on 2026-10-04: every test passed and golangci-lint found another running.
		{"a tool's own error", "ok  \texample.com/m/cmd/orchestra\t53.540s\nError: parallel golangci-lint is running\n" +
			"The command is terminated due to an error: parallel golangci-lint is running\nexit status 3\n",
			[]string{"Error: parallel golangci-lint is running"}},
		{"lint", "internal/dispatch/advice.go:12:121: The line is 130 characters long, which exceeds the maximum " +
			"of 120 characters. (lll)\n\tfunc (o *Loop) reviewInput(…) string {\n\t^\n1 issues:\n* lll: 1\n",
			[]string{"internal/dispatch/advice.go:12:121: The line is 130 characters long, which exceeds the maximum " +
				"of 120 characters. (lll)", "1 issues:"}},
		{"build", "# example.com/m/a [example.com/m/a.test]\n./a_test.go:9:2: undefined: foo\n" +
			"FAIL\texample.com/m/a [build failed]\n",
			[]string{"./a_test.go:9:2: undefined: foo", "FAIL\texample.com/m/a [build failed]"}},
		{"timeout", "panic: test timed out after 10m0s\n\trunning tests:\n\t\tTestHang (10m0s)\n\t\tTestSlow (9m0s)\n\n" +
			"goroutine 1 [running]:\ntesting.(*M).startAlarm.func1()\n\t/go/src/testing/testing.go:2484 +0x30c\n" +
			"FAIL\texample.com/m/a\t600.1s\n",
			[]string{"panic: test timed out after 10m0s", "running tests:", "TestHang (10m0s)", "TestSlow (9m0s)",
				"FAIL\texample.com/m/a\t600.1s"}},
		{"check.sh's own", "FAIL: ./internal/dispatch failed outside its tests, in TestMain or an init\n",
			[]string{"FAIL: ./internal/dispatch failed outside its tests, in TestMain or an init"}},
		{"repeats", "FAIL\texample.com/m/a\t1s\nFAIL\texample.com/m/a\t1s\n", []string{"FAIL\texample.com/m/a\t1s"}},
		{"nothing said", "FAIL\nexit status 1\n    errors.go:3: a test's own message\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := failureLines(tc.out); !equal(got, tc.want) {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// What the reviewer is given of a failed check is bounded: so many lines, each so long, and so many
// directories.
func TestWhatAFailedCheckSaidIsBounded(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	for i := 1; i <= 2*maxSaid; i++ {
		fmt.Fprintf(&out, "--- FAIL: Test%d (0.00s)\n", i)
	}
	long := "FAIL\t" + strings.Repeat("x", 2*saidWidth)
	out.WriteString(long + "\n")
	said := failureLines(out.String())
	if len(said) != maxSaid || said[0] != "--- FAIL: Test1 (0.00s)" || said[maxSaid-1] != "(… and 32 more lines like these)" {
		t.Errorf("said:\n%s", strings.Join(said, "\n"))
	}
	if got := failureLines(long); len(got) != 1 || len([]rune(got[0])) != saidWidth || !strings.HasSuffix(got[0], "x…") {
		t.Errorf("a long line should be cut to %d runes: %q", saidWidth, got)
	}

	var dirs []string
	for i := range maxDirs + 3 {
		dirs = append(dirs, fmt.Sprintf("d%02d", i))
	}
	f := checkFail{br: "wt/A", onto: "0123456789abcdef", how: "fails", output: "check.log",
		said: []string{"screen line"}, saidEnd: true, dirs: append([]string{"."}, dirs...)}
	ev := f.evidence("A", "make check", "main")
	for _, want := range []string{
		"A: 'make check' fails on wt/A rebased onto main at 0123456789; its output is in check.log.\n",
		"No line of its output says what failed; its last lines:\n    screen line\n",
		"(0123456789..wt/A): the top level (.), d00, ",
		", d18 and 4 more\n",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("the evidence lacks %q:\n%s", want, ev)
		}
	}
}
