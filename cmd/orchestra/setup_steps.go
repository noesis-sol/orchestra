package main

import (
	"context"

	"github.com/noesis-sol/orchestra/internal/project"
)

// setupStep is one step of setting a repository up, as orchestra init and a spec run take them in
// turn through runSteps, which checks for a stop after each.
type setupStep struct {
	name string // what the step sets up, as setupHalt names it
	// run takes the step, and returns the steps it took and the error that ends the setup there.
	run func(ctx context.Context) ([]project.Step, error)
	// stopped is what a stop during the step says, given the steps it took: what follows "stopped"
	// in init's words. nil says nothing more.
	stopped func(took []project.Step) string
}

// setupHalt is where runSteps ended short of its last step's end: a stop, or a step's error.
type setupHalt struct {
	step  string // the step's name
	stop  bool   // the context ended, as a stop signal ends it
	words string // where stop is, what the step says of a stop during it
	err   error  // where stop isn't, the step's error
}

// runSteps takes steps in turn until one fails or ctx ends, checking ctx after each, and returns the
// steps they took, and where they halted, nil once all have run. A stop comes before the error of
// the step it ended: a step that ctx ends fails too, for that.
func runSteps(ctx context.Context, steps []setupStep) (did []project.Step, halt *setupHalt) {
	for _, s := range steps {
		took, err := s.run(ctx)
		did = append(did, took...)
		switch {
		case ctx.Err() != nil:
			halt = &setupHalt{step: s.name, stop: true}
			if s.stopped != nil {
				halt.words = s.stopped(took)
			}
			return did, halt
		case err != nil:
			return did, &setupHalt{step: s.name, err: err}
		}
	}
	return did, nil
}
