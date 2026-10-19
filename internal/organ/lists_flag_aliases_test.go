package organ

import "testing"

// claudeAliasHelp is Claude Code 2.1.303's --help around an option it lists by two names, and a
// line of a description that starts with words and commas.
const claudeAliasHelp = `Options:
  --disable-slash-commands              Disable all skills
  --disallowedTools, --disallowed-tools <tools...>
      Comma or space-separated list of tool names to deny (e.g. "Bash(git *)
      Edit")
                                        selection, built-in tools and plugins,
`

// An option listed by two names is listed by each; words of a description aren't options.
func TestListsFlagByEitherName(t *testing.T) {
	for flag, want := range map[string]bool{
		"--disallowedTools": true, "--disallowed-tools": true, "--disable-slash-commands": true,
		"--tools": false, "Comma": false, "built-in": false, "Edit\")": false,
	} {
		if got := ListsFlag(claudeAliasHelp, flag); got != want {
			t.Errorf("ListsFlag(claudeAliasHelp, %q) = %v, want %v", flag, got, want)
		}
	}
}
