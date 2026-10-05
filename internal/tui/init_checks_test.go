package tui

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Stage 2 of init: the scout looks for the project's suites once the stage is reached, then the
// user chooses the checks: the suites found, tests from scratch, or a command typed.

const (
	onScouting = "Looking for the project's suites"
	onFast     = "┃ On every merge (check-fast)"
	onFull     = "┃ At the end of a run (check-full)"
	onUntested = "┃ Also file tickets for untested areas"
	onFullTime = "┃ check-full time limit"
	down       = "\x1b[B" // the down arrow: runes typed at once arrive as one key
	// The choice's options, focused: the stage draws the choice blurred while the scout's field, hidden
	// once the scout ends, still has the focus, and a key typed then reaches the scout's field.
	onFound   = "┃ > Use them as they are"
	onScratch = "┃ > Create from scratch"
	onManual  = "┃ > Manual"
)

// fourSuites are what the scout finds in a project with scripts/check.sh: two fast suites, two full
// ones, the e2e suite taking turns.
var fourSuites = organ.Scouting{Suites: []organ.Suite{
	{Name: "check", Kind: organ.SuiteOther, Command: "scripts/check.sh", FoundIn: "scripts/check.sh:1",
		Tier: organ.TierFast, ParallelSafe: true},
	{Name: "unit tests", Kind: organ.SuiteUnit, Command: "npm test", FoundIn: "package.json:7",
		Tier: organ.TierFast, ParallelSafe: true},
	{Name: "e2e", Kind: organ.SuiteE2E, Command: "npm run e2e", FoundIn: "package.json:9", Tier: organ.TierFull,
		Needs: []string{"postgres"}},
	{Name: "integration", Kind: organ.SuiteIntegration, Command: "npm run integration", FoundIn: "package.json:8",
		Tier: organ.TierFull, ParallelSafe: true},
}}

// finds is a scout that finds what it is given at once.
func finds(s organ.Scouting, err error) func(context.Context) (organ.Scouting, error) {
	return func(context.Context) (organ.Scouting, error) { return s, err }
}

// askChecks runs the init form for ask on a fake terminal, from c.
func askChecks(t *testing.T, c *project.Choice, ask Ask) *formTerminal {
	t.Helper()
	return askOn(t, func(in io.Reader, out io.Writer) error { return AskInit(in, out, c, ask) })
}

// commands are the suites' commands.
func commands(suites *[]project.Suite) []string {
	if suites == nil {
		return nil
	}
	cmds := []string{}
	for _, s := range *suites {
		cmds = append(cmds, s.Command)
	}
	return cmds
}

// runners are the scripts init would write for c in an empty repository: check-fast's, check-full's.
func runners(t *testing.T, c project.Choice) (fast, full string) {
	t.Helper()
	rs, err := project.PlanRunners(t.TempDir(), c)
	if err != nil {
		t.Fatal(err)
	}
	return rs[0].Script, rs[1].Script
}

func TestTheScoutStartsWhenStage2IsReached(t *testing.T) {
	var started atomic.Bool
	release := make(chan struct{})
	scout := func(context.Context) (organ.Scouting, error) {
		started.Store(true)
		<-release
		return fourSuites, nil
	}
	c := project.Choice{Union: true}
	term := askChecks(t, &c, Ask{Union: true, Check: true, Scout: scout})
	term.waitFor(t, onUnion)
	if started.Load() {
		t.Fatal("the scout started in stage 1")
	}
	term.typeKeys(t, "\r")
	term.typeSteps(t, []keysOn{{checksHeader, ""}, {onScouting, ""}, {"Esc: skip and type a check yourself", ""}})
	if !started.Load() {
		t.Error("the scout hadn't started in stage 2")
	}
	if screen := term.screen.String(); !strings.Contains(screen, "0:0") {
		t.Errorf("no elapsed time beside the spinner:\n%s", screen)
	}
	close(release)
	term.typeSteps(t, []keysOn{{onChoice, "\x03"}})
	_ = term.end(t)
}

