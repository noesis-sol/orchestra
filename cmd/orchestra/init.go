package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
	"golang.org/x/term"
)

// runInit sets up .orchestra/ in the repository around dir, asking what it needs when run in a
// terminal, and returns the exit code. It reads the form's answers from stdin and prints to stdout
// and stderr.
func runInit(ctx context.Context, dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.String("check", "", "the project's check command (lint, build, tests)")
	checkTimeout := fs.Duration("check-timeout", 0, "how long the check command may run on a rebased ticket, "+
		"e.g. 5m (asked when omitted; default "+project.DefaultCheckTimeoutText+")")
	force := fs.Bool("force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	union := fs.Bool("changelog-union", false, "add 'CHANGELOG.md merge=union' to .gitattributes, "+
		"so two tickets' changelog entries don't conflict (asked when omitted; =false declines)")
	var concurrent int
	fs.IntVar(&concurrent, "concurrent", 0, "tickets to run at the same time by default (asked when omitted)")
	fs.IntVar(&concurrent, "c", 0, "shorthand for --concurrent")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra init [--check \"<command>\"] [--check-timeout D] [--concurrent N] "+
			"[--changelog-union] [--force]\n\n"+
			"Set up .orchestra/ in this repository: the worker prompt (from the built-in template, or moved\n"+
			"from .claude/worker-prompt.md), settings.json (the check command, its time limit and how many\n"+
			"tickets run at the same time), a .gitignore for the log, reports and per-ticket files, and a\n"+
			"check of what orchestra needs. Where the project keeps a CHANGELOG.md, it offers to merge it by\n"+
			"union in .gitattributes. In a terminal it asks for anything the flags don't give.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return dispatch.ExitOK
		}
		return dispatch.ExitSetup
	}
	if rest := fs.Args(); len(rest) > 0 {
		fmt.Fprintf(stderr, "orchestra init: unexpected argument %q (see orchestra init -h)\n", rest[0])
		return dispatch.ExitSetup
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	checkGiven, timeoutGiven, unionGiven := given["check"], given["check-timeout"], given["changelog-union"]
	concurrentGiven := given["concurrent"] || given["c"]
	if timeoutGiven && *checkTimeout <= 0 {
		fmt.Fprintln(stderr, "orchestra init: --check-timeout must be a positive duration such as 5m")
		return dispatch.ExitSetup
	}
	if concurrentGiven && (concurrent < 1 || concurrent > project.MaxConcurrency) {
		fmt.Fprintf(stderr, "orchestra init: --concurrent must be between 1 and %d\n", project.MaxConcurrency)
		return dispatch.ExitSetup
	}

	out, err := command.Output(ctx, command.ReadLimit, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintln(stderr, "orchestra init: not inside a git repository")
		return dispatch.ExitSetup
	}
	repo := strings.TrimSpace(out)
	existing, _, err := project.LoadSettings(repo)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra init:", err)
		return dispatch.ExitSetup
	}
	promptText, _ := os.ReadFile(project.Locate(repo).Prompt) // no prompt yet: no check command to find in it
	choice := project.DefaultChoice(existing, string(promptText))
	if checkGiven {
		choice.Check, choice.CheckFrom = strings.TrimSpace(*check), "--check"
	}
	if concurrentGiven {
		choice.Concurrent, choice.Unasked = concurrent, false
	}
	if timeoutGiven {
		choice.CheckTimeout, choice.ReplacedTimeout = dispatch.ShortDuration(*checkTimeout), ""
	}
	askUnion := !unionGiven && project.OffersUnion(ctx, repo)
	choice.Union = *union || askUnion // offered as yes

	ui := tui.NewInitScreen(stdout)
	ui.Header(repo)
	if isTerminal(stdin) && isTerminal(stdout) && !(checkGiven && timeoutGiven && concurrentGiven && !askUnion) {
		if err := tui.AskInit(stdin, stdout, &choice, !checkGiven, !timeoutGiven, !concurrentGiven, askUnion); err != nil {
			ui.Cancelled()
			return dispatch.ExitSetup
		}
	} else if askUnion {
		choice.Union, choice.UnionUnasked = false, true
	}

	steps, err := project.Init(ctx, repo, choice.Check, *force)
	if err == nil {
		var s project.Step
		s, err = project.ApplySettings(repo, choice)
		steps = append(steps, s)
	}
	if err == nil {
		var s project.Step
		var ok bool
		if s, ok, err = project.ApplyUnion(ctx, repo, choice); ok && err == nil {
			steps = append(steps, s)
		}
	}
	if err != nil {
		ui.Steps(steps)
		fmt.Fprintln(stderr, "orchestra init:", err)
		return dispatch.ExitSetup
	}
	pre := project.Prerequisites(repo)
	ui.Steps(steps)
	ui.Prerequisites(pre)
	ui.Next(project.NextSteps(ctx, repo, steps, pre))
	ready := true
	for _, p := range pre {
		ready = ready && p.Kind != project.StepMissing
	}
	ui.SignOff(ready)
	return dispatch.ExitOK
}

// isTerminal reports whether f is a terminal.
func isTerminal(f any) bool {
	fd, ok := f.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(fd.Fd()))
}
