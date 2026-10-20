package main

import (
	"context"
	"fmt"
	"io"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// runInitUpdate is orchestra init --update in the repository around dir: it brings the worker prompt
// and the verifier up to date with orchestra's templates (project.UpdateTemplates), with nothing else
// of init, and shows what it changed as a diff, for the user to read before committing it. It returns
// the exit code.
func runInitUpdate(
	ctx context.Context, dir string, f initFlags, getenv func(string) string, stdout, stderr io.Writer,
) int {
	fail := func(why any) int {
		fmt.Fprintln(stderr, "orchestra init:", why)
		return dispatch.ExitSetup
	}
	if f.force || f.setsUp() {
		return fail("--update takes no other flag: it changes only the worker prompt, the verifier and the " +
			"templates' versions in settings.json (run orchestra init without it for the rest)")
	}
	repo, _, err := openInitRepo(ctx, dir)
	if err != nil {
		return fail(err)
	}
	lock, refusal := lockInit(ctx, repo)
	if refusal != "" {
		return fail(refusal)
	}
	defer func() { _ = lock.Close() }() // only releases it; init exiting would too
	steps, updates, err := project.UpdateTemplates(repo)
	ui := tui.NewInitScreen(stdout, getenv("TERM"))
	ui.Header(repo)
	ui.Steps(steps)
	if err != nil {
		return fail(err)
	}
	for _, u := range updates {
		ui.Diff(u.Diff())
	}
	ui.Next(project.UpdateNext(steps, updates))
	return dispatch.ExitOK
}

// setsUp reports whether f gives any of init's flags that set the project up, which --update doesn't.
func (f initFlags) setsUp() bool {
	return f.checkFast.Given || f.checkFull.Given || f.setup.Given || f.mcp.Given || f.prefix.Given ||
		f.fastTimeout.Given || f.fullTimeout.Given || f.concurrent.Given || f.union.Given || f.install.Given ||
		f.verifier.Given
}
