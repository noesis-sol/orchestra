package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/skills"
)

// skillText is the create-check-suite skill's SKILL.md as orchestra installs it.
func skillText(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(skills.CreateCheckSuite(), "SKILL.md")
	if err != nil || !strings.Contains(string(b), "name: create-check-suite") {
		t.Fatalf("embedded SKILL.md: %v\n%s", err, b)
	}
	return string(b)
}

func TestApplySkillInstallsTheSkillOnlyForTestWork(t *testing.T) {
	want := skillText(t)
	for _, tc := range []struct {
		name         string
		tests        Tests
		fileUntested bool
		agent        string
		dir          string // where the skill lands; "" for nowhere
	}{
		{"from scratch, claude", TestsScratch, false, "claude", ".claude/skills/create-check-suite"},
		{"from scratch, the default agent", TestsScratch, false, "", ".claude/skills/create-check-suite"},
		{"from scratch, codex", TestsScratch, false, "codex", ".agents/skills/create-check-suite"},
		{"found, with untested areas, claude", TestsFound, true, "claude", ".claude/skills/create-check-suite"},
		{"found, with untested areas, codex", TestsFound, true, "codex", ".agents/skills/create-check-suite"},
		{"found as they are", TestsFound, false, "claude", ""},
		{"manual", TestsManual, false, "claude", ""},
		{"manual, with untested areas ticked before switching", TestsManual, true, "codex", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			c := Choice{Tests: tc.tests, FileUntested: tc.fileUntested, Agent: tc.agent}
			s, ok, err := ApplySkill(repo, c)
			if err != nil {
				t.Fatal(err)
			}
			if tc.dir == "" {
				if ok || s.Label != "" {
					t.Errorf("ApplySkill = %+v, %v; want no step", s, ok)
				}
				for _, d := range []string{".claude", ".agents"} {
					if _, err := os.Stat(filepath.Join(repo, d)); err == nil {
						t.Errorf("%s written for a choice that files no test work", d)
					}
				}
				return
			}
			if got := read(t, filepath.Join(repo, filepath.FromSlash(tc.dir), "SKILL.md")); got != want {
				t.Errorf("%s/SKILL.md differs from the embedded skill", tc.dir)
			}
			if !ok || s.Kind != StepDone || s.Detail != "wrote "+tc.dir+"/, for the tickets that set up the project's tests" ||
				len(s.Commit) != 1 || s.Commit[0] != tc.dir+"/" {
				t.Errorf("ApplySkill = %+v, %v", s, ok)
			}
			other := ".agents"
			if strings.HasPrefix(tc.dir, ".agents") {
				other = ".claude"
			}
			if _, err := os.Stat(filepath.Join(repo, other)); err == nil {
				t.Errorf("%s written as well as %s", other, tc.dir)
			}
		})
	}
}

func TestApplySkillKeepsOrReplacesACopyThatDiffers(t *testing.T) {
	want := skillText(t)
	const dir = ".claude/skills/create-check-suite"
	for _, tc := range []struct {
		name    string
		replace bool
	}{{"kept", false}, {"replaced", true}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			at := filepath.Join(repo, filepath.FromSlash(dir))
			if err := os.MkdirAll(at, 0o755); err != nil {
				t.Fatal(err)
			}
			const ours = "---\nname: create-check-suite\n---\nthe project's own\n"
			for name, text := range map[string]string{"SKILL.md": ours, "notes.md": "ours too\n"} {
				if err := os.WriteFile(filepath.Join(at, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p, err := PlanSkill(repo, "claude")
			if err != nil || p != (SkillPlan{Dir: dir, Exists: true, Differs: true}) {
				t.Fatalf("PlanSkill = %+v, %v", p, err)
			}
			s, ok, err := ApplySkill(repo, Choice{Tests: TestsScratch, Agent: "claude", ReplaceSkill: tc.replace})
			if err != nil || !ok {
				t.Fatalf("ApplySkill: %v, %v", ok, err)
			}
			got := read(t, filepath.Join(at, "SKILL.md"))
			if tc.replace {
				if got != want || s.Kind != StepDone || !strings.HasPrefix(s.Detail, "replaced "+dir+"/") {
					t.Errorf("replaced: step %+v, SKILL.md is orchestra's: %v", s, got == want)
				}
			} else if got != ours || s.Kind != StepCaution || !strings.Contains(s.Detail, dir+"/ differs") ||
				len(s.Commit) != 0 {
				t.Errorf("kept: step %+v, SKILL.md:\n%s", s, got)
			}
			if got := read(t, filepath.Join(at, "notes.md")); got != "ours too\n" {
				t.Errorf("the project's own file in the skill folder became %q", got)
			}
		})
	}
}

func TestApplySkillLeavesTheSameCopyAndNamesAnUnknownAgent(t *testing.T) {
	repo := t.TempDir()
	c := Choice{Tests: TestsFound, FileUntested: true, Agent: "codex"}
	if _, _, err := ApplySkill(repo, c); err != nil {
		t.Fatal(err)
	}
	if p, err := PlanSkill(repo, "codex"); err != nil || !p.Exists || p.Differs {
		t.Errorf("PlanSkill after installing = %+v, %v", p, err)
	}
	s, ok, err := ApplySkill(repo, c)
	if err != nil || !ok || s.Kind != StepKept || s.Detail != ".agents/skills/create-check-suite/ is there; left as it is" {
		t.Errorf("again: %+v, %v, %v", s, ok, err)
	}

	// A copy missing one of the skill's files differs.
	if err := os.Remove(filepath.Join(repo, ".agents", "skills", "create-check-suite", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if p, err := PlanSkill(repo, "codex"); err != nil || !p.Differs {
		t.Errorf("PlanSkill without SKILL.md = %+v, %v", p, err)
	}

	repo = t.TempDir()
	s, ok, err = ApplySkill(repo, Choice{Tests: TestsScratch, Agent: "aider"})
	if err != nil || !ok || s.Kind != StepCaution || !strings.Contains(s.Detail, "the workers' agent is aider") {
		t.Errorf("an unknown agent: %+v, %v, %v", s, ok, err)
	}
	if entries, _ := os.ReadDir(repo); len(entries) != 0 {
		t.Errorf("wrote %v for an unknown agent", entries)
	}
}
