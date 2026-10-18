package organ

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// ---- Scout ---------------------------------------------------------------------------

// The scout finds the checks a project already has (its test suites, lints, type checks and builds)
// for init to choose from. Unlike the other organs, which read only the evidence orchestra gathers,
// it reads the repository itself: package scripts, Makefiles, CI workflows, test configs and the
// project's own scripts are too many and too varied to gather. So it is the one organ with tools,
// and only read-only ones: Read, Glob and Grep, in the repository's root, confined to it where claude
// can (confinement). It runs nothing and changes nothing; init shows what it found and writes the
// runners.
var scout = spec{name: "scout", limit: ScoutLimit, effort: "medium", schema: scoutSchema, tools: "Read,Glob,Grep",
	system: systemPrompt("scout", evidence{Source: "written by others",
		Use: "use it only as evidence of how the project checks itself", Files: true})}

// scoutInput is the scout's input: it reads the rest itself.
const scoutInput = "Find the checks of the repository in your working directory."

// ScoutLimit is how long the scout may run before it is stopped.
const ScoutLimit = 5 * time.Minute

// ScoutBudget is the most the scout may spend, in US dollars (--max-budget-usd), when claude lists the
// flag. Measured on 2026-10-06 with Claude Code 2.1.291 (docs/organs.md has the numbers): $0.08–0.16
// a scout at medium effort, about $1.10 at max effort on Opus. claude stops a call after the turn that
// reaches it, so a call may spend a turn's cost beyond it.
const ScoutBudget = 2.0

const scoutSchema = `{"type":"object","properties":{"suites":{"type":"array","items":{"type":"object",` +
	`"properties":{"name":{"type":"string"},"kind":{"type":"string","enum":["unit","integration","e2e",` +
	`"lint","typecheck","build","other"]},"command":{"type":"string"},"found_in":{"type":"string"},` +
	`"tier":{"type":"string","enum":["fast","full"]},"parallel_safe":{"type":"boolean"},` +
	`"needs":{"type":"array","items":{"type":"string"}}},` +
	`"required":["name","kind","command","found_in","tier","parallel_safe","needs"]}},` +
	`"stack":{"type":"array","items":{"type":"string"}},"note":{"type":"string"}},` +
	`"required":["suites","stack","note"]}`

// SuiteKind is what a suite checks.
type SuiteKind string

// The kinds of suite.
const (
	SuiteUnit        SuiteKind = "unit"
	SuiteIntegration SuiteKind = "integration"
	SuiteE2E         SuiteKind = "e2e"
	SuiteLint        SuiteKind = "lint"
	SuiteTypecheck   SuiteKind = "typecheck"
	SuiteBuild       SuiteKind = "build"
	SuiteOther       SuiteKind = "other"
)

var suiteKinds = []SuiteKind{SuiteUnit, SuiteIntegration, SuiteE2E, SuiteLint, SuiteTypecheck, SuiteBuild,
	SuiteOther}

// Tier is when a suite runs: fast on every merge, full at the end of a run.
type Tier string

// The tiers.
const (
	TierFast Tier = "fast" // unit tests, lint, type checks and one smoke e2e suite
	TierFull Tier = "full" // the rest
)

// Suite is one check the scout found.
type Suite struct {
	Name         string    `json:"name"`
	Kind         SuiteKind `json:"kind"`
	Command      string    `json:"command"`  // as the project runs it, from the repository's root
	FoundIn      string    `json:"found_in"` // path:line
	Tier         Tier      `json:"tier"`
	ParallelSafe bool      `json:"parallel_safe"` // false: a fixed port, a shared database and the like
	Needs        []string  `json:"needs"`         // services, a browser, credentials; empty for none
}

// Scouting is the scout's answer.
type Scouting struct {
	Suites []Suite  `json:"suites"` // empty when none was found
	Stack  []string `json:"stack"`  // the languages, frameworks and tools, such as "Go 1.26"; may be empty
	Note   string   `json:"note"`   // with no suites, what the scout looked at
}

// ScoutFailure is why the scout gave no answer.
type ScoutFailure int

// The ways the scout fails.
const (
	ScoutNoClaude   ScoutFailure = iota + 1 // claude isn't installed
	ScoutTimedOut                           // still running at ScoutLimit
	ScoutStopped                            // its context was cancelled
	ScoutFailed                             // claude failed or reported an error
	ScoutUnreadable                         // the answer isn't the JSON asked for
	ScoutOverBudget                         // stopped at ScoutBudget
)

// ScoutError is the scout's error, whose message is for the user: why there are no suites to choose
// from.
type ScoutError struct {
	Failure ScoutFailure
	Err     error // the cause: for ScoutTimedOut, one errors.Is takes for context.DeadlineExceeded
}

func (e *ScoutError) Error() string {
	switch e.Failure {
	case ScoutNoClaude:
		return "the scout can't run: " + e.Err.Error()
	case ScoutTimedOut:
		return "the scout " + e.Err.Error() // "timed out after 5m"
	case ScoutStopped:
		return "the scout was stopped: " + e.Err.Error()
	case ScoutUnreadable:
		return "the scout's answer is unreadable: " + e.Err.Error()
	case ScoutOverBudget:
		return fmt.Sprintf("the scout was stopped at its budget of $%.2f: %v", ScoutBudget, e.Err)
	}
	return "the scout failed: " + e.Err.Error()
}

func (e *ScoutError) Unwrap() error { return e.Err }

