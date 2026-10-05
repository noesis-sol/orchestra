package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// gitRepo makes a repository with one commit and returns its path and a git runner.
func gitRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := command.Output(context.Background(), 0, dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
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
	steps, err := Init(context.Background(), repo, "make check", false)
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
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	if err := os.WriteFile(filepath.Join(repo, ".orchestra", "worker-prompt.md"), []byte("mine TICKET_ID"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(context.Background(), repo, "", false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md")); got != "mine TICKET_ID" {
		t.Errorf("init overwrote an existing prompt: %q", got)
	}
	if _, err := Init(context.Background(), repo, "", true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(repo, ".orchestra", "worker-prompt.md")); got != promptTemplate {
		t.Error("-force should restore the template")
	}
}

func TestInitMovesALegacyPromptAndFixesTheOldExclude(t *testing.T) {
	repo, git := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, legacyPrompt), []byte("legacy TICKET_ID"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", legacyPrompt)
	git(repo, "commit", "-q", "-m", "prompt")
	// Earlier versions excluded all of .orchestra/, which would hide the committed prompt.
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# mine\n*.tmp\n# worker prompts written by orchestra\n/.orchestra/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if Layout := Locate(repo); !Layout.Legacy {
		t.Error("before init the legacy Layout should be used")
	}
	steps, err := Init(context.Background(), repo, "", false)
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
	if err := EnsureRunExcluded(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRunExcluded(context.Background(), repo); err != nil { // idempotent
		t.Fatal(err)
	}
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

func TestTicketLimitPrecedence(t *testing.T) {
	cases := []struct {
		flag    time.Duration
		given   bool
		setting string
		want    time.Duration
		fails   bool
	}{
		{0, false, "", 0, false}, {0, false, "2h", 2 * time.Hour, false}, {0, false, "0", 0, false},
		{time.Hour, true, "2h", time.Hour, false}, {0, true, "2h", 0, false},
		{-time.Hour, true, "", 0, true}, {0, false, "two hours", 0, true}, {0, false, "-1h", 0, true},
	}
	for _, c := range cases {
		got, err := ResolveTicketLimit(c.flag, c.given, Settings{TicketLimit: c.setting})
		if (err != nil) != c.fails || (!c.fails && got != c.want) {
			t.Errorf("ResolveTicketLimit(%s, %v, %q) = %s, %v", c.flag, c.given, c.setting, got, err)
		}
	}
}

func TestCheckTimeoutPrecedence(t *testing.T) {
	cases := []struct {
		flag    time.Duration
		given   bool
		setting string
		want    time.Duration
		fails   bool
	}{
		{0, false, "", 30 * time.Minute, false}, {0, false, "5m", 5 * time.Minute, false},
		{45 * time.Minute, true, "5m", 45 * time.Minute, false},
		{0, true, "5m", 0, true}, {-time.Minute, true, "", 0, true},
		{0, false, "0", 0, true}, {0, false, "-5m", 0, true}, {0, false, "five minutes", 0, true},
	}
	for _, c := range cases {
		got, err := ResolveCheckTimeout(c.flag, c.given, Settings{CheckFastTimeout: c.setting})
		if (err != nil) != c.fails || (!c.fails && got != c.want) {
			t.Errorf("ResolveCheckTimeout(%s, %v, %q) = %s, %v", c.flag, c.given, c.setting, got, err)
		}
	}
	if d, err := ParseCheckTimeout(DefaultCheckTimeoutText); err != nil || d != DefaultCheckTimeout {
		t.Errorf("DefaultCheckTimeoutText = %q, DefaultCheckTimeout = %s", DefaultCheckTimeoutText, DefaultCheckTimeout)
	}
}

func TestExcludeTypes(t *testing.T) {
	list := func(types ...string) *[]string { return &types }
	cases := []struct {
		setting *[]string
		want    string
		fails   bool
	}{
		{nil, "epic", false}, {list(), "", false}, {list("epic", "decision"), "epic decision", false},
		{list(""), "", true}, {list("epic,decision"), "", true}, {list("a b"), "", true},
	}
	for _, c := range cases {
		got, err := ResolveExcludeTypes(Settings{ExcludeTypes: c.setting})
		if (err != nil) != c.fails || (!c.fails && strings.Join(got, " ") != c.want) {
			t.Errorf("ResolveExcludeTypes(%v) = %q, %v", c.setting, got, err)
		}
	}
	// The default is copied, so a run can't change it.
	got, _ := ResolveExcludeTypes(Settings{})
	got[0] = "task"
	if DefaultExcludeTypes[0] != "epic" {
		t.Error("the default changed")
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
	if c.FastCommand() != "scripts/ci-local.sh" || c.CheckFrom != "found in the worker prompt" || c.Concurrent != 1 || !c.Unasked {
		t.Errorf("from the prompt: %+v", c)
	}
	c = DefaultChoice(Settings{CheckFast: "make check", Concurrency: 3}, kinieta)
	if c.FastCommand() != "make check" || c.Concurrent != 3 || c.Unasked {
		t.Errorf("settings win: %+v", c)
	}
	// A concurrency every run would reject is replaced, and the summary says so.
	for _, n := range []int{-1, MaxConcurrency + 4} {
		c = DefaultChoice(Settings{CheckFast: "make check", Concurrency: n}, "")
		if c.Concurrent != 1 || !c.Unasked || c.Replaced != n {
			t.Errorf("concurrent %d: %+v", n, c)
		}
	}
	// So is a check time limit every run would reject; a valid one is kept.
	if c = DefaultChoice(Settings{CheckFastTimeout: "45m"}, ""); c.CheckFastTimeout != "45m" || c.ReplacedTimeout != "" {
		t.Errorf("check timeout 45m: %+v", c)
	}
	if c = DefaultChoice(Settings{CheckFastTimeout: "soon"}, ""); c.CheckFastTimeout != "" || c.ReplacedTimeout != "soon" {
		t.Errorf("check timeout soon: %+v", c)
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, _ := ApplySettings(repo, DefaultChoice(Settings{CheckFast: "make check", Concurrency: 20}, ""))
	if st.Kind != StepCaution || !strings.Contains(st.Detail, "settings had 20") {
		t.Errorf("replaced concurrency: %+v", st)
	}
	st, _ = ApplySettings(repo, DefaultChoice(Settings{CheckFast: "make check", Concurrency: 1, CheckFastTimeout: "0"}, ""))
	if st.Kind != StepCaution || !strings.Contains(st.Detail, "check_fast_timeout '0'") || !strings.Contains(st.Detail, "stopped after 30m") {
		t.Errorf("replaced check timeout: %+v", st)
	}
	if s, _, _ := LoadSettings(repo); s.CheckFastTimeout != "" {
		t.Errorf("the invalid check_timeout was kept: %+v", s)
	}
}

func TestApplySettingsSavesAndExplains(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := ApplySettings(repo, Choice{Fast: RunnerSuites("make check", "", FastRunner), CheckFastTimeout: "5m", Concurrent: 3})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(SettingsPath(repo))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["concurrent"] != float64(3) || m["check_fast"] != FastRunner || m["check_full"] != FullRunner ||
		m["check_fast_timeout"] != "5m" {
		t.Errorf("settings.json = %s", raw)
	}
	if !strings.Contains(st.Detail, "check-fast: make check, stopped after 5m") ||
		!strings.Contains(st.Detail, "check-full at the end of a run, stopped after 60m") {
		t.Errorf("the time limit should be in the summary: %+v", st)
	}
	if st.Kind != StepCaution || !strings.Contains(st.Detail, "side by side") {
		t.Errorf("more than 1 should come with a caution: %+v", st)
	}
	st, _ = ApplySettings(repo, Choice{Fast: &[]Suite{}, Concurrent: 1, Unasked: true})
	if st.Kind != StepCaution || !strings.Contains(st.Detail, "merges unchecked") || !strings.Contains(st.Detail, "not asked") {
		t.Errorf("no check, not asked: %+v", st)
	}
	st, _ = ApplySettings(repo, Choice{Fast: RunnerSuites("make check", "", FastRunner), Concurrent: 1})
	if st.Kind != StepDone {
		t.Errorf("one at a time with a check is plain done: %+v", st)
	}
	// Settings init doesn't ask about are kept.
	none := []string{}
	if err := SaveSettings(repo, Settings{CheckFast: "make check", Concurrency: 1, TicketLimit: "2h", ExcludeTypes: &none}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySettings(repo, Choice{Concurrent: 2}); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := LoadSettings(repo); s.TicketLimit != "2h" || s.CheckFast != FastRunner || s.Concurrency != 2 ||
		s.ExcludeTypes == nil || len(*s.ExcludeTypes) != 0 {
		t.Errorf("after init: %+v", s)
	}
}

func TestSaveSettingsKeepsUnknownKeys(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SettingsPath(repo), []byte(`{"check": "make check", "ticket_limit": "2h", "future": {"a": [1, 2]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(repo, Settings{Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(SettingsPath(repo))
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["concurrent"] != float64(2) || m["future"] == nil {
		t.Errorf("known keys follow Settings, unknown ones stay: %s", raw)
	}
	// Broken JSON is not overwritten.
	if err := os.WriteFile(SettingsPath(repo), []byte(`{"concurrent": `), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(repo, Settings{Concurrency: 2}); err == nil {
		t.Error("saved over a settings.json it could not read")
	}
}

func TestNextStepsOnlyListWhatIsLeft(t *testing.T) {
	repo, git := gitRepo(t)
	steps, _ := Init(context.Background(), repo, "make check", false)
	next := NextSteps(context.Background(), repo, steps, Prerequisites(repo, os.Getenv), Choice{})
	joined := strings.Join(next, "\n")
	if !strings.Contains(joined, "Commit .orchestra/") || !strings.Contains(joined, "orchestra") {
		t.Errorf("fresh init: %q", next)
	}
	if strings.Contains(joined, "placeholders") {
		t.Errorf("--check filled the placeholders: %q", next)
	}
	if !strings.Contains(joined, "Read .orchestra/worker-prompt.md") {
		t.Errorf("a prompt written from the template is to be read: %q", next)
	}
	git(repo, "add", ".orchestra")
	git(repo, "commit", "-q", "-m", "setup")
	steps, _ = Init(context.Background(), repo, "", false)
	if joined := strings.Join(NextSteps(context.Background(), repo, steps, nil, Choice{}), "\n"); strings.Contains(joined, "Commit") || strings.Contains(joined, "Read ") {
		t.Errorf("nothing to commit or read on a second run: %q", joined)
	}
}
