package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// A project keeps everything orchestra owns in .orchestra/:
//
//	.orchestra/worker-prompt.md   committed: the worker prompt, TICKET_ID filled in per ticket
//	.orchestra/.gitignore         committed: ignores the rest
//	.orchestra/orchestra.log      the event log
//	.orchestra/reports/           run reports
//	.orchestra/run/               per-ticket scratch in each worktree (the launch prompt)
//
// 'orchestra init' creates it. A project set up before that keeps its files in .claude/
// (worker-prompt.md, orchestrate.log, orchestrate-reports/) and still works.
const (
	orchDir         = ".orchestra"
	promptName      = "worker-prompt.md"
	logName         = "orchestra.log"
	reportsName     = "reports"
	runName         = "run"
	legacyPrompt    = ".claude/worker-prompt.md"
	legacyLog       = ".claude/orchestrate.log"
	legacyReports   = ".claude/orchestrate-reports"
	orchGitignore   = "orchestra.log\nreports/\nrun/\n"
	runExcludeEntry = "/" + orchDir + "/" + runName + "/"
)

//go:embed prompts/worker-prompt.md
var promptTemplate string

// layout is where a project's orchestra files are.
type layout struct {
	Prompt, Log, Reports string
	Legacy               bool // set up before 'orchestra init': files under .claude/
}

