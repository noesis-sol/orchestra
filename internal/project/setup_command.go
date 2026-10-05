package project

import (
	"fmt"
	"path/filepath"
	"strings"
)

// setupLockfiles are the lockfiles init offers a setup command for, each with the command that installs
// what it pins, first in each ecosystem first: a repository with two of one ecosystem's lockfiles gets
// the first's command.
var setupLockfiles = []struct{ ecosystem, lockfile, command string }{
	{"js", "package-lock.json", "npm ci"},
	{"js", "pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
	{"js", "yarn.lock", "yarn install --frozen-lockfile"},
	{"python", "uv.lock", "uv sync"},
	{"python", "poetry.lock", "poetry install"},
	{"ruby", "Gemfile.lock", "bundle install"},
}

// FindSetup is the setup command init offers for the lockfiles at the repository's top, one command
// per ecosystem joined with &&, and the lockfiles it is for; "" and none when there is no lockfile.
func FindSetup(repo string) (command string, lockfiles []string) {
	var commands []string
	done := map[string]bool{}
	for _, l := range setupLockfiles {
		if done[l.ecosystem] || !fileExists(filepath.Join(repo, l.lockfile)) {
			continue
		}
		done[l.ecosystem] = true
		commands, lockfiles = append(commands, l.command), append(lockfiles, l.lockfile)
	}
	return strings.Join(commands, " && "), lockfiles
}

// SetupStep is the step for init's summary of the setup command; ok is false when there is nothing to
// say: no setup command, none before, and no lockfile to offer one for.
func SetupStep(c Choice) (s Step, ok bool) {
	const label = "setup"
	const when = " runs before check-fast on a ticket whose rebase changes a dependency file"
	found := "found " + joinAnd(c.SetupFrom)
	switch {
	case c.Setup != "" && c.Setup == c.SetupWas:
		return Step{Kind: StepKept, Label: label, Detail: "'" + c.Setup + "'" + when + " (" + SettingsName +
			" has it; --setup replaces it)"}, true
	case c.Setup != "":
		return Step{Kind: StepDone, Label: label, Detail: "'" + c.Setup + "'" + when}, true
	case c.SetupUnasked:
		return Step{Kind: StepCaution, Label: label, Detail: fmt.Sprintf("not asked (no terminal): %s, and a "+
			"ticket rebased over a dependency change is checked against the dependencies installed before it; "+
			"--setup '%s' sets the command that installs them", found, c.SetupOffer)}, true
	case c.SetupWas != "":
		return Step{Kind: StepDone, Label: label, Detail: "removed '" + c.SetupWas + "': no setup command runs"}, true
	case c.SetupOffer != "":
		return Step{Kind: StepKept, Label: label, Detail: found + "; no setup command: a ticket rebased over a " +
			"dependency change is checked against the dependencies installed before it (--setup sets one)"}, true
	}
	return Step{}, false
}