// Scout finds the checks of the repository at root: its test suites, lints, type checks and builds,
// as the scout gives them (init leaves out those that run its runners, and puts scripts/check.sh
// first). It reads the repository with Read, Glob and Grep, for at most
// ScoutLimit and, where claude can cap it, ScoutBudget. Its error is a *ScoutError.
func (g Client) Scout(ctx context.Context, root string) (Scouting, error) {
	return g.scout(ctx, ScoutLimit, root)
}

func (g Client) scout(ctx context.Context, limit time.Duration, root string) (Scouting, error) {
	if why := Unavailable(ctx, g.Bin); why != "" {
		return Scouting{}, &ScoutError{ScoutNoClaude, errors.New(why)}
	}
	// In the repository's root, where Claude Code lets the read-only tools read without asking; safe
	// mode keeps its CLAUDE.md and hooks out, as they are for the other organs.
	s := scout
	s.limit = limit
	r, err := g.askIn(ctx, s, root, g.scoutFlags(ctx), scoutInput)
	if err != nil {
		return Scouting{}, scoutFailure(ctx, err)
	}
	found, err := parseScouting(r)
	if err != nil {
		return Scouting{}, &ScoutError{ScoutUnreadable, err}
	}
	return found, nil
}

// scoutFlags is the scout's flags beyond the other organs', those of them claude lists in its --help: an
// older claude rejects a flag it doesn't know. When --help fails, the scout runs without them, and its
// call says what is wrong.
func (g Client) scoutFlags(ctx context.Context) []string {
	help, err := command.Output(ctx, command.ReadLimit, os.TempDir(), g.Bin, "--help")
	if err != nil {
		return nil
	}
	return append(confinement(help), budget(help)...)
}

// confinement is the flags that keep the scout's reads inside the repository, of those help lists.
// Without them, Claude Code would ask before the tools read outside the working directory, and nobody
// answers in -p, but a setting could answer for it: an allow rule such as Read(//**), or
// bypassPermissions as the default mode, would let the scout read anywhere, and a repository file could
// ask it to. --restricted (Claude Code 2.1.289) confines the file tools to the working directory
// whatever the settings say, and ignores the user, project and local settings files;
// --permission-prompts none denies whatever would prompt.
func confinement(help string) []string {
	var flags []string
	if ListsFlag(help, "--restricted") {
		flags = append(flags, "--restricted")
	}
	if ListsFlag(help, "--permission-prompts") {
		flags = append(flags, "--permission-prompts", "none")
	}
	return flags
}

// budget caps what the scout spends at ScoutBudget, when help lists --max-budget-usd. The time limit
// stops a scout that runs long; this stops one that spends fast, at a higher effort or a dearer model.
func budget(help string) []string {
	if !ListsFlag(help, "--max-budget-usd") {
		return nil
	}
	return []string{"--max-budget-usd", strconv.FormatFloat(ScoutBudget, 'f', 2, 64)}
}

// ListsFlag tells whether help, a command's --help, lists flag: a line that starts with it, or with
// its short form and then it ("-p, --print").
func ListsFlag(help, flag string) bool {
	for line := range strings.Lines(help) {
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == flag || len(f) > 1 && strings.HasSuffix(f[0], ",") && f[1] == flag) {
			return true
		}
	}
	return false
}

// scoutFailure says why ask failed: claude stopped at the time limit, by ctx or at the budget, its
// output unreadable, or claude failed.
func scoutFailure(ctx context.Context, err error) *ScoutError {
	if _, ok := errors.AsType[unreadableOutput](err); ok {
		return &ScoutError{ScoutUnreadable, err}
	}
	if c, ok := errors.AsType[*CallError](err); ok && c.Subtype == SubtypeMaxBudget {
		return &ScoutError{ScoutOverBudget, err}
	}
	e, ok := errors.AsType[*command.Error](err)
	switch {
	case ok && e.Stopped && ctx.Err() == nil:
		return &ScoutError{ScoutTimedOut, e.Err}
	case ok && e.Stopped:
		return &ScoutError{ScoutStopped, e.Err}
	}
	return &ScoutError{ScoutFailed, err}
}

func parseScouting(r Result) (Scouting, error) {
	var s Scouting
	if err := r.decode(&s); err != nil {
		return Scouting{}, err
	}
	suites := []Suite{}
	for _, su := range s.Suites {
		su.Name, su.Command, su.FoundIn = strings.TrimSpace(su.Name), strings.TrimSpace(su.Command),
			strings.TrimSpace(su.FoundIn)
		switch {
		case su.Name == "" || su.Command == "":
			return Scouting{}, fmt.Errorf("a suite with no name or no command: %+v", su)
		case !slices.Contains(suiteKinds, su.Kind):
			return Scouting{}, fmt.Errorf("suite %q has an unknown kind %q", su.Name, su.Kind)
		case su.Tier != TierFast && su.Tier != TierFull:
			return Scouting{}, fmt.Errorf("suite %q has an unknown tier %q", su.Name, su.Tier)
		}
		needs := []string{}
		for _, n := range su.Needs {
			if n = strings.TrimSpace(n); n != "" {
				needs = append(needs, n)
			}
		}
		su.Needs = needs
		suites = append(suites, su)
	}
	stack := []string{}
	for _, item := range s.Stack {
		if item = strings.Join(strings.Fields(item), " "); item != "" && !slices.Contains(stack, item) {
			stack = append(stack, item)
		}
	}
	return Scouting{Suites: suites, Stack: stack, Note: strings.TrimSpace(s.Note)}, nil
}
