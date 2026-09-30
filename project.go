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

// stepKind is how a step of 'orchestra init' went.
type stepKind int

const (
	stepDone    stepKind = iota // done now
	stepKept                    // already there, left as it is
	stepMissing                 // something orchestra needs is missing
	stepCaution                 // done, with something to watch
)

type step struct {
	kind          stepKind
	label, detail string
}

// initChoice is what init sets up: the check command and the default number of tickets at once.
type initChoice struct {
	Check      string
	Concurrent int
	checkFrom  string // where the check command came from, for the summary
	unasked    bool   // concurrent fell back to 1 without asking
}

// defaultChoice starts from the project's settings, with the check command found in the worker
// prompt when the settings have none.
func defaultChoice(s Settings, prompt string) initChoice {
	c := initChoice{Check: s.Check, Concurrent: s.Concurrency, checkFrom: "settings"}
	if c.Check == "" {
		if c.Check = detectCheck(prompt); c.Check != "" {
			c.checkFrom = "found in the worker prompt"
		}
	}
	if c.Concurrent == 0 {
		c.Concurrent, c.unasked = 1, true
	}
	return c
}

// detectCheck finds the check command in a worker prompt ("Check your work with `…`").
func detectCheck(prompt string) string {
	const marker = "Check your work with `"
	i := strings.Index(prompt, marker)
	if i < 0 {
		return ""
	}
	rest := prompt[i+len(marker):]
	j := strings.Index(rest, "`")
	if j < 0 || strings.ContainsAny(rest[:j], "<>") {
		return "" // the template's placeholder
	}
	return rest[:j]
}

// runInit sets up .orchestra/ in the repository around dir, asking what it needs when run in a
// terminal, and returns the exit code.
func runInit(dir string, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	check := fs.String("check", "", "the project's check command (lint, build, tests)")
	force := fs.Bool("force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	var concurrent int
	fs.IntVar(&concurrent, "concurrent", 0, "tickets to run at the same time by default (asked when omitted)")
	fs.IntVar(&concurrent, "c", 0, "shorthand for --concurrent")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra init [--check \"<command>\"] [--concurrent N] [--force]\n\n"+
			"Set up .orchestra/ in this repository: the worker prompt (from the built-in template, or moved\n"+
			"from .claude/worker-prompt.md), settings.json (the check command and how many tickets run at\n"+
			"the same time), a .gitignore for the log, reports and per-ticket files, and a check of what\n"+
			"orchestra needs. In a terminal it asks for anything the flags don't give.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitOK
		}
		return exitSetup
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	checkGiven, concurrentGiven := given["check"], given["concurrent"] || given["c"]
	if concurrentGiven && (concurrent < 1 || concurrent > maxConcurrency) {
		fmt.Fprintf(os.Stderr, "orchestra init: --concurrent must be between 1 and %d\n", maxConcurrency)
		return exitSetup
	}

	out, err := run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init: not inside a git repository")
		return exitSetup
	}
	repo := strings.TrimSpace(out)
	existing, _, err := loadSettings(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init:", err)
		return exitSetup
	}
	promptText, _ := os.ReadFile(projectLayout(repo).Prompt)
	choice := defaultChoice(existing, string(promptText))
	if checkGiven {
		choice.Check, choice.checkFrom = *check, "--check"
	}
	if concurrentGiven {
		choice.Concurrent, choice.unasked = concurrent, false
	}

	ui := newInitUI(os.Stdout)
	ui.header(repo)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if interactive && !(checkGiven && concurrentGiven) {
		if err := askInit(&choice, !checkGiven, !concurrentGiven); err != nil {
			ui.cancelled()
			return exitSetup
		}
	}

	steps, err := initProject(repo, choice.Check, *force)
	if err == nil {
		var s step
		s, err = applySettings(repo, choice)
		steps = append(steps, s)
	}
	if err != nil {
		ui.steps(steps)
		fmt.Fprintln(os.Stderr, "orchestra init:", err)
		return exitSetup
	}
	pre := prerequisites(repo)
	ui.steps(steps)
	ui.prerequisites(pre)
	ui.next(nextSteps(repo, steps, pre))
	ready := true
	for _, p := range pre {
		ready = ready && p.kind != stepMissing
	}
	ui.signOff(ready)
	return exitOK
}

