package project

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/noesis-sol/orchestra/skills"
)

// Tests is how stage 2 of init sets up a project's tests.
type Tests int

// The ways stage 2 can set up a project's tests.
const (
	TestsManual  Tests = iota // a check typed by hand, or given by flags: no test work filed
	TestsFound                // the suites the scout found, used as they are
	TestsScratch              // tests created from scratch, with the create-check-suite skill
)

// FilesTestWork reports whether the choice files test work for the workers, who then need the
// create-check-suite skill: tests from scratch, or the suites found with "Also file tickets for
// untested areas".
func (c Choice) FilesTestWork() bool {
	return c.Tests == TestsScratch || c.Tests == TestsFound && c.FileUntested
}

// SkillsDir is the folder, from the repository's top, where the workers' agent kind finds a
// project's skills: .claude/skills for claude (and "", the default kind), .agents/skills for codex.
// It is "" for a kind orchestra knows no folder for.
func SkillsDir(agent string) string {
	switch agent {
	case "", "claude":
		return ".claude/skills"
	case "codex":
		return ".agents/skills"
	}
	return ""
}

// SkillPlan is the create-check-suite skill as init would install it for the workers' agent.
type SkillPlan struct {
	Dir string // its folder from the repository's top; "" where the agent's skill folder is unknown
	// Exists is set when the folder is there, and Differs when it lacks one of the skill's files or
	// has one that differs from it. A file of the project's own in the folder isn't a difference.
	Exists, Differs bool
}

// PlanSkill says where init would install the create-check-suite skill for the workers' agent, and
// whether a copy is there and differs from it, for stage 2 to ask whether to keep or replace it.
func PlanSkill(repo, agent string) (SkillPlan, error) {
	dir := SkillsDir(agent)
	if dir == "" {
		return SkillPlan{}, nil
	}
	p := SkillPlan{Dir: path.Join(dir, skills.CreateCheckSuiteName)}
	fi, err := os.Stat(filepath.Join(repo, filepath.FromSlash(p.Dir)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
		return p, err
	}
	p.Exists = true
	if !fi.IsDir() {
		p.Differs = true
		return p, nil
	}
	skill := skills.CreateCheckSuite()
	err = fs.WalkDir(skill, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p.Differs {
			return err
		}
		want, err := fs.ReadFile(skill, name)
		if err != nil {
			return err
		}
		have, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path.Join(p.Dir, name))))
		switch {
		case errors.Is(err, os.ErrNotExist):
			p.Differs = true
		case err != nil:
			return err
		default:
			p.Differs = !bytes.Equal(have, want)
		}
		return nil
	})
	return p, err
}

// ApplySkill installs the create-check-suite skill in the project's skill folder for the workers'
// agent, to be committed, where the choice files test work. It keeps a copy that differs unless the
// choice replaces it, and says what it did; ok is false where the choice files no test work.
func ApplySkill(repo string, c Choice) (s Step, ok bool, err error) {
	if !c.FilesTestWork() {
		return Step{}, false, nil
	}
	p, err := PlanSkill(repo, c.Agent)
	if err != nil {
		return Step{}, true, err
	}
	const label = "skill"
	switch {
	case p.Dir == "":
		return Step{Kind: StepCaution, Label: label, Detail: "the workers' agent is " + c.Agent +
			", whose skill folder orchestra doesn't know: " + skills.CreateCheckSuiteName + " isn't installed"}, true, nil
	case p.Exists && !p.Differs:
		return Step{Kind: StepKept, Label: label, Detail: p.Dir + "/ is there; left as it is"}, true, nil
	case p.Differs && !c.ReplaceSkill:
		return Step{Kind: StepCaution, Label: label, Detail: p.Dir +
			"/ differs from orchestra's; kept (remove it and run orchestra init again for orchestra's)"}, true, nil
	}
	if err := writeSkill(filepath.Join(repo, filepath.FromSlash(p.Dir))); err != nil {
		return Step{}, true, err
	}
	detail := "wrote " + p.Dir + "/"
	if p.Exists {
		detail = "replaced " + p.Dir + "/"
	}
	detail += ", for the tickets that set up the project's tests"
	return Step{Kind: StepDone, Label: label, Detail: detail, Commit: []string{p.Dir + "/"}}, true, nil
}

// writeSkill writes the create-check-suite skill's files into dir, over the ones there; a file of
// the project's own there is left as it is.
func writeSkill(dir string) error {
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		if err := os.Remove(dir); err != nil { // a file where the folder goes: replacing it is what was chosen
			return err
		}
	}
	skill := skills.CreateCheckSuite()
	return fs.WalkDir(skill, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		to := filepath.Join(dir, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := fs.ReadFile(skill, name)
		if err != nil {
			return err
		}
		return writeFile(to, b, 0o644)
	})
}
