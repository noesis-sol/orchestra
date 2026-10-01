// Package project is a project's .orchestra/ folder: where the worker prompt, settings, log and
// reports are, setting it up (orchestra init), and the per-ticket launch prompt.
package project

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A project keeps everything orchestra owns in .orchestra/:
//
//	.orchestra/worker-prompt.md   committed: the worker prompt, TICKET_ID filled in per ticket
//	.orchestra/.gitignore         committed: ignores the rest
//	.orchestra/orchestra.log      the event log
//	.orchestra/reports/           run reports
//	.orchestra/run/               per-ticket scratch in each worktree (the launch prompt, the worker's hooks)
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

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// EnsureRunExcluded keeps each worktree's .orchestra/run/ out of git through the repository's
// info/exclude (shared by all worktrees, never committed), so it is ignored even on a branch cut
// before .orchestra/.gitignore was committed. Earlier versions excluded all of .orchestra/, which
// would hide the committed prompt; that entry is narrowed to run/.
func EnsureRunExcluded(ctx context.Context, repo string) error {
	common, err := command.Output(ctx, command.ReadLimit, repo, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
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
	return os.WriteFile(exclude, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// ---- orchestra init ------------------------------------------------------------------

// StepKind is how a Step of 'orchestra init' went.
type StepKind int

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
	Template      bool // the worker prompt was written from the template, for the project to adjust
}

// Choice is what init sets up: the check command, its time limit and the default number of
// tickets at once.
type Choice struct {
	Check        string
	CheckTimeout string // as in settings.json; "" for DefaultCheckTimeout
	Concurrent   int
	CheckFrom    string // where the check command came from, for the summary
	Unasked      bool   // concurrent fell back to 1 without asking
	Replaced     int    // the out-of-range concurrency in settings that init replaced, or 0
	// ReplacedTimeout is the check_timeout in settings that every run would reject, which init
	// dropped, or "".
	ReplacedTimeout string
	// Union adds CHANGELOG.md merge=union to .gitattributes (see OffersUnion); UnionUnasked is set
	// when init could neither ask nor take --changelog-union.
	Union, UnionUnasked bool
}

// DefaultChoice starts from the project's settings, with the check command found in the worker
// prompt when the settings have none. A concurrency outside 1 to MaxConcurrency, which every run
// would reject, is replaced by 1; a check_timeout every run would reject is dropped.
func DefaultChoice(s Settings, prompt string) Choice {
	c := Choice{Check: s.Check, CheckTimeout: s.CheckTimeout, Concurrent: s.Concurrency, CheckFrom: "settings"}
	if _, err := ParseCheckTimeout(c.CheckTimeout); c.CheckTimeout != "" && err != nil {
		c.ReplacedTimeout, c.CheckTimeout = c.CheckTimeout, ""
	}
	if c.Check == "" {
		if c.Check = DetectCheck(prompt); c.Check != "" {
			c.CheckFrom = "found in the worker prompt"
		}
	}
	if c.Concurrent < 0 || c.Concurrent > MaxConcurrency {
		c.Replaced, c.Concurrent = c.Concurrent, 0
	}
	if c.Concurrent == 0 {
		c.Concurrent, c.Unasked = 1, true
	}
	return c
}

// DetectCheck finds the check command in a worker prompt ("Check your work with `…`").
func DetectCheck(prompt string) string {
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
		done = append(done, Step{Kind: StepKept, Label: "worker prompt", Detail: rel + " is there; left as it is (--force replaces it with the template)"})
	case !fileExists(prompt) && fileExists(legacy) && !force:
		if err := movePrompt(ctx, repo, legacy, prompt); err != nil {
			return done, err
		}
		done = append(done, Step{Kind: StepDone, Label: "worker prompt", Detail: "moved " + legacyPrompt + " to " + rel})
	default:
		if err := os.WriteFile(prompt, []byte(fillTemplate(promptTemplate, check)), 0o644); err != nil {
			return done, err
		}
		done = append(done, Step{Kind: StepDone, Label: "worker prompt", Detail: "wrote " + rel + " from the template", Template: true})
	}

	gi := filepath.Join(dir, ".gitignore")
	if !fileExists(gi) {
		if err := os.WriteFile(gi, []byte(orchGitignore), 0o644); err != nil {
			return done, err
		}
		done = append(done, Step{Kind: StepDone, Label: ".gitignore", Detail: "the log, reports and per-ticket files stay out of git"})
	} else {
		done = append(done, Step{Kind: StepKept, Label: ".gitignore", Detail: Dir + "/.gitignore is there"})
	}
	if err := EnsureRunExcluded(ctx, repo); err != nil {
		return done, err
	}
	if fileExists(filepath.Join(repo, legacyLog)) || fileExists(filepath.Join(repo, legacyReports)) {
		done = append(done, Step{Kind: StepKept, Label: "history", Detail: "the old " + legacyLog + " and reports stay where they are; new runs write to " + Dir + "/"})
	}
	return done, nil
}

// ApplySettings saves the choice to .orchestra/settings.json.
func ApplySettings(repo string, c Choice) (Step, error) {
	s, _, _ := LoadSettings(repo) // keep the settings init doesn't ask about
	s.Check, s.CheckTimeout, s.Concurrency = c.Check, c.CheckTimeout, c.Concurrent
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
	if c.Check != "" {
		detail += " · check: " + c.Check
		if c.CheckFrom == "found in the worker prompt" {
			detail += " (found in the worker prompt)"
		}
		limit := c.CheckTimeout
		if limit == "" {
			limit = DefaultCheckTimeoutText
		}
		detail += ", stopped after " + limit
	} else {
		detail += " · no check command: a rebased ticket merges unchecked (--check sets one)"
	}
	if c.ReplacedTimeout != "" {
		detail += fmt.Sprintf(" (settings had check_timeout '%s', not a positive duration)", c.ReplacedTimeout)
	}
	kind := StepDone
	if c.Concurrent > 1 || c.Check == "" || c.Replaced != 0 || c.ReplacedTimeout != "" {
		kind = StepCaution
	}
	if c.Concurrent > 1 {
		detail += fmt.Sprintf(" · with %d at once, the checks must cope with running side by side", c.Concurrent)
	}
	return Step{Kind: kind, Label: "settings", Detail: detail}, nil
}

// movePrompt moves the legacy prompt, with 'git mv' when git tracks it so the move is staged.
func movePrompt(ctx context.Context, repo, from, to string) error {
	rel, _ := filepath.Rel(repo, from)
	if _, err := command.Output(ctx, command.ReadLimit, repo, "git", "ls-files", "--error-unmatch", rel); err == nil {
		relTo, _ := filepath.Rel(repo, to)
		_, err := command.Output(ctx, command.WriteLimit, repo, "git", "mv", rel, relTo)
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

// Prerequisites reports what orchestra needs and whether it is there.
func Prerequisites(repo string) []Step {
	var steps []Step
	mark := func(ok bool, what, fix string) {
		if ok {
			steps = append(steps, Step{Kind: StepDone, Label: what})
		} else {
			steps = append(steps, Step{Kind: StepMissing, Label: what, Detail: fix})
		}
	}
	has := func(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }
	mark(has("bd"), "bd", "install Beads")
	mark(fileExists(filepath.Join(repo, ".beads")), "Beads", "set it up here: bd init")
	mark(has("herdr"), "herdr", "install Herdr; workers run in its tabs")
	mark(has("claude"), "claude", "install Claude Code; it runs the workers, triage and the report")
	return steps
}

// NextSteps lists what is left for the user, in order.
func NextSteps(ctx context.Context, repo string, steps []Step, pre []Step) []string {
	var next []string
	for _, p := range pre {
		if p.Kind == StepMissing {
			next = append(next, "Fix what's missing above ("+p.Label+": "+p.Detail+").")
			break
		}
	}
	prompt, _ := os.ReadFile(filepath.Join(repo, Dir, promptName))
	switch {
	case strings.Contains(string(prompt), "<check command>") || strings.Contains(string(prompt), "<What it runs"):
		next = append(next, "Fill in the <…> placeholders in "+Dir+"/"+promptName+".")
	case wroteTemplate(steps):
		next = append(next, "Read "+Dir+"/"+promptName+" and adjust it to the project.")
	}
	var commit []string
	if out, _ := command.Output(ctx, command.ReadLimit, repo, "git", "status", "--porcelain", "--", Dir, legacyPrompt); strings.TrimSpace(out) != "" {
		commit = append(commit, Dir+"/")
	}
	if out, _ := command.Output(ctx, command.ReadLimit, repo, "git", "status", "--porcelain", "--", attributesName); strings.TrimSpace(out) != "" {
		commit = append(commit, attributesName)
	}
	if len(commit) > 0 {
		next = append(next, "Commit "+strings.Join(commit, " and ")+".")
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

// WriteLaunchPrompt puts the worker prompt in the worktree at .orchestra/run/prompt.md, which
// EnsureRunExcluded keeps out of git, and returns the one-line instruction to start the worker with.
func WriteLaunchPrompt(wt, ticket, prompt string) (string, error) {
	dir := filepath.Join(wt, Dir, RunName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Your instructions for ticket %s are in %s/%s/prompt.md in this directory. Read that file and follow it exactly.", ticket, Dir, RunName), nil
}
