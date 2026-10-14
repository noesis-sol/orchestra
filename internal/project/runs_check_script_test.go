package project

import "testing"

// scripts/check.sh is found however a suite's command runs it, and another directory's script isn't.
func TestRunsCheckScript(t *testing.T) {
	for command, want := range map[string]bool{
		"scripts/check.sh":                       true,
		"./scripts/check.sh":                     true,
		"sh scripts/check.sh":                    true,
		"bash ./scripts/check.sh -v":             true,
		"$PWD/scripts/check.sh":                  true,
		"${PWD}/scripts/check.sh":                true,
		"$(pwd)/scripts/check.sh":                true,
		`bash -c "scripts/check.sh"`:             true,
		"sh -c 'scripts/check.sh'":               true,
		"scripts/check.sh && make lint":          true,
		"make build;scripts/check.sh":            true,
		"(cd . && ./scripts/check.sh)":           true,
		"/repo/scripts/check.sh":                 true,
		"other/scripts/check.sh":                 false,
		"./other/scripts/check.sh":               false,
		"scripts/check.sh.bak":                   false,
		`bash -c "scripts/check.sh.bak && lint"`: false,
		FastRunner:                               false,
		"npm test":                               false,
		"":                                       false,
	} {
		if got := RunsCheckScript(command); got != want {
			t.Errorf("RunsCheckScript(%q) = %v, want %v", command, got, want)
		}
	}
}
