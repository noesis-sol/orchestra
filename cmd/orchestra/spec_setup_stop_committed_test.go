package main

import (
	"context"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A stop that comes as a spec run's setup commit succeeds says what the commit did and that the run
// stopped once it had committed, with exit code 130, not that nothing was committed.
func TestSpecSetupStoppedAsItCommits(t *testing.T) {
	t.Parallel()
	var out, errOut strings.Builder
	u := specSetup{repo: t.TempDir(), out: &out, err: &errOut}
	var committed specCommit
	steps := u.steps(nil, &committed)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := range steps {
		name := steps[i].name
		steps[i].run = func(context.Context) ([]project.Step, error) {
			if name != specStepCommit {
				return []project.Step{{Kind: project.StepDone, Label: name}}, nil
			}
			committed = specCommit{hash: "0123456789abcdef", paths: []string{"spec.md", ".orchestra/settings.json"}}
			cancel()
			return nil, nil
		}
	}
	did, halt := runSteps(ctx, steps)
	if halt == nil || !halt.stop || halt.step != specStepCommit {
		t.Fatalf("halt %+v, want a stop at %q", halt, specStepCommit)
	}
	if code := u.halted(did, halt, "spec.md", committed); code != dispatch.ExitInterrupted {
		t.Errorf("exit %d, want %d", code, dispatch.ExitInterrupted)
	}
	if want := "orchestra: stopped once it had set the repository up for the spec and committed it " +
		"(see git log).\n"; errOut.String() != want {
		t.Errorf("stderr %q, want %q", errOut.String(), want)
	}
	if want := "committed the setup with the spec spec.md as 0123456789ab (2 files)\n"; !strings.HasSuffix(out.String(),
		want) {
		t.Errorf("stdout doesn't end with %q:\n%s", want, out.String())
	}
	for _, name := range []string{specStepBeads, specStepApply} {
		if !strings.Contains(out.String(), "  - "+name+"\n") {
			t.Errorf("stdout lacks the step %q:\n%s", name, out.String())
		}
	}
}
