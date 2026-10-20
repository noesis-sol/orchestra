package dispatch

import (
	"strings"
	"testing"
)

// orchestra-nr1v.5 on 2026-10-19: a hand-back quoted only bun's "error: expect(received).toBe(expected)"
// of a failed check, as the lines that say what failed were picked by go test's words alone, and its
// worker had to search check.log for the test. The lines in which other test runners name a failed
// test, or count the failures, are among those quoted, in the order printed.
func TestFailureLinesNameOtherRunnersFailedTests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, out string
		want      []string
	}{
		{"bun", "bun test v1.1.30 (7996d06b)\n\nmath.test.ts:\n1 | import { expect, test } from \"bun:test\";\n" +
			"2 |\n3 | test(\"adds\", () => {\n4 |   expect(1 + 1).toBe(3);\n                      ^\n" +
			"error: expect(received).toBe(expected)\n\nExpected: 3\nReceived: 2\n\n" +
			"      at <anonymous> (/repo/math.test.ts:4:17)\n(fail) math > adds [0.42ms]\n(pass) math > subtracts [0.03ms]\n\n" +
			" 1 pass\n 1 fail\n 2 expect() calls\nRan 2 tests across 1 files. [12.00ms]\n",
			[]string{"error: expect(received).toBe(expected)", "(fail) math > adds [0.42ms]", "1 fail"}},
		{"bun in colour", "✓ math > subtracts [0.03ms]\n✗ math > adds [0.42ms]\n\n 1 pass\n 1 fail\n",
			[]string{"✗ math > adds [0.42ms]", "1 fail"}},
		{"jest", "FAIL src/sum.test.js\n  sum\n    ✓ adds 1 + 1 (2 ms)\n    ✕ adds 1 + 2 (3 ms)\n\n" +
			"  ● sum › adds 1 + 2\n\n    expect(received).toBe(expected) // Object.is equality\n\n" +
			"    Expected: 4\n    Received: 3\n\n      4 |   test('adds 1 + 2', () => {\n" +
			"    > 5 |     expect(sum(1, 2)).toBe(4);\n        |                       ^\n      6 |   });\n\n" +
			"      at Object.toBe (src/sum.test.js:5:23)\n\nTest Suites: 1 failed, 1 total\n" +
			"Tests:       1 failed, 1 passed, 2 total\nSnapshots:   0 total\nTime:        0.5 s\n",
			[]string{"FAIL src/sum.test.js", "✕ adds 1 + 2 (3 ms)", "● sum › adds 1 + 2", "Test Suites: 1 failed, 1 total",
				"Tests:       1 failed, 1 passed, 2 total"}},
		{"vitest", " ❯ src/sum.test.ts (2 tests | 1 failed) 4ms\n   × sum > adds 1 + 2 3ms\n" +
			"     → expected 3 to be 4 // Object.is equality\n\n⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯\n\n" +
			" FAIL  src/sum.test.ts > sum > adds 1 + 2\nAssertionError: expected 3 to be 4 // Object.is equality\n" +
			" ❯ src/sum.test.ts:5:22\n\n Test Files  1 failed (1)\n      Tests  1 failed | 1 passed (2)\n",
			[]string{"× sum > adds 1 + 2 3ms", "FAIL  src/sum.test.ts > sum > adds 1 + 2", "Test Files  1 failed (1)",
				"Tests  1 failed | 1 passed (2)"}},
		{"pytest", "============================= test session starts ==============================\n" +
			"platform darwin -- Python 3.12.4, pytest-8.3.2\ncollected 2 items\n\n" +
			"tests/test_math.py .F                                                    [100%]\n\n" +
			"=================================== FAILURES ===================================\n" +
			"___________________________________ test_add ___________________________________\n\n" +
			"    def test_add():\n>       assert 1 + 1 == 3\nE       assert (1 + 1) == 3\n\n" +
			"tests/test_math.py:5: AssertionError\n" +
			"=========================== short test summary info ============================\n" +
			"FAILED tests/test_math.py::test_add - assert (1 + 1) == 3\n" +
			"========================= 1 failed, 1 passed in 0.03s ==========================\n",
			[]string{"tests/test_math.py:5: AssertionError", "FAILED tests/test_math.py::test_add - assert (1 + 1) == 3",
				"========================= 1 failed, 1 passed in 0.03s =========================="}},
		{"pytest -v", "tests/test_math.py::test_add[1-2] FAILED                                 [ 50%]\n" +
			"tests/test_math.py::test_sub PASSED                                      [100%]\n" +
			"=================== 2 passed, 11 failed in 0.03s ===================\n",
			[]string{"tests/test_math.py::test_add[1-2] FAILED                                 [ 50%]",
				"=================== 2 passed, 11 failed in 0.03s ==================="}},
		{"cargo", "   Compiling m v0.1.0 (/repo)\n" +
			"    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.5s\n" +
			"     Running unittests src/lib.rs (target/debug/deps/m-abc)\n\nrunning 2 tests\n" +
			"test tests::adds ... ok\ntest tests::subtracts ... FAILED\n\nfailures:\n\n" +
			"---- tests::subtracts stdout ----\n\nthread 'tests::subtracts' panicked at src/lib.rs:12:9:\n" +
			"assertion `left == right` failed\n  left: 1\n right: 2\n" +
			"note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n\n\n" +
			"failures:\n    tests::subtracts\n\n" +
			"test result: FAILED. 1 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n\n" +
			"error: test failed, to rerun pass `--lib`\n",
			[]string{"test tests::subtracts ... FAILED", "thread 'tests::subtracts' panicked at src/lib.rs:12:9:",
				"test result: FAILED. 1 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s",
				"error: test failed, to rerun pass `--lib`"}},
		{"cargo nextest", "        PASS [   0.003s] m tests::adds\n        FAIL [   0.004s] m tests::subtracts\n",
			[]string{"FAIL [   0.004s] m tests::subtracts"}},
		{"mocha", "\n  Array\n    #indexOf()\n      ✔ returns the index\n      1) returns -1 when absent\n\n\n" +
			"  1 passing (6ms)\n  1 failing\n\n  1) Array\n       #indexOf()\n         returns -1 when absent:\n\n" +
			"      AssertionError [ERR_ASSERTION]: 0 == -1\n      + expected - actual\n\n" +
			"      at Context.<anonymous> (test/array.test.js:9:14)\n",
			[]string{"1) returns -1 when absent", "1 failing", "1) Array"}},
		{"node --test", "✔ adds (0.4ms)\n✖ subtracts (0.6ms)\n  AssertionError [ERR_ASSERTION]: Expected values\n" +
			"ℹ tests 2\nℹ pass 1\nℹ fail 1\n\n✖ failing tests:\n",
			[]string{"✖ subtracts (0.6ms)", "✖ failing tests:"}},
		{"TAP", "TAP version 13\n# Subtest: adds\nok 1 - adds\n  ---\n  duration_ms: 0.4\n  ...\n" +
			"# Subtest: subtracts\nnot ok 2 - subtracts\n  ---\n  duration_ms: 0.6\n  failureType: 'testCodeFailure'\n" +
			"  error: |-\n    Expected values to be strictly equal:\n  ...\n1..2\n# tests 2\n# pass 1\n# fail 1\n",
			[]string{"not ok 2 - subtracts"}},
		{"none failed", " 2 pass\n 0 fail\nTests:       2 passed, 2 total\n" +
			"========================= 0 failed, 2 passed in 0.03s ==========================\n" +
			"test result: ok. 2 passed; 0 failed\n  2 passing (6ms)\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := failureLines(tc.out); !equal(got, tc.want) {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}