func TestTheChoiceStartsOnUseThemWithSuitesAndOnScratchWithout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		found   organ.Scouting
		starts  string
		because string
	}{
		{"suites found", fourSuites, "> Use them as they are", "The scout found 4 suites."},
		{"none found", organ.Scouting{Suites: []organ.Suite{}, Note: "No manifest, no CI."},
			"> Create from scratch with the create-check-suite skill", "The scout found no suites. No manifest, no CI."},
	} {
		c := project.Choice{}
		term := askChecks(t, &c, Ask{Check: true, Scout: finds(tc.found, nil)})
		term.typeSteps(t, []keysOn{{onChoice, ""}, {tc.starts, ""}, {tc.because, "\x03"}})
		_ = term.end(t)
	}
}

func TestUseThemAsTheyAreWritesTheSuitesTicked(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fastKeys   string
		fast, full []string
	}{
		{"as the scout says", "\r", []string{"scripts/check.sh", "npm test"},
			[]string{"npm run e2e", "npm run integration"}},
		// e2e ticked for every merge too runs there, and not again in check-full.
		{"e2e on every merge", down + down + "x\r", []string{"scripts/check.sh", "npm test", "npm run e2e"},
			[]string{"npm run integration"}},
	} {
		c := project.Choice{}
		term := askChecks(t, &c, Ask{Check: true, Scout: finds(fourSuites, nil)})
		term.typeSteps(t, []keysOn{{onFound, "\r"}, {onFast, tc.fastKeys}, {onFull, "\r"},
			{onUntested, "\r"}})
		if err := term.end(t); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.Tests != project.TestsFound || c.FileUntested || c.FilesTestWork() {
			t.Errorf("%s: tests %v, untested %v", tc.name, c.Tests, c.FileUntested)
		}
		if got := commands(c.Fast); !slices.Equal(got, tc.fast) {
			t.Errorf("%s: check-fast %q, want %q", tc.name, got, tc.fast)
		}
		if got := commands(c.Full); !slices.Equal(got, tc.full) {
			t.Errorf("%s: check-full %q, want %q", tc.name, got, tc.full)
		}
		fast, full := runners(t, c)
		for _, cmd := range tc.fast {
			if !strings.Contains(fast, "\n"+cmd+"\n") {
				t.Errorf("%s: check-fast.sh doesn't run %q:\n%s", tc.name, cmd, fast)
			}
		}
		if !strings.Contains(full, "\n"+project.FastRunner+"\n") || !strings.Contains(full, "\nnpm run integration\n") {
			t.Errorf("%s: check-full.sh:\n%s", tc.name, full)
		}
		e2eIn := map[bool]string{true: fast, false: full}[len(tc.fast) == 3]
		if !strings.Contains(e2eIn, "lock e2e\nnpm run e2e\nunlock\n") {
			t.Errorf("%s: e2e doesn't take turns:\n%s", tc.name, e2eIn)
		}
	}
}

