package project

import "context"

// Applier is a part of what init sets up in the repository at repo, once Beads is: it writes what the
// choice calls for, and returns the steps for init's summary, none where it has nothing to say. Its
// error stops init, the steps before it standing.
type Applier func(ctx context.Context, repo string, c Choice) ([]Step, error)

// Apply sets the repository at repo up as the choice says: the worker prompt, replacing one that is
// there where force is set; the runners; the settings; the changelog's union; the create-check-suite
// skill; and the verifier, which reads the worker prompt and the runners written before it. It
// returns each one's steps, stopping at the first error with the steps so far.
func Apply(ctx context.Context, repo string, c Choice, force bool) ([]Step, error) {
	var steps []Step
	for _, apply := range []Applier{initPrompt(force), ApplyRunners, ApplySettings, ApplyUnion, ApplySkill,
		ApplyVerifier} {
		s, err := apply(ctx, repo, c)
		steps = append(steps, s...)
		if err != nil {
			return steps, err
		}
	}
	return steps, nil
}

// initPrompt is Init as an Applier: the worker prompt, with the runner as its check command.
func initPrompt(force bool) Applier {
	return func(ctx context.Context, repo string, _ Choice) ([]Step, error) {
		return Init(ctx, repo, FastRunner, force)
	}
}
