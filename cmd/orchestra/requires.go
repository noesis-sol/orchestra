package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/requires"
)

// requiresSynopsis is orchestra requires' flags, as its usage line gives them (see subcommand).
const requiresSynopsis = "[--no-install]"

// runRequires checks the requirements of the project around e's dir (requires in
// .orchestra/settings.json) as a run does as it starts, installing what it can unless --no-install
// says not to, and prints how each was found: for a new contributor's machine, or a worker that adds
// one. It returns the exit code: 0 when every requirement is met, 2 when one is missing or the list is
// refused. A stop signal, caught as its stopPolicy says, stops the check or install under way.
func runRequires(_ context.Context, args []string, e cmdEnv, stop stopCatch) int {
	stdout, stderr := e.stdout, e.stderr
	fs := flag.NewFlagSet("requires", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noInstall := fs.Bool("no-install", false, "only check: run no install, and list what is missing")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usageLines("Usage: ", "requires", requiresSynopsis)+"\n"+
			"Check what this machine must have for the project, the 'requires' list in "+project.SettingsFile+",\n"+
			"as each run does before its first ticket: run each entry's check, and for one that fails, its\n"+
			"install, then check again. It prints each entry's state; what is still missing says its manual\n"+
			"step, or why its install failed and where its output is.\n\n")
		printFlags(fs)
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 every requirement met, 2 one missing or the list refused, "+
			"130 Ctrl+C.\n")
	}
	if code, ok := parseArgs(fs, args, "requires"); !ok {
		return code
	}
	fail := func(why any) int {
		fmt.Fprintln(stderr, "orchestra requires:", why)
		return dispatch.ExitSetup
	}
	repo, err := git.Git{}.TopLevel(stop.ctx, e.dir)
	if err != nil {
		return fail("not inside a git repository")
	}
	settings, _, err := project.LoadSettings(repo)
	if err != nil {
		return fail(err)
	}
	list, err := project.ResolveRequires(settings)
	if err != nil {
		return fail(err)
	}
	limit, err := project.ResolveCheckTimeout(project.Given[time.Duration]{}, settings)
	if err != nil {
		return fail(err)
	}
	if len(list) == 0 {
		fmt.Fprintf(stdout, "No requirements: %s has no 'requires' list.\n", project.SettingsFile)
		return dispatch.ExitOK
	}
	report := requires.Check(stop.ctx, list, requires.Options{Dir: repo, InstallLimit: limit, NoInstall: *noInstall})
	printRequires(stdout, report)
	switch {
	case stop.stopped():
		fmt.Fprintln(stderr, "orchestra requires: stopped.")
		return dispatch.ExitInterrupted
	case report.MissingLine() != "":
		fmt.Fprintln(stderr, report.MissingLine())
		return dispatch.ExitSetup
	}
	return dispatch.ExitOK
}

// printRequires prints how each requirement in report was found, a line each.
func printRequires(out io.Writer, report requires.Report) {
	for _, res := range report {
		fmt.Fprintf(out, "%s: %s\n", res.Name, res.Says())
	}
}
