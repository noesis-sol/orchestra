package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo makes a repository with one commit and returns its path and a git runner.
func gitRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := run(dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
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
	lines, err := initProject(repo, "make check", false)
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
	if len(lines) == 0 {
		t.Error("init should say what it did")
	}

	// A second run leaves the prompt alone; -force replaces it.
	os.WriteFile(filepath.Join(repo, ".orchestra", "worker-prompt.md"), []byte("mine TICKET_ID"), 0o644)
	initProject(repo, "", false)
	if got := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md")); got != "mine TICKET_ID" {
		t.Errorf("init overwrote an existing prompt: %q", got)
	}
	initProject(repo, "", true)
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

	if layout := projectLayout(repo); !layout.Legacy {
		t.Error("before init the legacy layout should be used")
	}
	lines, err := initProject(repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "moved") {
		t.Errorf("lines = %q", lines)
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
	if layout := projectLayout(repo); layout.Legacy || !strings.HasSuffix(layout.Log, ".orchestra/orchestra.log") {
		t.Errorf("after init the .orchestra layout should be used: %+v", layout)
	}
}

func TestLaunchPromptIsIgnoredInAWorktreeCutBeforeInit(t *testing.T) {
	repo, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", "-b", "wt/k-1", wt)
	if err := ensureRunExcluded(repo); err != nil {
		t.Fatal(err)
	}
	ensureRunExcluded(repo) // idempotent
	line, err := writeLaunchPrompt(wt, "k-1", "You are responsible for k-1.\n- Run `ls`.\n")
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