// initProject creates .orchestra/ in repo, with the worker prompt and its .gitignore.
func initProject(repo, check string, force bool) ([]step, error) {
	var done []step
	dir := filepath.Join(repo, orchDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return done, err
	}

	prompt := filepath.Join(dir, promptName)
	legacy := filepath.Join(repo, legacyPrompt)
	rel := orchDir + "/" + promptName
	switch {
	case fileExists(prompt) && !force:
		done = append(done, step{stepKept, "worker prompt", rel + " is there; left as it is (--force replaces it with the template)"})
	case !fileExists(prompt) && fileExists(legacy) && !force:
		if err := movePrompt(repo, legacy, prompt); err != nil {
			return done, err
		}
		done = append(done, step{stepDone, "worker prompt", "moved " + legacyPrompt + " to " + rel})
	default:
		if err := os.WriteFile(prompt, []byte(fillTemplate(promptTemplate, check)), 0o644); err != nil {
			return done, err
		}
		done = append(done, step{stepDone, "worker prompt", "wrote " + rel + " from the template"})
	}

	gi := filepath.Join(dir, ".gitignore")
	if !fileExists(gi) {
		if err := os.WriteFile(gi, []byte(orchGitignore), 0o644); err != nil {
			return done, err
		}
		done = append(done, step{stepDone, ".gitignore", "the log, reports and per-ticket files stay out of git"})
	} else {
		done = append(done, step{stepKept, ".gitignore", orchDir + "/.gitignore is there"})
	}
	if err := ensureRunExcluded(repo); err != nil {
		return done, err
	}
	if fileExists(filepath.Join(repo, legacyLog)) || fileExists(filepath.Join(repo, legacyReports)) {
		done = append(done, step{stepKept, "history", "the old " + legacyLog + " and reports stay where they are; new runs write to " + orchDir + "/"})
	}
	return done, nil
}

// applySettings saves the choice to .orchestra/settings.json.
func applySettings(repo string, c initChoice) (step, error) {
	if err := saveSettings(repo, Settings{Check: c.Check, Concurrency: c.Concurrent}); err != nil {
		return step{}, err
	}
	detail := fmt.Sprintf("%d at the same time", c.Concurrent)
	if c.unasked {
		detail += " (not asked: no terminal; --concurrent sets it)"
	}
	if c.Check != "" {
		detail += " · check: " + c.Check
		if c.checkFrom == "found in the worker prompt" {
			detail += " (found in the worker prompt)"
		}
	} else {
		detail += " · no check command: a rebased ticket merges unchecked (--check sets one)"
	}
	kind := stepDone
	if c.Concurrent > 1 || c.Check == "" {
		kind = stepCaution
	}
	if c.Concurrent > 1 {
		detail += fmt.Sprintf(" · with %d at once, the checks must cope with running side by side", c.Concurrent)
	}
	return step{kind, "settings", detail}, nil
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
	// The sentence describing the checks is the project's to write; drop the placeholder.
	return strings.Replace(t, " <What it runs, e.g. lint, build and the test suites.>", "", 1)
}

// prerequisites reports what orchestra needs and whether it is there.
func prerequisites(repo string) []step {
	var steps []step
	mark := func(ok bool, what, fix string) {
		if ok {
			steps = append(steps, step{stepDone, what, ""})
		} else {
			steps = append(steps, step{stepMissing, what, fix})
		}
	}
	has := func(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }
	mark(has("bd"), "bd", "install Beads")
	mark(fileExists(filepath.Join(repo, ".beads")), "Beads", "set it up here: bd init")
	mark(has("herdr"), "herdr", "install Herdr; workers run in its tabs")
	mark(has("claude"), "claude", "install Claude Code; it runs the workers, triage and the report")
	return steps
}

// nextSteps lists what is left for the user, in order.
func nextSteps(repo string, steps []step, pre []step) []string {
	var next []string
	for _, p := range pre {
		if p.kind == stepMissing {
			next = append(next, "Fix what's missing above ("+p.label+": "+p.detail+").")
			break
		}
	}
	prompt, _ := os.ReadFile(filepath.Join(repo, orchDir, promptName))
	switch {
	case strings.Contains(string(prompt), "<check command>") || strings.Contains(string(prompt), "<What it runs"):
		next = append(next, "Fill in the <…> placeholders in "+orchDir+"/"+promptName+".")
	case len(steps) > 0 && strings.HasPrefix(steps[0].detail, "wrote"):
		next = append(next, "Read "+orchDir+"/"+promptName+" and adjust it to the project.")
	}
	if out, _ := run(repo, "git", "status", "--porcelain", "--", orchDir, legacyPrompt); strings.TrimSpace(out) != "" {
		next = append(next, "Commit "+orchDir+"/.")
	}
	ws := os.Getenv("HERDR_WORKSPACE_ID")
	if ws == "" {
		ws = "<herdr workspace id>"
	}
	next = append(next, "From a Herdr pane, on the branch finished tickets should land on:\n"+"WORKSPACE="+ws+" orchestra")
	return next
}
