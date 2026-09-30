package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// gitRepo makes a repository with one commit and returns its path and a git runner.
func gitRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := command.Output(dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git(repo, "init", "-q")
	git(repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo, git
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitWritesTheTemplateAndIgnoresOrchestrasFiles(t *testing.T) {
	repo, git := gitRepo(t)
	steps, err := Init(repo, "make check", false)
	if err != nil {
		t.Fatal(err)
	}
	prompt := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md"))
	if !strings.Contains(prompt, "`make check`") || strings.Contains(prompt, "<check command>") ||
		strings.Contains(prompt, "<What it runs") || !strings.Contains(prompt, "TICKET_ID") {
		t.Errorf("prompt not filled in:\n%s", prompt)
	}
	if got := read(t, filepath.Join(repo, ".orchestra", ".gitignore")); got != orchGitignore {
		t.Errorf(".gitignore = %q", got)
	}
	// The log, reports and run files are ignored; the prompt and .gitignore are not.
	for _, f := range []string{"orchestra.log", "reports/r.md", "run/prompt.md"} {
		p := filepath.Join(repo, ".orchestra", f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	status := git(repo, "status", "--porcelain", "--untracked-files=all")
	if strings.Contains(status, "orchestra.log") || strings.Contains(status, "reports/") || strings.Contains(status, "run/") {
		t.Errorf("ignored files show in git status:\n%s", status)
	}
	if !strings.Contains(status, ".orchestra/worker-prompt.md") || !strings.Contains(status, ".orchestra/.gitignore") {
		t.Errorf("the prompt and .gitignore should be committable:\n%s", status)
	}
	if len(steps) == 0 {
		t.Error("init should say what it did")
	}

	// A second run leaves the prompt alone; -force replaces it.
	os.WriteFile(filepath.Join(repo, ".orchestra", "worker-prompt.md"), []byte("mine TICKET_ID"), 0o644)
	Init(repo, "", false)
	if got := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md")); got != "mine TICKET_ID" {
		t.Errorf("init overwrote an existing prompt: %q", got)
	}
	Init(repo, "", true)
	if got := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md")); got != promptTemplate {
		t.Error("-force should restore the template")
	}
}

func TestInitMovesALegacyPromptAndFixesTheOldExclude(t *testing.T) {
	repo, git := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, ".claude"), 0o755)
	os.WriteFile(filepath.Join(repo, legacyPrompt), []byte("legacy TICKET_ID"), 0o644)
	git(repo, "add", legacyPrompt)
	git(repo, "commit", "-q", "-m", "prompt")
	// Earlier versions excluded all of .orchestra/, which would hide the committed prompt.
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	os.WriteFile(exclude, []byte("# mine\n*.tmp\n# worker prompts written by orchestra\n/.orchestra/\n"), 0o644)

	if Layout := Locate(repo); !Layout.Legacy {
		t.Error("before init the legacy Layout should be used")
	}
	steps, err := Init(repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(steps[0].Detail, "moved") {
		t.Errorf("steps = %+v", steps)
	}
	if got := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md")); got != "legacy TICKET_ID" {
		t.Errorf("moved prompt = %q", got)
	}
	status := git(repo, "status", "--porcelain")
	if !strings.Contains(status, "R  .claude/worker-prompt.md -> .orchestra/worker-prompt.md") {
		t.Errorf("the move should be staged as a rename:\n%s", status)
	}
	ex := read(t, exclude)
	if strings.Contains(ex, "\n/.orchestra/\n") || strings.Count(ex, runExcludeEntry) != 1 || !strings.Contains(ex, "*.tmp") {
		t.Errorf("exclude = %q", ex)
	}
	if Layout := Locate(repo); Layout.Legacy || !strings.HasSuffix(Layout.Log, ".orchestra/orchestra.log") {
		t.Errorf("after init the .orchestra Layout should be used: %+v", Layout)
	}
}

