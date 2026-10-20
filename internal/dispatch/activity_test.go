package dispatch

import "testing"

func TestDoingOf(t *testing.T) {
	t.Parallel()
	bash := func(cmd string) ToolUse { return ToolUse{Event: "PreToolUse", Tool: "Bash", Command: cmd} }
	cases := []struct {
		u     ToolUse
		check string
		want  Doing
	}{
		{bash("xcrun simctl terminate X; cd /wt/k-1 && scripts/ci-local.sh 2>&1 | tail -25"), "scripts/ci-local.sh", DoingTesting},
		{bash("scripts/ci-local.sh lint ios"), "scripts/ci-local.sh", DoingTesting},
		{bash("go test ./..."), "", DoingTesting},
		{bash("xcodebuild -scheme Kinieta -destination 'platform=macOS' test"), "", DoingTesting},
		{bash("npm run test -- --watch=false"), "", DoingTesting},
		{bash("git status --short"), "scripts/ci-local.sh", ""},
		{bash("xcodebuild -scheme Kinieta build"), "", ""},
		{bash("cat latest.txt"), "", ""}, // "test" inside a word is not a test run
		{ToolUse{Event: "PreToolUse", Tool: "Edit"}, "", DoingEditing},
		{ToolUse{Event: "PreToolUse", Tool: "Grep"}, "", DoingReading},
		{ToolUse{Event: "PreToolUse", Tool: "Agent"}, "", DoingSubagent},
		{ToolUse{Event: EventPermission, Tool: "Bash", Command: "go test ./..."}, "", DoingTesting}, // allowed, running
		{ToolUse{Event: "PostToolUse"}, "", ""},
		{ToolUse{Event: "Stop"}, "", ""},
	}
	for _, c := range cases {
		if got := DoingOf(c.u, c.check); got != c.want {
			t.Errorf("DoingOf(%+v, %q) = %q, want %q", c.u, c.check, got, c.want)
		}
	}
}
