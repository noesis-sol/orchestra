package dispatch

import "testing"

func TestDoing(t *testing.T) {
	bash := func(cmd string) ToolUse { return ToolUse{Event: "PreToolUse", Tool: "Bash", Command: cmd} }
	cases := []struct {
		u     ToolUse
		check string
		want  string
	}{
		{bash("xcrun simctl terminate X; cd /wt/k-1 && scripts/ci-local.sh 2>&1 | tail -25"), "scripts/ci-local.sh", "testing"},
		{bash("scripts/ci-local.sh lint ios"), "scripts/ci-local.sh", "testing"},
		{bash("go test ./..."), "", "testing"},
		{bash("xcodebuild -scheme Kinieta -destination 'platform=macOS' test"), "", "testing"},
		{bash("npm run test -- --watch=false"), "", "testing"},
		{bash("git status --short"), "scripts/ci-local.sh", ""},
		{bash("xcodebuild -scheme Kinieta build"), "", ""},
		{bash("cat latest.txt"), "", ""}, // "test" inside a word is not a test run
		{ToolUse{Event: "PreToolUse", Tool: "Edit", Path: "a.swift"}, "", "editing"},
		{ToolUse{Event: "PreToolUse", Tool: "Grep"}, "", "reading"},
		{ToolUse{Event: "PreToolUse", Tool: "Agent"}, "", ""},
		{ToolUse{Event: "PostToolUse"}, "", ""},
		{ToolUse{Event: "Stop"}, "", ""},
	}
	for _, c := range cases {
		if got := Doing(c.u, c.check); got != c.want {
			t.Errorf("Doing(%+v, %q) = %q, want %q", c.u, c.check, got, c.want)
		}
	}
}