func TestLaunchPromptIsIgnoredInAWorktreeCutBeforeInit(t *testing.T) {
	repo, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", "-b", "wt/k-1", wt)
	if err := EnsureRunExcluded(repo); err != nil {
		t.Fatal(err)
	}
	EnsureRunExcluded(repo) // idempotent
	line, err := WriteLaunchPrompt(wt, "k-1", "You are responsible for k-1.\n- Run `ls`.\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(line, "\n") || !strings.Contains(line, ".orchestra/run/prompt.md") {
		t.Errorf("launch instruction = %q", line)
	}
	if s := git(wt, "status", "--porcelain", "--untracked-files=all"); s != "" {
		t.Errorf("the launch prompt shows in git status: %q", s)
	}
	if n := strings.Count(read(t, filepath.Join(repo, ".git", "info", "exclude")), runExcludeEntry); n != 1 {
		t.Errorf("exclude has %d run entries", n)
	}
	// An ignored file doesn't stop the worktree from being removed after a merge.
	git(repo, "worktree", "remove", wt)
}

func TestConcurrencyPrecedence(t *testing.T) {
	cases := []struct {
		flag, setting, want int
		fails               bool
	}{
		{0, 0, 1, false}, {0, 3, 3, false}, {2, 3, 2, false}, {17, 0, 0, true}, {-1, 0, 0, true},
	}
	for _, c := range cases {
		got, err := ResolveConcurrency(c.flag, Settings{Concurrency: c.setting})
		if (err != nil) != c.fails || (!c.fails && got != c.want) {
			t.Errorf("resolveConcurrency(%d, %d) = %d, %v", c.flag, c.setting, got, err)
		}
	}
}

func TestDetectCheckAndDefaultChoice(t *testing.T) {
	kinieta := "- Check your work with `scripts/ci-local.sh`. It runs the CI jobs locally"
	if got := DetectCheck(kinieta); got != "scripts/ci-local.sh" {
		t.Errorf("detectCheck = %q", got)
	}
	if got := DetectCheck(promptTemplate); got != "" {
		t.Errorf("the template's placeholder is not a check command: %q", got)
	}
	c := DefaultChoice(Settings{}, kinieta)
	if c.Check != "scripts/ci-local.sh" || c.CheckFrom != "found in the worker prompt" || c.Concurrent != 1 || !c.Unasked {
		t.Errorf("from the prompt: %+v", c)
	}
	c = DefaultChoice(Settings{Check: "make check", Concurrency: 3}, kinieta)
	if c.Check != "make check" || c.Concurrent != 3 || c.Unasked {
		t.Errorf("settings win: %+v", c)
	}
}

func TestApplySettingsSavesAndExplains(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755)
	st, err := ApplySettings(repo, Choice{Check: "make check", Concurrent: 3})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(SettingsPath(repo))
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["concurrent"] != float64(3) || m["check"] != "make check" {
		t.Errorf("settings.json = %s", raw)
	}
	if st.Kind != StepCaution || !strings.Contains(st.Detail, "side by side") {
		t.Errorf("more than 1 should come with a caution: %+v", st)
	}
	st, _ = ApplySettings(repo, Choice{Concurrent: 1, Unasked: true})
	if st.Kind != StepCaution || !strings.Contains(st.Detail, "merges unchecked") || !strings.Contains(st.Detail, "not asked") {
		t.Errorf("no check, not asked: %+v", st)
	}
	st, _ = ApplySettings(repo, Choice{Check: "make check", Concurrent: 1})
	if st.Kind != StepDone {
		t.Errorf("one at a time with a check is plain done: %+v", st)
	}
}

func TestNextStepsOnlyListWhatIsLeft(t *testing.T) {
	repo, git := gitRepo(t)
	steps, _ := Init(repo, "make check", false)
	next := NextSteps(repo, steps, Prerequisites(repo))
	joined := strings.Join(next, "\n")
	if !strings.Contains(joined, "Commit .orchestra/") || !strings.Contains(joined, "orchestra") {
		t.Errorf("fresh init: %q", next)
	}
	if strings.Contains(joined, "placeholders") {
		t.Errorf("--check filled the placeholders: %q", next)
	}
	git(repo, "add", ".orchestra")
	git(repo, "commit", "-q", "-m", "setup")
	steps, _ = Init(repo, "", false)
	if joined := strings.Join(NextSteps(repo, steps, nil), "\n"); strings.Contains(joined, "Commit") || strings.Contains(joined, "Read ") {
		t.Errorf("nothing to commit or read on a second run: %q", joined)
	}
}
