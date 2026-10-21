package project

import (
	"slices"
	"strings"
	"testing"
)

// environmentHeading opens the section of the breakdown's instructions on the environment setup
// ticket.
const environmentHeading = "## The environment first"

// environmentSection is the section of the instructions at p on the environment setup ticket, its
// words joined by single spaces; ok is false if they have none.
func environmentSection(t *testing.T, p string) (section string, ok bool) {
	t.Helper()
	_, rest, ok := strings.Cut(readText(t, p), environmentHeading)
	if !ok {
		return "", false
	}
	section, _, _ = strings.Cut(rest, " ## ")
	return section, true
}

// A spec's breakdown, headless or with the user, files one environment setup ticket first, for a
// spec that needs a tool the repository doesn't have as for a repository with no code: solo,
// priority 0, every other ticket blocked by it. Its worker writes the requires list (a check each,
// an install without sudo or a manual step), pins the versions in a committed file the checks read,
// installs nothing outside the list but runs orchestra requires, asks about what only a person can
// meet, scaffolds a project with no code, installs the dependencies, sets setup (and setup_files
// where needed), and says what to install in the docs. A spec that adds a tool to a repository with
// code gets it without the scaffold.
func TestSpecBreakdownFilesTheEnvironmentFirst(t *testing.T) {
	want := []string{
		"When the spec needs a tool that the repository doesn't have yet",
		"or the repository has no code yet",
		"one ticket for all of its basic setup, the environment included",
		"label `solo` and priority 0", "every other ticket waits for it",
		"The requires list: an entry in `\"requires\"` in `.orchestra/settings.json` for each tool, service " +
			"and permission that the tickets after it need",
		"Each has a `check`, a command that exits 0 when it is met at the version they need",
		"an `install` that meets it with a user-level package manager or the tool's own installer",
		"never with `sudo`", "a `manual` step instead",
		"pinned in the ecosystem's own file where it has one, committed",
		"`mise.toml`", "`rust-toolchain.toml`", "`.nvmrc`", "the `toolchain` line of `go.mod`",
		"and the entry's check checks them",
		"orchestra checks the list at every run's start, on every machine, and installs what is missing " +
			"with the entries' own commands, so the worker installs nothing outside the list",
		"Then `orchestra requires`, which checks the list and installs what its entries can",
		"A requirement still missing that only a person can meet is not worked around",
		"the worker asks the maintainer about it with a question",
		"so that the run waits for the answer rather than build on a guess",
		"Where the repository has no code yet, the scaffold",
		"the generators the stack starts from", "run with their non-interactive flags and at pinned versions",
		"their output committed", "a first test", "`check_fast`", "`check_full`",
		"`setup` in `.orchestra/settings.json` set to the command that installs them in a fresh worktree",
		"`npm ci`", "with `setup_files` set where orchestra's default list",
		"orchestra runs `setup` in each new worktree before its worker starts",
		"so no later ticket installs dependencies itself",
		"in its Build & Test section, and its README saying what to install and how, with the versions",
		"A spec that adds a tool to a repository with code", "gets the ticket all the same, without the scaffold",
	}
	// The headless breakdown files it with bd and has the tickets of its answer wait for it; one with
	// the user files it first and links every other ticket to it.
	const (
		inAnswer = "file it with bd right after the root epic, before you answer, so that the run can start on it " +
			"while orchestra files the rest, and list its ID in the `waits_for` of every ticket in your answer"
		filedFirst = "file it first, and give every other ticket a blocks link to it"
	)
	for name, tc := range map[string]struct {
		write func(repo string) (string, error)
		link  string
	}{
		"headless":                   {func(repo string) (string, error) { return WriteSpecPrompt(repo, false) }, inAnswer},
		"refinement in a pane":       {func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineSpec) }, filedFirst},
		"refinement on the terminal": {func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineSpec) }, filedFirst},
	} {
		write := tc.write
		want := append(slices.Clone(want), tc.link)
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			section, ok := environmentSection(t, p)
			if !ok {
				t.Fatalf("the instructions have no %q", environmentHeading)
			}
			for _, w := range want {
				if !strings.Contains(section, w) {
					t.Errorf("the environment section lacks %q", w)
				}
			}
			if strings.Contains(readText(t, p), "## A project with no code yet") {
				t.Error("the instructions still have the scaffold's own section")
			}
			for _, g := range []string{"The toolchain:", "installed where it is missing", "entries are added to those the list has"} {
				if strings.Contains(section, g) {
					t.Errorf("the environment section has %q", g)
				}
			}
		})
	}
}

// An update files the environment setup ticket first for a change that needs a new tool, or
// updates the tree's own while it is open, with the same requires list, added to the entries it has,
// pinning, question, setup and docs; never the scaffold, which the tree it updates was filed with.
func TestSpecUpdateFilesTheEnvironmentFirst(t *testing.T) {
	want := []string{
		"When the change needs a tool that the repository doesn't have yet",
		"the update's first ticket sets it up", "label `solo` and priority 0",
		"the update's other tickets wait for it",
		"file it first, and give each ticket the update files, and each open one you update to use the tool, a blocks link to it",
		"Where the tree's own environment setup ticket is still open, update that one for the tool instead",
		"The requires list: an entry in `\"requires\"` in `.orchestra/settings.json`",
		"The new tool's entries are added to those the list has already",
		"never with `sudo`", "a `manual` step instead", "pinned in the ecosystem's own file where it has one, committed",
		"Then `orchestra requires`", "the worker asks the maintainer about it with a question",
		"`setup` in `.orchestra/settings.json`", "`setup_files`",
		"orchestra runs `setup` in each new worktree before its worker starts",
		"its README saying what to install and how, with the versions",
		"A change that needs no new tool gets no such ticket",
	}
	gone := []string{"the scaffold", "generators", "`check_fast`", "or the repository has no code yet",
		"When the spec needs a tool", "installed where it is missing"}
	for name, write := range map[string]func(repo string) (string, error){
		"headless":                   func(repo string) (string, error) { return WriteSpecPrompt(repo, true) },
		"refinement in a pane":       func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineUpdate) },
		"refinement on the terminal": func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineUpdate) },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			section, ok := environmentSection(t, p)
			if !ok {
				t.Fatalf("the instructions have no %q", environmentHeading)
			}
			for _, w := range want {
				if !strings.Contains(section, w) {
					t.Errorf("the environment section lacks %q", w)
				}
			}
			for _, g := range gone {
				if strings.Contains(section, g) {
					t.Errorf("the environment section has %q", g)
				}
			}
		})
	}
}

// A feature's breakdown leaves the environment setup ticket out.
func TestFeatureBreakdownLeavesTheEnvironmentOut(t *testing.T) {
	for name, write := range map[string]func(repo string) (string, error){
		"in a pane":       func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineFeature) },
		"on the terminal": func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineFeature) },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			_, body, _ := strings.Cut(readText(t, p), "-->") // past the opening comment, which names every mode
			for _, g := range []string{environmentHeading, "never with `sudo`", "`setup_files`"} {
				if strings.Contains(body, g) {
					t.Errorf("the feature's instructions have %q", g)
				}
			}
		})
	}
}