func TestUseThemAsTheyAreCanFileTicketsForUntestedAreas(t *testing.T) {
	c := project.Choice{}
	term := askChecks(t, &c, Ask{Check: true, Scout: finds(fourSuites, nil)})
	term.typeSteps(t, []keysOn{{onFound, "\r"}, {onFast, "\r"}, {onFull, "\r"}, {onUntested, "y"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if !c.FileUntested || !c.FilesTestWork() {
		t.Errorf("untested %v, files test work %v; want both", c.FileUntested, c.FilesTestWork())
	}
}

func TestCreateFromScratchWritesRunnersThatCheckNothing(t *testing.T) {
	c := project.Choice{}
	term := askChecks(t, &c, Ask{Check: true, Timeout: true, FullTimeout: true,
		Scout: finds(organ.Scouting{Suites: []organ.Suite{}}, nil)})
	term.typeSteps(t, []keysOn{{onScratch, "\r"}, {onTimeout, "\r"}, {onFullTime, "\x7f\x7f\x7f90m\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if c.Tests != project.TestsScratch || !c.FilesTestWork() {
		t.Errorf("tests %v; want from scratch", c.Tests)
	}
	if c.Fast == nil || len(*c.Fast) != 0 || c.Full == nil || len(*c.Full) != 0 {
		t.Errorf("check-fast %q, check-full %q; want none", commands(c.Fast), commands(c.Full))
	}
	fast, full := runners(t, c)
	if !strings.HasSuffix(fast, "\nexit 0\n") || !strings.HasSuffix(full, "\nexit 0\n") {
		t.Errorf("runners that check something:\n%s\n%s", fast, full)
	}
	if c.CheckFastTimeout != project.DefaultCheckTimeoutText || c.CheckFullTimeout != "90m" {
		t.Errorf("time limits %q, %q; want %q, 90m", c.CheckFastTimeout, c.CheckFullTimeout, project.DefaultCheckTimeoutText)
	}
}

func TestManualWritesTheCommandTyped(t *testing.T) {
	for _, tc := range []struct {
		typed string
		want  []string
	}{
		{"make check", []string{"make check"}},
		{"", []string{}}, // a runner that checks nothing
	} {
		c := project.Choice{Fast: &[]project.Suite{{Command: "make old"}}}
		term := askChecks(t, &c, Ask{Check: true, Scout: finds(fourSuites, nil)})
		term.typeSteps(t, []keysOn{{onFound, down + down + "\r"},
			{onCheck, "\x15" + tc.typed + "\r"}}) // Ctrl+U clears what is there
		if err := term.end(t); err != nil {
			t.Fatal(err)
		}
		if got := commands(c.Fast); c.Tests != project.TestsManual || !slices.Equal(got, tc.want) || !c.ReplaceFast {
			t.Errorf("typed %q: tests %v, check-fast %q, replace %v", tc.typed, c.Tests, got, c.ReplaceFast)
		}
		if fast, _ := runners(t, c); len(tc.want) == 0 && !strings.HasSuffix(fast, "\nexit 0\n") {
			t.Errorf("typed nothing: check-fast.sh checks something:\n%s", fast)
		}
	}
}

func TestAScoutThatFailsLandsOnManualWithWhy(t *testing.T) {
	c := project.Choice{}
	failed := &organ.ScoutError{Failure: organ.ScoutTimedOut, Err: errors.New("timed out after 5m")}
	term := askChecks(t, &c, Ask{Check: true, Scout: finds(organ.Scouting{}, failed)})
	term.typeSteps(t, []keysOn{{"The scout timed out after 5m.", ""}, {onManual, "\r"}, {onCheck, "make check\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if got := commands(c.Fast); c.Tests != project.TestsManual || !slices.Equal(got, []string{"make check"}) {
		t.Errorf("tests %v, check-fast %q", c.Tests, got)
	}
}

func TestEscSkipsTheScoutForManual(t *testing.T) {
	stopped := make(chan error, 1)
	scout := func(ctx context.Context) (organ.Scouting, error) {
		<-ctx.Done()
		stopped <- context.Cause(ctx)
		return organ.Scouting{}, ctx.Err()
	}
	c := project.Choice{}
	term := askChecks(t, &c, Ask{Check: true, Scout: scout})
	term.typeSteps(t, []keysOn{{onScouting, "\x1b"}, {"The search was skipped with Esc.", ""}, {onManual, "\r"},
		{onCheck, "make check\r"}})
	if err := term.end(t); err != nil {
		t.Fatalf("Esc while the scout looks: %v, want the form to go on", err)
	}
	if cause := <-stopped; !errors.Is(cause, errSkipped) {
		t.Errorf("the scout stopped by %v, want %v", cause, errSkipped)
	}
	if got := commands(c.Fast); c.Tests != project.TestsManual || !slices.Equal(got, []string{"make check"}) {
		t.Errorf("tests %v, check-fast %q", c.Tests, got)
	}
}

func TestCtrlCWhileTheScoutLooksCancelsAndStopsIt(t *testing.T) {
	stopped := make(chan struct{})
	scout := func(ctx context.Context) (organ.Scouting, error) {
		<-ctx.Done()
		close(stopped)
		return organ.Scouting{}, ctx.Err()
	}
	c := project.Choice{}
	term := askChecks(t, &c, Ask{Check: true, Scout: scout})
	term.typeSteps(t, []keysOn{{onScouting, "\x03"}})
	if err := term.end(t); err == nil {
		t.Fatal("Ctrl+C while the scout looks: the form went on")
	}
	select {
	case <-stopped:
	default:
		t.Error("AskInit returned with the scout still looking")
	}
}

func TestKeepOrReplaceARunnerThatDiffers(t *testing.T) {
	old := project.FastScript([]project.Suite{{Command: "make old"}})
	for _, tc := range []struct {
		keys    string
		replace bool
	}{{"\r", false}, {down + "\r", true}} {
		c := project.Choice{}
		ask := Ask{Check: true, Scout: finds(fourSuites, nil),
			Runners: []project.Runner{{Path: project.FastRunner, Script: old, Exists: true}}}
		term := askChecks(t, &c, ask)
		term.typeSteps(t, []keysOn{{onFound, "\r"}, {onFast, "\r"}, {onFull, "\r"}, {onUntested, "\r"},
			{"┃ scripts/check-fast.sh is there, and differs from the checks chosen", ""}, {"make old", tc.keys}})
		if err := term.end(t); err != nil {
			t.Fatal(err)
		}
		if c.ReplaceFast != tc.replace || c.ReplaceFull {
			t.Errorf("%q: replace check-fast %v, check-full %v; want %v, false", tc.keys, c.ReplaceFast, c.ReplaceFull,
				tc.replace)
		}
	}
}

func TestARunnerThatIsTheSameIsntAskedAbout(t *testing.T) {
	same := project.FastScript([]project.Suite{{Name: "check", Command: "scripts/check.sh", FoundIn: "scripts/check.sh:1"},
		{Name: "unit tests", Command: "npm test", FoundIn: "package.json:7"}})
	c := project.Choice{}
	ask := Ask{Check: true, Timeout: true, Scout: finds(fourSuites, nil),
		Runners: []project.Runner{{Path: project.FastRunner, Script: same, Exists: true}}}
	term := askChecks(t, &c, ask)
	term.typeSteps(t, []keysOn{{onFound, "\r"}, {onFast, "\r"}, {onFull, "\r"}, {onUntested, "\r"},
		{onTimeout, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if screen := term.screen.String(); strings.Contains(screen, "is there, and differs") {
		t.Errorf("asked about a runner that is the same:\n%s", screen)
	}
}

func TestKeepOrReplaceTheSkillThatDiffers(t *testing.T) {
	c := project.Choice{}
	ask := Ask{Check: true, Scout: finds(organ.Scouting{Suites: []organ.Suite{}}, nil),
		Skill: project.SkillPlan{Dir: ".claude/skills/create-check-suite", Exists: true, Differs: true}}
	term := askChecks(t, &c, ask)
	term.typeSteps(t, []keysOn{{onScratch, "\r"},
		{"┃ .claude/skills/create-check-suite/ is there, and differs from orchestra's", down + "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if !c.ReplaceSkill {
		t.Error("the skill is kept; want it replaced")
	}
}

func TestAccessibleStage2SaysWhatTheScoutFound(t *testing.T) {
	t.Setenv("TERM", "dumb")
	c := project.Choice{}
	var out strings.Builder
	failed := &organ.ScoutError{Failure: organ.ScoutNoClaude, Err: errors.New("claude not found")}
	// The choice: Manual, the default; the check command.
	if err := AskInit(typed("3", "make check"), &out, &c, Ask{Check: true, Scout: finds(organ.Scouting{}, failed)}); err != nil {
		t.Fatal(err)
	}
	screen := ansi.Strip(out.String())
	if !strings.Contains(screen, "The scout can't run: claude not found.") {
		t.Errorf("no reason:\n%s", screen)
	}
	if got := commands(c.Fast); c.Tests != project.TestsManual || !slices.Equal(got, []string{"make check"}) {
		t.Errorf("tests %v, check-fast %q", c.Tests, got)
	}
}
