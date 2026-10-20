package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// stopAfter has steps take one step each, labelled with its name, in place of what they do, the
// context ending as the one at stop ends, as a stop signal ends it. It returns them and the context,
// and records the names of those that ran in ran.
func stopAfter(t *testing.T, steps []setupStep, stop int, ran *[]string) ([]setupStep, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	faked := slices.Clone(steps)
	for i := range faked {
		name := faked[i].name
		faked[i].run = func(context.Context) ([]project.Step, error) {
			*ran = append(*ran, name)
			if i == stop {
				cancel()
			}
			return []project.Step{{Kind: project.StepDone, Label: name}}, nil
		}
	}
	return faked, ctx
}

// runSteps checks for a stop after each step: it takes none after it, and says where it stopped, the
// steps so far and the words of the step it stopped at. A stop comes before that step's error.
func TestRunStepsStopsAfterEachStepInTurn(t *testing.T) {
	t.Parallel()
	words := func(took []project.Step) string { return " after " + took[0].Label }
	steps := []setupStep{{name: "one", stopped: words}, {name: "two"}, {name: "three", stopped: words}}
	for stop, want := range []string{" after one", "", " after three"} {
		var ran []string
		faked, ctx := stopAfter(t, steps, stop, &ran)
		did, halt := runSteps(ctx, faked)
		names := []string{"one", "two", "three"}[:stop+1]
		if !slices.Equal(ran, names) || len(did) != stop+1 {
			t.Errorf("stopped at %d: ran %v, took %v", stop, ran, did)
		}
		if halt == nil || !halt.stop || halt.step != names[stop] || halt.words != want || halt.err != nil {
			t.Errorf("stopped at %d: halt %+v, want a stop at %q saying %q", stop, halt, names[stop], want)
		}
	}

	failure := errors.New("no")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failing := []setupStep{{name: "one", run: func(context.Context) ([]project.Step, error) {
		cancel()
		return nil, failure
	}}}
	if _, halt := runSteps(ctx, failing); halt == nil || !halt.stop {
		t.Errorf("a stop that fails its step too: halt %+v, want a stop", halt)
	}
	failing[0].run = func(context.Context) ([]project.Step, error) { return nil, failure }
	if _, halt := runSteps(context.Background(), failing); halt == nil || halt.stop || !errors.Is(halt.err, failure) {
		t.Errorf("a step that fails: halt %+v, want its error", halt)
	}
	if did, halt := runSteps(context.Background(), steps[:0]); did != nil || halt != nil {
		t.Errorf("no steps: %v, %+v", did, halt)
	}
}

// A stop after each of init's steps in turn ends init with exit code 130, the steps so far and what
// it did, in the words of the step it stopped at.
func TestInitStoppedAfterEachStep(t *testing.T) {
	t.Parallel()
	filing := project.Choice{Tests: project.TestsScratch}
	for _, tc := range []struct {
		choice project.Choice
		stop   int
		want   string
	}{
		{project.Choice{}, 0, "orchestra init: stopped\n"},
		{project.Choice{}, 1, "orchestra init: stopped once it had written the files above\n"},
		{filing, 1, "orchestra init: stopped once it had written the files above, before filing the test work: " +
			"orchestra init again files it\n"},
		{filing, 2, "orchestra init: stopped while filing the test work: orchestra init again files what is missing\n"},
	} {
		steps := initSteps(t.TempDir(), tc.choice, false, func(string) string { return "" }, func(string) {})
		var ran []string
		faked, ctx := stopAfter(t, steps, tc.stop, &ran)
		did, halt := runSteps(ctx, faked)
		if halt == nil {
			t.Fatalf("stopped after %q: no halt", steps[tc.stop].name)
		}
		var out, errOut strings.Builder
		code := initHalted(tui.NewInitScreen(&out, "dumb"), &errOut, did, halt)
		if code != dispatch.ExitInterrupted || errOut.String() != tc.want {
			t.Errorf("stopped after %q: exit %d, stderr %q; want %d, %q", steps[tc.stop].name, code, errOut.String(),
				dispatch.ExitInterrupted, tc.want)
		}
		for _, name := range ran {
			if !strings.Contains(out.String(), name) {
				t.Errorf("stopped after %q: stdout lacks the step %q:\n%s", steps[tc.stop].name, name, out.String())
			}
		}
	}

	// A stop before Apply wrote anything says so.
	steps := initSteps(t.TempDir(), project.Choice{}, false, func(string) string { return "" }, func(string) {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	steps[0].run = func(context.Context) ([]project.Step, error) { return nil, nil }
	steps[1].run = func(context.Context) ([]project.Step, error) {
		cancel()
		return nil, context.Canceled
	}
	did, halt := runSteps(ctx, steps)
	var out, errOut strings.Builder
	if code := initHalted(tui.NewInitScreen(&out, "dumb"), &errOut, did, halt); code != dispatch.ExitInterrupted ||
		errOut.String() != "orchestra init: stopped before writing .orchestra/\n" {
		t.Errorf("stopped as Apply started: exit %d, stderr %q", code, errOut.String())
	}
}

// A stop after each of a spec run's setup steps in turn ends the run with exit code 130, saying it
// stopped with nothing committed; it lists what it did once it has written the setup, and only then.
func TestSpecSetupStoppedAfterEachStep(t *testing.T) {
	t.Parallel()
	var out, errOut strings.Builder
	u := specSetup{repo: t.TempDir(), out: &out, err: &errOut}
	var committed specCommit
	steps := u.steps(nil, &committed)
	for stop, s := range steps {
		out.Reset()
		errOut.Reset()
		var ran []string
		faked, ctx := stopAfter(t, steps, stop, &ran)
		did, halt := runSteps(ctx, faked)
		if code := u.halted(did, halt, "spec.md", committed); code != dispatch.ExitInterrupted {
			t.Errorf("stopped after %q: exit %d, want %d", s.name, code, dispatch.ExitInterrupted)
		}
		if want := "orchestra: stopped while setting the repository up for the spec; nothing was committed " +
			"(see git status).\n"; errOut.String() != want {
			t.Errorf("stopped after %q: stderr %q, want %q", s.name, errOut.String(), want)
		}
		listed := s.name == specStepCommit
		for _, name := range ran {
			if strings.Contains(out.String(), "  - "+name+"\n") != listed {
				t.Errorf("stopped after %q: the step %q listed: %v, want %v; stdout:\n%s", s.name, name, !listed, listed,
					out.String())
			}
		}
	}
}
