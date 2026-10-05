package project

import (
	"path"
	"slices"

	"github.com/noesis-sol/orchestra/skills"
)

// TestWorkLabel marks the tickets init files for a project's tests, so that init run again finds
// those still open and doesn't file them twice. Their subtickets inherit it from the epic.
const TestWorkLabel = "orchestra-tests"

// soloLabel is dispatch.SoloLabel, which project can't import: a ticket that runs alone.
const soloLabel = "solo"

// TestTicket is a ticket init files for the project's tests (see TestWork).
type TestTicket struct {
	Title, Description, Acceptance string
	Type                           string // bd's issue type: epic or task
	Priority                       int
	Labels                         []string
	// Parent is the index in the plan of the epic it belongs to, or -1; BlockedBy are the indexes of
	// the tickets it waits for, which come before it.
	Parent    int
	BlockedBy []int
}

// Solo reports whether the ticket runs alone.
func (t TestTicket) Solo() bool {
	return slices.Contains(t.Labels, soloLabel)
}

// The titles of the tickets TestWork plans.
const (
	TestsEpicTitle    = "The project's tests"
	SetUpTestsTitle   = "Set up the test harness and a first suite"
	MapUntestedTitle  = "Map the untested areas"
	passRunnersPrefix = "Make " + FastRunner + " and " + FullRunner + " pass on "
)

// PassRunnersTitle is the title of the ticket that gets the runners passing on base.
func PassRunnersTitle(base string) string { return passRunnersPrefix + base }

// TestWork is the work the choice files for the project's tests, in the order to file it, an epic
// before its tickets: with tests from scratch, an epic and the solo P1 ticket that sets them up;
// with the suites found, the solo P1 ticket that gets the runners passing on base, under an epic
// with the ticket that maps the untested areas after it where FileUntested is set. A manual check
// files nothing. Every ticket carries TestWorkLabel.
func TestWork(c Choice, base string) []TestTicket {
	skill := "the " + skills.CreateCheckSuiteName + " skill"
	if dir := SkillsDir(c.Agent); dir != "" {
		skill += " (" + path.Join(dir, skills.CreateCheckSuiteName, "SKILL.md") + ")"
	}
	epic := TestTicket{Title: TestsEpicTitle, Type: "epic", Priority: 2, Labels: []string{TestWorkLabel}, Parent: -1,
		Description: "The tests orchestra's checks run: " + FastRunner + " on every merge, " + FullRunner +
			" once at the end of a run. orchestra init filed it with the tickets that set them up; " +
			"each untested area gets a P3 ticket here."}
	runners := FastRunner + " and " + FullRunner
	switch c.Tests {
	case TestsScratch:
		return []TestTicket{epic, {Title: SetUpTestsTitle, Type: "task", Priority: 1, Parent: 0,
			Labels: []string{TestWorkLabel, soloLabel},
			Description: "orchestra init wrote " + runners + " checking nothing (exit 0): until this ticket " +
				"is done, nothing merged is checked. It runs first and alone (P1, solo), so nothing merges " +
				"meanwhile.\n\nUse " + skill + ", every section:\n" +
				"- set up a harness and smoke tests in the project's own frameworks, added to the runners;\n" +
				"- get both runners passing on " + base + ";\n" +
				"- map the features in FEATURES.md;\n" +
				"- file one P3 ticket per untested area, under this ticket's epic.",
			Acceptance: runners + " run the new suites and exit 0 on " + base + "; FEATURES.md maps the " +
				"features; each untested area has a P3 ticket under the epic."}}
	case TestsFound:
		pass := TestTicket{Title: PassRunnersTitle(base), Type: "task", Priority: 1, Parent: -1,
			Labels: []string{TestWorkLabel, soloLabel},
			Description: "orchestra init wrote " + runners + " from the suites the project has. Every merge " +
				"runs " + FastRunner + ", so it runs first and alone (P1, solo).\n\n" +
				"Run them, check-fast first, and fix what fails. A failing test whose fix isn't trivial and " +
				"plainly right is skipped with its framework's own skip, naming in the skip's reason a bug " +
				"ticket filed for it. Run each runner twice more, to catch a test that passes only " +
				"sometimes, and record how long each takes in this ticket's notes.",
			Acceptance: runners + " exit 0 on " + base + ", three times running; how long each takes is " +
				"in the notes."}
		if !c.FileUntested {
			return []TestTicket{pass}
		}
		pass.Parent = 0
		return []TestTicket{epic, pass, {Title: MapUntestedTitle, Type: "task", Priority: 2, Parent: 0,
			BlockedBy: []int{1}, Labels: []string{TestWorkLabel},
			Description: "Use " + skill + " to map the project's features and what has no test: read the " +
				"repository (section 1), write FEATURES.md (section 5) and file one P3 ticket per untested " +
				"area under this ticket's epic (section 6). It waits for \"" + pass.Title + "\", so the " +
				"map starts from runners that pass.",
			Acceptance: "FEATURES.md maps the features, with the suites and how long each runner takes; " +
				"each untested area has a P3 ticket under the epic."}}
	}
	return nil
}
