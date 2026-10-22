package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"

	"github.com/noesis-sol/orchestra/internal/git"
)

// Applier is a part of what init sets up in the repository at repo, once Beads is: it writes what the
// choice calls for, and returns the steps for init's summary, none where it has nothing to say. Its
// error stops init, the steps before it standing.
type Applier func(ctx context.Context, repo string, c Choice) ([]Step, error)

// Apply sets the repository at repo up as the choice says: the worker prompt, replacing one that is
// there where force is set; the runners; the settings; the README's section on what to install
// (ApplyReadme); the changelog's union; the create-check-suite skill; and the verifier, which reads
// the worker prompt and the runners written before it. Last, the settings record the versions of
// the templates it wrote the worker prompt and the verifier from. It returns each one's steps.
//
// It writes the whole setup or none of it: a setup half written, the worker prompt without the
// settings, would lack the merge check. Where one of them fails, it puts back what it had written
// (see setupPaths), and returns the error and no steps, the repository set up as it was before, or
// not at all. Once it has started, it goes on though ctx ends meanwhile: what it runs takes little
// time, its commands each a short time limit of their own. Where ctx has ended before it starts, it
// writes nothing and returns ctx's error.
func Apply(ctx context.Context, repo string, c Choice, force bool) ([]Step, error) {
	return applyAll(ctx, repo, c, setupAppliers(force))
}

// setupAppliers are the appliers Apply runs, in turn.
func setupAppliers(force bool) []Applier {
	return []Applier{initPrompt(force), ApplyRunners, ApplySettings, ApplyReadme, ApplyUnion, ApplySkill, ApplyVerifier}
}

// applyAll is Apply with the appliers it runs, for the tests to fail each in turn.
func applyAll(ctx context.Context, repo string, c Choice, appliers []Applier) ([]Step, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx = context.WithoutCancel(ctx)
	before, err := takeSnapshot(repo, c)
	if err != nil {
		return nil, fmt.Errorf("cannot tell what the setup would replace: %w", err)
	}
	var steps []Step
	for _, apply := range appliers {
		s, err := apply(ctx, repo, c)
		steps = append(steps, s...)
		if err != nil {
			return putBack(ctx, before, steps, err)
		}
	}
	if err := recordTemplates(repo, steps); err != nil {
		return putBack(ctx, before, steps, err)
	}
	return steps, nil
}

// putBack puts the setup back as the snapshot before has it, once an applier has failed with err,
// and returns no steps and err. Where some of it can't be put back, it returns the steps, which say
// what was written, and both errors.
func putBack(ctx context.Context, before snapshot, steps []Step, err error) ([]Step, error) {
	if rerr := before.restore(ctx); rerr != nil {
		return steps, errors.Join(err, fmt.Errorf("cannot put the setup back as it was: %w", rerr))
	}
	return nil, fmt.Errorf("%w (the setup is as it was before)", err)
}

// initPrompt is Init as an Applier: the worker prompt, with the runner as its check command and the
// choice's suites.
func initPrompt(force bool) Applier {
	return func(ctx context.Context, repo string, c Choice) ([]Step, error) {
		return Init(ctx, repo, PromptChecks{Check: FastRunner, Fast: c.Fast, Full: c.Full}, force)
	}
}

// CommitSetUp commits what a spec run wrote setting up the repository whose main checkout is repo,
// with the spec file, and returns the commit and the paths it holds, sorted. What it wrote is each
// file whose state differs from before, git's status as git.Git.Changes gave it before the setup;
// the spec is kept where CommitSpec keeps it, a file outside the repository copied there first. In
// a repository with no commit yet it is the first.
//
// Like CommitSpec, it is called before a run's loop starts, with the run lock held.
func CommitSetUp(ctx context.Context, repo string, before map[string]string, file string) (string, []string, error) {
	text, err := ReadSpec(file)
	if err != nil {
		return "", nil, err
	}
	rel, inside := SpecPath(repo, file)
	if inside && InGitDir(rel) {
		return "", nil, ErrSpecInGitDir
	}
	source := ""
	if !inside {
		if err := copyOutside(ctx, repo, file, rel, text); err != nil {
			return "", nil, err
		}
		source = sourceTrailer(file)
	}
	g := git.Git{}
	paths := []string{rel}
	after, err := g.Changes(ctx, repo)
	if err != nil { // what the setup wrote would be left out of the commit
		return "", nil, fmt.Errorf("cannot tell what the setup wrote: %w", err)
	}
	for p, st := range after {
		if before[p] != st && p != rel {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	sum := sha256.Sum256(text)
	msg := fmt.Sprintf("orchestra: set up for spec %s\n\norchestra --spec set up this repository, which orchestra init "+
		"hadn't, as init does without a terminal, and added the spec it builds, at %s, where the workers on its "+
		"tickets can read it.\n\nsha256 %s\n%s", path.Base(rel), rel, hex.EncodeToString(sum[:]), source)
	commit, err := g.CommitPaths(ctx, repo, msg, paths...)
	if err != nil {
		return "", nil, fmt.Errorf("cannot commit the setup: %w", err)
	}
	return commit, paths, nil
}
