package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if !inside {
		if err := copySpec(repo, rel, text); err != nil {
			return "", nil, err
		}
	}
	g := git.Git{}
	paths := []string{rel}
	for p, st := range g.Changes(ctx, repo) {
		if before[p] != st && p != rel {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	sum := sha256.Sum256(text)
	msg := fmt.Sprintf("orchestra: set up for spec %s\n\norchestra --spec set up this repository, which orchestra init "+
		"hadn't, as init does without a terminal, and added the spec it builds, at %s, where the workers on its "+
		"tickets can read it.\n\nsha256 %s\n", path.Base(rel), rel, hex.EncodeToString(sum[:]))
	commit, err := g.CommitPaths(ctx, repo, msg, paths...)
	if err != nil {
		return "", nil, fmt.Errorf("cannot commit the setup: %w", err)
	}
	return commit, paths, nil
}
