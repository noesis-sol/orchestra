// Package project is a project's .orchestra/ folder: where the worker prompt, settings, log and
// reports are, setting it up (orchestra init), and the per-ticket launch prompt.
package project

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/mcp"
)

// A project keeps everything orchestra owns in .orchestra/:
//
//	.orchestra/worker-prompt.md   committed: the worker prompt, TICKET_ID filled in per ticket
//	.orchestra/.gitignore         committed: ignores the rest
//	.orchestra/orchestra.log      the event log
//	.orchestra/reports/           run reports
//	.orchestra/run/               per-ticket scratch in each worktree (the launch prompt, the rules, the hooks);
//	                              in the main checkout, the event stream (events.jsonl), the workers
//	                              the last run left behind (state.json), and a feature interview's
//	                              instructions and the epic it filed (feature.json)
//
// The run lock is not among them: it is in the git directory (see LockRun).
//
// 'orchestra init' creates it. A project set up before that keeps its files in .claude/
// (worker-prompt.md, orchestrate.log, orchestrate-reports/) and still works.
const (
	Dir             = ".orchestra"
	promptName      = "worker-prompt.md"
	logName         = "orchestra.log"
	reportsName     = "reports"
	RunName         = "run"
	legacyPrompt    = ".claude/worker-prompt.md"
	legacyLog       = ".claude/orchestrate.log"
	legacyReports   = ".claude/orchestrate-reports"
	orchGitignore   = "orchestra.log\nreports/\nrun/\n"
	runExcludeEntry = "/" + Dir + "/" + RunName + "/"
)

//go:embed worker-prompt.md
var promptTemplate string

// Layout is where a project's orchestra files are.
type Layout struct {
	Prompt, Log, Reports string
	Legacy               bool // set up before 'orchestra init': files under .claude/
}

// Locate finds the files for the repository at repo: .orchestra/ once initialised,
// otherwise the .claude/ files of earlier versions.
func Locate(repo string) Layout {
	if fileExists(filepath.Join(repo, Dir, promptName)) || !fileExists(filepath.Join(repo, legacyPrompt)) {
		return Layout{
			Prompt:  filepath.Join(repo, Dir, promptName),
			Log:     filepath.Join(repo, Dir, logName),
			Reports: filepath.Join(repo, Dir, reportsName),
		}
	}
	return Layout{
		Prompt:  filepath.Join(repo, legacyPrompt),
		Log:     filepath.Join(repo, legacyLog),
		Reports: filepath.Join(repo, legacyReports),
		Legacy:  true,
	}
}

