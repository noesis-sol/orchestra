package dispatch

import (
	"regexp"
	"strings"
)

// ToolUse is what a worker last reported through its hooks.
type ToolUse struct {
	Event   string // PreToolUse (using Tool now), PostToolUse (between tools) or Stop (turn over)
	Tool    string // Bash, Edit, Read, …
	Command string // Bash's command
}

// testRunner matches commands that run a test suite, for projects whose check command is not
// the one being run.
var testRunner = regexp.MustCompile(`(^|[\s;&|(])(go test|swift test|cargo test|py\.?test|jest|vitest|rspec|phpunit|mix test|dotnet test|ctest|tox|(npm|pnpm|yarn|bun)( run)? test|make (test|check)|gradlew? test|mvnw? test|xcodebuild\b.*\btest)\b`)

// Doing names what a working worker is doing from what it reported: testing (the project's check
// command or a test runner), editing or reading. "" means nothing more precise than Herdr's
// "working": thinking between tools, another command, or no report.
func Doing(u ToolUse, check string) string {
	if u.Event != "PreToolUse" {
		return ""
	}
	switch u.Tool {
	case "Bash":
		cmd := strings.TrimSpace(u.Command)
		if check != "" && strings.Contains(cmd, check) || testRunner.MatchString(cmd) {
			return "testing"
		}
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "editing"
	case "Read", "Grep", "Glob", "LS", "WebFetch", "WebSearch":
		return "reading"
	}
	return ""
}