// projectLayout finds the files for the repository at repo: .orchestra/ once initialised,
// otherwise the .claude/ files of earlier versions.
func projectLayout(repo string) layout {
	if fileExists(filepath.Join(repo, orchDir, promptName)) || !fileExists(filepath.Join(repo, legacyPrompt)) {
		return layout{
			Prompt:  filepath.Join(repo, orchDir, promptName),
			Log:     filepath.Join(repo, orchDir, logName),
			Reports: filepath.Join(repo, orchDir, reportsName),
		}
	}
	return layout{
		Prompt:  filepath.Join(repo, legacyPrompt),
		Log:     filepath.Join(repo, legacyLog),
		Reports: filepath.Join(repo, legacyReports),
		Legacy:  true,
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ensureRunExcluded keeps each worktree's .orchestra/run/ out of git through the repository's
// info/exclude (shared by all worktrees, never committed), so it is ignored even on a branch cut
// before .orchestra/.gitignore was committed. Earlier versions excluded all of .orchestra/, which
// would hide the committed prompt; that entry is narrowed to run/.
func ensureRunExcluded(repo string) error {
	common, err := run(repo, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	exclude := filepath.Join(strings.TrimSpace(common), "info", "exclude")
	b, _ := os.ReadFile(exclude)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	found, changed := false, false
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case "/" + orchDir + "/":
			lines[i], changed = runExcludeEntry, true
			found = true
		case runExcludeEntry:
			found = true
		}
	}
	if !found {
		lines = append(lines, "# per-ticket files written by orchestra", runExcludeEntry)
		changed = true
	}
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	return os.WriteFile(exclude, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// ---- orchestra init ------------------------------------------------------------------

// runInit sets up .orchestra/ in the repository around dir and reports what it did and what is
// still missing. It returns the exit code.
func runInit(dir string, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	check := fs.String("check", "", "the project's check command (lint, build, tests), filled into the prompt")
	force := fs.Bool("force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	var concurrent int
	fs.IntVar(&concurrent, "concurrent", 0, "tickets to run at the same time by default (asked when omitted)")
	fs.IntVar(&concurrent, "c", 0, "shorthand for --concurrent")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra init [--check \"<command>\"] [--concurrent N] [--force]\n\n"+
			"Set up .orchestra/ in this repository: the worker prompt (from the built-in template, or moved\n"+
			"from .claude/worker-prompt.md), settings.json (the check command and how many tickets run at\n"+
			"the same time), a .gitignore for the log, reports and per-ticket files, and a check of what\n"+
			"orchestra needs.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitSetup
	}

	out, err := run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init: not inside a git repository")
		return exitSetup
	}
	repo := strings.TrimSpace(out)
	lines, err := initProject(repo, *check, *force)
	for _, l := range lines {
		fmt.Println(l)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init:", err)
		return exitSetup
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	settingsLines, err := configureSettings(repo, *check, concurrent, interactive, os.Stdin, os.Stdout)
	for _, l := range settingsLines {
		fmt.Println(l)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init:", err)
		return exitSetup
	}
	for _, l := range prerequisites(repo) {
		fmt.Println(l)
	}
	fmt.Printf("\nNext:\n")
	if *check == "" && !strings.Contains(strings.Join(lines, "\n"), "moved") {
		fmt.Printf("  1. Fill in the <…> placeholders in %s/%s (or run init again with --check and --force).\n", orchDir, promptName)
	} else {
		fmt.Printf("  1. Read %s/%s and adjust it to the project.\n", orchDir, promptName)
	}
	fmt.Printf("  2. Commit %s/ (the prompt, settings.json and .gitignore).\n", orchDir)
	fmt.Printf("  3. From a Herdr pane, on the branch finished tickets should land on: WORKSPACE=<id> orchestra\n")
	return exitOK
}

// initProject creates .orchestra/ in repo and returns a line per step taken.
func initProject(repo, check string, force bool) ([]string, error) {
	var done []string
	dir := filepath.Join(repo, orchDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return done, err
	}

	prompt := filepath.Join(dir, promptName)
	legacy := filepath.Join(repo, legacyPrompt)
	switch {
	case fileExists(prompt) && !force:
		done = append(done, fmt.Sprintf("✓ %s/%s exists; left as it is (-force replaces it with the template)", orchDir, promptName))
	case !fileExists(prompt) && fileExists(legacy) && !force:
		if err := movePrompt(repo, legacy, prompt); err != nil {
			return done, err
		}
		done = append(done, fmt.Sprintf("✓ moved %s to %s/%s", legacyPrompt, orchDir, promptName))
	default:
		if err := os.WriteFile(prompt, []byte(fillTemplate(promptTemplate, check)), 0o644); err != nil {
			return done, err
		}
		done = append(done, fmt.Sprintf("✓ wrote %s/%s from the template", orchDir, promptName))
	}

	gi := filepath.Join(dir, ".gitignore")
	if !fileExists(gi) {
		if err := os.WriteFile(gi, []byte(orchGitignore), 0o644); err != nil {
			return done, err
		}
		done = append(done, fmt.Sprintf("✓ wrote %s/.gitignore (log, reports and per-ticket files stay out of git)", orchDir))
	}
	if err := ensureRunExcluded(repo); err != nil {
		return done, err
	}
	if fileExists(filepath.Join(repo, legacyLog)) || fileExists(filepath.Join(repo, legacyReports)) {
		done = append(done, fmt.Sprintf("  the old %s and %s/ stay where they are, as history; new runs write to %s/", legacyLog, legacyReports, orchDir))
	}
	return done, nil
}

// movePrompt moves the legacy prompt, with 'git mv' when git tracks it so the move is staged.
func movePrompt(repo, from, to string) error {
	rel, _ := filepath.Rel(repo, from)
	if _, err := run(repo, "git", "ls-files", "--error-unmatch", rel); err == nil {
		relTo, _ := filepath.Rel(repo, to)
		_, err := run(repo, "git", "mv", rel, relTo)
		return err
	}
	return os.Rename(from, to)
}

// fillTemplate puts the check command into the template's placeholders.
func fillTemplate(t, check string) string {
	if check == "" {
		return t
	}
	t = strings.ReplaceAll(t, "<check command>", check)
	t = strings.ReplaceAll(t, "<a quicker subset>", check)
	var kept []string
	for _, l := range strings.Split(t, "\n") {
		// The sentence describing the checks is the project's to write; drop the placeholder.
		l = strings.Replace(l, " <What it runs, e.g. lint, build and the test suites.>", "", 1)
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// prerequisites reports what orchestra needs and whether it is there.
func prerequisites(repo string) []string {
	var lines []string
	mark := func(ok bool, what, fix string) {
		if ok {
			lines = append(lines, "✓ "+what)
		} else {
			lines = append(lines, "✗ "+what+": "+fix)
		}
	}
	has := func(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }
	mark(has("bd"), "bd (Beads) installed", "install Beads")
	mark(fileExists(filepath.Join(repo, ".beads")), "Beads set up in this repository", "run bd init")
	mark(has("herdr"), "herdr installed", "install Herdr; workers run in its tabs")
	mark(has("claude"), "claude installed", "install Claude Code; it runs the workers, triage and the run report")
	return lines
}