// IsSetUp reports whether the repository at repo was set up for orchestra: .orchestra/ has the
// worker prompt or the settings, or .claude/ has the worker prompt of a project set up before
// 'orchestra init'. A project with neither needs 'orchestra init' before it can run.
func IsSetUp(repo string) bool {
	return fileExists(filepath.Join(repo, Dir, promptName)) || fileExists(SettingsPath(repo)) ||
		fileExists(filepath.Join(repo, legacyPrompt))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// EnsureRunExcluded keeps each worktree's .orchestra/run/ out of git through the repository's
// info/exclude (shared by all worktrees, never committed), so it is ignored even on a branch cut
// before .orchestra/.gitignore was committed. Earlier versions excluded all of .orchestra/, which
// would hide the committed prompt; that entry is narrowed to run/.
func EnsureRunExcluded(ctx context.Context, repo string) error {
	common, err := git.Git{}.CommonDir(ctx, repo)
	if err != nil {
		return err
	}
	exclude := filepath.Join(common, "info", "exclude")
	b, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err // rewriting it would lose the entries it holds
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	found, changed := false, false
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case "/" + Dir + "/":
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
	return writeFile(exclude, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// ---- orchestra init ------------------------------------------------------------------

// StepKind is how a Step of 'orchestra init' went.
type StepKind int

// The ways a Step can go.
const (
	StepDone    StepKind = iota // done now
	StepKept                    // already there, left as it is
	StepMissing                 // something orchestra needs is missing
	StepCaution                 // done, with something to watch
)

// Step is one thing orchestra init did or found, for its summary.
type Step struct {
	Kind          StepKind
	Label, Detail string
	Template      bool     // the worker prompt was written from the template, for the project to adjust
	Commit        []string // what the step added for the user to commit, as at the repository's top
}

// Choice is what init sets up: the checks and their time limits, the default number of tickets
// at once, the MCP servers workers get and whether it installs bd.
type Choice struct {
	// Fast and Full are the suites init writes FastRunner and FullRunner from (Full's run after
	// FastRunner); an empty list writes a runner that checks nothing, and nil leaves a runner that is
	// there as it is.
	Fast, Full *[]Suite
	// ReplaceFast and ReplaceFull write a runner over one that differs from it; otherwise init keeps
	// that one and says so.
	ReplaceFast, ReplaceFull bool
	CheckFrom                string // where Fast came from, for the summary
	// CheckFastTimeout and CheckFullTimeout are the checks' time limits as in settings.json; "" for
	// DefaultCheckTimeout and DefaultCheckFullTimeout.
	CheckFastTimeout, CheckFullTimeout string
	Concurrent                         int
	Unasked                            bool // concurrent fell back to 1 without asking
	Replaced                           int  // the out-of-range concurrency in settings that init replaced, or 0
	// ReplacedTimeout and ReplacedFullTimeout are the check_fast_timeout and check_full_timeout in
	// settings that every run would reject, which init dropped, or "".
	ReplacedTimeout, ReplacedFullTimeout string
	// Setup is settings.json's setup command as init writes it ("" removes it), SetupWas the one there,
	// which init keeps unless --setup or the form changes it. SetupOffer is the command init offers for
	// the lockfiles SetupFrom (see FindSetup), and SetupUnasked is set when init could neither ask nor
	// take --setup.
	Setup, SetupWas, SetupOffer string
	SetupFrom                   []string
	SetupUnasked                bool
	// Union adds CHANGELOG.md merge=union to .gitattributes (see OffersUnion); UnionUnasked is set
	// when init could neither ask nor take --changelog-union.
	Union, UnionUnasked bool
	// MCP names the MCP servers workers get; nil leaves mcp_servers unset. MCPUnasked is set when
	// init could neither ask nor take --mcp.
	MCP        *[]string
	MCPUnasked bool
	// Servers are the MCP servers Claude Code knows for the repository, and ServersErr why they
	// couldn't all be read.
	Servers    []mcp.Server
	ServersErr error
	// Install is how init would install bd (see FindBeadsInstall), InstallBeads whether it does where
	// bd is missing, and InstallUnasked is set when init could neither ask nor take --install-beads.
	Install                      BeadsInstall
	InstallBeads, InstallUnasked bool
	// Tests is stage 2's choice of how the project's tests are set up, and FileUntested its "Also
	// file tickets for untested areas" with TestsFound. Where they file test work (FilesTestWork),
	// init installs the create-check-suite skill for Agent, the workers' agent kind, writing it over
	// a copy that differs where ReplaceSkill is set (see ApplySkill).
	Tests        Tests
	FileUntested bool
	Agent        string
	ReplaceSkill bool
}

// CheckScript is a project's own check, which check-fast.sh calls as a suite and init never edits.
const CheckScript = "scripts/check.sh"

// DefaultChoice starts from the project's settings, with the check command found in the worker
// prompt when the settings have none: a check command that isn't the runner becomes the runner's
// one suite. A concurrency outside 1 to MaxConcurrency, which every run would reject, is replaced
// by 1; a time limit every run would reject is dropped.
func DefaultChoice(s Settings, prompt string) Choice {
	c := Choice{CheckFastTimeout: s.CheckFastTimeout, CheckFullTimeout: s.CheckFullTimeout,
		Concurrent: s.Concurrency, CheckFrom: "settings", Setup: s.Setup, SetupWas: s.Setup}
	if s.MCPServers != nil {
		names := append([]string{}, *s.MCPServers...)
		c.MCP = &names
	}
	if _, err := ParseCheckTimeout(c.CheckFastTimeout); c.CheckFastTimeout != "" && err != nil {
		c.ReplacedTimeout, c.CheckFastTimeout = c.CheckFastTimeout, ""
	}
	if _, err := ParseCheckTimeout(c.CheckFullTimeout); c.CheckFullTimeout != "" && err != nil {
		c.ReplacedFullTimeout, c.CheckFullTimeout = c.CheckFullTimeout, ""
	}
	check, from := s.CheckFast, SettingsName
	if check == "" {
		if check = DetectCheck(prompt); check != "" {
			c.CheckFrom, from = "found in the worker prompt", "the worker prompt"
		}
	}
	c.Fast = RunnerSuites(check, from, FastRunner)
	c.Full = RunnerSuites(s.CheckFull, SettingsName, FullRunner)
	if c.Concurrent < 0 || c.Concurrent > MaxConcurrency {
		c.Replaced, c.Concurrent = c.Concurrent, 0
	}
	if c.Concurrent == 0 {
		c.Concurrent, c.Unasked = 1, true
	}
	return c
}

// RunnerSuites are the suites of the runner at path for one check command, found in from: nil when
// the command is that runner (nothing to write it from), none when it is empty, else the command.
func RunnerSuites(check, from, path string) *[]Suite {
	switch check = strings.TrimSpace(check); {
	case isRunner(check, path):
		return nil
	case check == "":
		return &[]Suite{}
	}
	return &[]Suite{{Command: check, FoundIn: from}}
}

// DetectCheck finds the check command in a worker prompt ("Check your work with `…`").
func DetectCheck(prompt string) string {
	const marker = "Check your work with `"
	_, rest, ok := strings.Cut(prompt, marker)
	if !ok {
		return ""
	}
	check, _, ok := strings.Cut(rest, "`")
	if !ok || strings.ContainsAny(check, "<>") {
		return "" // the template's placeholder
	}
	return check
}

// Init creates .orchestra/ in repo, with the worker prompt and its .gitignore.
func Init(ctx context.Context, repo, check string, force bool) ([]Step, error) {
	var done []Step
	dir := filepath.Join(repo, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return done, err
	}

	prompt := filepath.Join(dir, promptName)
	legacy := filepath.Join(repo, legacyPrompt)
	rel := Dir + "/" + promptName
	switch {
	case fileExists(prompt) && !force:
		done = append(done, Step{Kind: StepKept, Label: "worker prompt",
			Detail: rel + " is there; left as it is (--force replaces it with the template)"})
	case !fileExists(prompt) && fileExists(legacy) && !force:
		if err := movePrompt(ctx, repo, legacy, prompt); err != nil {
			return done, err
		}
		done = append(done, Step{Kind: StepDone, Label: "worker prompt", Detail: "moved " + legacyPrompt + " to " + rel})
	default:
		if err := os.WriteFile(prompt, []byte(fillTemplate(promptTemplate, check)), 0o644); err != nil {
			return done, err
		}
		done = append(done, Step{Kind: StepDone, Label: "worker prompt",
			Detail: "wrote " + rel + " from the template", Template: true})
	}

	gi := filepath.Join(dir, ".gitignore")
	if !fileExists(gi) {
		if err := os.WriteFile(gi, []byte(orchGitignore), 0o644); err != nil {
			return done, err
		}
		done = append(done, Step{Kind: StepDone, Label: ".gitignore",
			Detail: "the log, reports and per-ticket files stay out of git"})
	} else {
		done = append(done, Step{Kind: StepKept, Label: ".gitignore", Detail: Dir + "/.gitignore is there"})
	}
	if err := EnsureRunExcluded(ctx, repo); err != nil {
		return done, err
	}
	if fileExists(filepath.Join(repo, legacyLog)) || fileExists(filepath.Join(repo, legacyReports)) {
		done = append(done, Step{Kind: StepKept, Label: "history",
			Detail: "the old " + legacyLog + " and reports stay where they are; new runs write to " + Dir + "/"})
	}
	return done, nil
}

// ApplySettings saves the choice to .orchestra/settings.json: the runners as check_fast and
// check_full, under their new names whatever the settings called them, and the setup command where
// the choice changes it.
func ApplySettings(repo string, c Choice) (Step, error) {
	s, _, _ := LoadSettings(repo) // keep the settings init doesn't ask about; init has read them already
	s.CheckFast, s.CheckFull, s.Concurrency, s.MCPServers = FastRunner, FullRunner, c.Concurrent, c.MCP
	s.CheckFastTimeout, s.CheckFullTimeout = c.CheckFastTimeout, c.CheckFullTimeout
	if c.Setup != c.SetupWas {
		s.Setup = c.Setup
	}
	if c.MCP != nil && *c.MCP == nil {
		s.MCPServers = &[]string{} // none, not null
	}
	if err := SaveSettings(repo, s); err != nil {
		return Step{}, err
	}
	detail := fmt.Sprintf("%d at the same time", c.Concurrent)
	if c.Replaced != 0 {
		detail += fmt.Sprintf(" (settings had %d, outside 1 to %d)", c.Replaced, MaxConcurrency)
	}
	if c.Unasked {
		detail += " (not asked: no terminal; --concurrent sets it)"
	}
	unchecked := c.Fast != nil && len(*c.Fast) == 0
	switch {
	case unchecked:
		detail += " · check-fast checks nothing yet: a rebased ticket merges unchecked (--check-fast sets a command)"
	case c.Fast == nil:
		detail += " · check-fast: " + FastRunner + " as it is"
	default:
		var commands []string
		for _, suite := range *c.Fast {
			commands = append(commands, suite.Command)
		}
		detail += " · check-fast: " + strings.Join(commands, ", ")
		if c.CheckFrom == "found in the worker prompt" {
			detail += " (found in the worker prompt)"
		}
	}
	if !unchecked {
		detail += ", stopped after " + orText(c.CheckFastTimeout, DefaultCheckTimeoutText)
	}
	detail += " · check-full at the end of a run, stopped after " + orText(c.CheckFullTimeout, DefaultCheckFullTimeoutText)
	if c.ReplacedTimeout != "" {
		detail += fmt.Sprintf(" (settings had check_fast_timeout '%s', not a positive duration)", c.ReplacedTimeout)
	}
	if c.ReplacedFullTimeout != "" {
		detail += fmt.Sprintf(" (settings had check_full_timeout '%s', not a positive duration)", c.ReplacedFullTimeout)
	}
	kind := StepDone
	if c.Concurrent > 1 || unchecked || c.Replaced != 0 || c.ReplacedTimeout != "" || c.ReplacedFullTimeout != "" {
		kind = StepCaution
	}
	if c.Concurrent > 1 {
		detail += fmt.Sprintf(" · with %d at once, the checks must cope with running side by side", c.Concurrent)
	}
	return Step{Kind: kind, Label: "settings", Detail: detail}, nil
}

// orText is v, or def when v is empty.
func orText(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// movePrompt moves the legacy prompt, with 'git mv' when git tracks it so the move is staged.
func movePrompt(ctx context.Context, repo, from, to string) error {
	rel, _ := filepath.Rel(repo, from) // both paths are under repo, so Rel can't fail
	if g := (git.Git{}); g.Tracks(ctx, repo, rel) {
		relTo, _ := filepath.Rel(repo, to)
		_, err := g.Move(ctx, repo, rel, relTo)
		return err
	}
	return os.Rename(from, to)
}

// fillTemplate puts the check command into the template's placeholders.
func fillTemplate(t, check string) string {
	if check == "" {
		return t
	}
	const what = " <What it runs, e.g. lint, build and the fast test suites.>"
	if isRunner(check, FastRunner) {
		// The runner lists its suites, a command a line: one of them is the quicker subset.
		t = strings.Replace(t, what, " It runs the project's fast suites, a command a line.", 1)
		t = strings.ReplaceAll(t, "`<a quicker subset>`", "a suite's command from `<check command>`")
	}
	t = strings.ReplaceAll(t, "<check command>", check)
	t = strings.ReplaceAll(t, "<a quicker subset>", check)
	// The sentence describing another check is the project's to write; drop the placeholder.
	return strings.Replace(t, what, "", 1)
}

// Prerequisites reports what orchestra needs and whether it is there. getenv gives the environment
// where bd may be installed off the PATH (see LocateBd).
func Prerequisites(repo string, getenv func(string) string) []Step {
	var steps []Step
	mark := func(ok bool, what, fix string) {
		if ok {
			steps = append(steps, Step{Kind: StepDone, Label: what})
		} else {
			steps = append(steps, Step{Kind: StepMissing, Label: what, Detail: fix})
		}
	}
	has := func(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }
	if bd, onPath := LocateBd(getenv); bd != "" && !onPath {
		mark(false, "bd", offPath(bd))
	} else {
		mark(onPath, "bd", "install Beads")
	}
	mark(fileExists(filepath.Join(repo, ".beads")), "Beads", "set it up here: bd init")
	mark(has("herdr"), "herdr", "install Herdr; workers run in its tabs")
	mark(has("claude"), "claude", "install Claude Code; it runs the workers, triage and the report")
	return steps
}

// NextSteps lists what is left for the user, in order.
func NextSteps(ctx context.Context, repo string, steps []Step, pre []Step, c Choice) []string {
	var next []string
	for _, p := range pre {
		if p.Kind == StepMissing {
			next = append(next, "Fix what's missing above ("+p.Label+": "+p.Detail+").")
			break
		}
	}
	prompt, _ := os.ReadFile(filepath.Join(repo, Dir, promptName)) // a step above says if it is missing
	switch {
	case strings.Contains(string(prompt), "<check command>") || strings.Contains(string(prompt), "<What it runs"):
		next = append(next, "Fill in the <…> placeholders in "+Dir+"/"+promptName+".")
	case wroteTemplate(steps):
		next = append(next, "Read "+Dir+"/"+promptName+" and adjust it to the project.")
	}
	if c.MCP == nil || len(*c.MCP) == 0 {
		if names := AvailableServers(c.Servers); len(names) > 0 {
			next = append(next, "Choose the MCP servers workers need from those defined here ("+
				strings.Join(names, ", ")+"):\norchestra init --mcp <name>,<name>")
		}
	}
	if s, _, err := LoadSettings(repo); err == nil && s.WorkerEffort == "" {
		next = append(next, "Workers run at Claude Code's default effort. To choose one, set \"worker_effort\" ("+
			strings.Join(Efforts, ", ")+") in "+Dir+"/"+SettingsName+", or run with --worker-effort.")
	}
	// The next steps are advice: a git status that fails only leaves out the commit step.
	var commit []string
	if len(git.Git{}.Changes(ctx, repo, Dir, legacyPrompt)) > 0 {
		commit = append(commit, Dir+"/")
	}
	if len(git.Git{}.Changes(ctx, repo, attributesName)) > 0 {
		commit = append(commit, attributesName)
	}
	for _, s := range steps {
		for _, p := range s.Commit {
			if !slices.Contains(commit, p) {
				commit = append(commit, p)
			}
		}
	}
	if len(commit) > 0 {
		next = append(next, "Commit "+joinAnd(commit)+".")
	}
	next = append(next, "From a Herdr pane, on the branch finished tickets should land on:\norchestra")
	return next
}

func wroteTemplate(steps []Step) bool {
	for _, s := range steps {
		if s.Template {
			return true
		}
	}
	return false
}

// RulesName is the file in .orchestra/run/ that holds a Claude worker's standing rules.
const RulesName = "rules.md"

// WriteRules puts a Claude worker's standing rules in the worktree at .orchestra/run/rules.md,
// which EnsureRunExcluded keeps out of git, and returns its path, for --append-system-prompt-file.
func WriteRules(wt, rules string) (string, error) {
	root, err := OpenRun(wt)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(RulesName)
	if err := WriteRun(root, wt, rel, []byte(rules), 0o644); err != nil {
		return "", err
	}
	return filepath.Join(wt, rel), nil
}

// WriteLaunchPrompt puts a worker's first message, prompt, in the worktree at
// .orchestra/run/prompt.md, which EnsureRunExcluded keeps out of git, and returns the one-line
// instruction to start the worker with.
func WriteLaunchPrompt(wt, ticket, prompt string) (string, error) {
	root, err := OpenRun(wt)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	if err := WriteRun(root, wt, RunPath("prompt.md"), []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Your instructions for ticket %s are in %s/%s/prompt.md in this directory. "+
		"Read that file and follow it exactly.", ticket, Dir, RunName), nil
}
