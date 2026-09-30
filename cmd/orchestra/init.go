package main

import (
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
func runInit(dir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.String("check", "", "the project's check command (lint, build, tests)")
	checkTimeout := fs.Duration("check-timeout", 0, "how long the check command may run on a rebased ticket, e.g. 5m (asked when omitted; default "+project.DefaultCheckTimeoutText+")")
	force := fs.Bool("force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	var concurrent int
	fs.IntVar(&concurrent, "concurrent", 0, "tickets to run at the same time by default (asked when omitted)")
	fs.IntVar(&concurrent, "c", 0, "shorthand for --concurrent")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra init [--check \"<command>\"] [--check-timeout D] [--concurrent N] [--force]\n\n"+
			"Set up .orchestra/ in this repository: the worker prompt (from the built-in template, or moved\n"+
			"from .claude/worker-prompt.md), settings.json (the check command, its time limit and how many\n"+
			"tickets run at the same time), a .gitignore for the log, reports and per-ticket files, and a\n"+
			"check of what orchestra needs. In a terminal it asks for anything the flags don't give.\n\n")
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
	checkGiven, timeoutGiven, concurrentGiven := given["check"], given["check-timeout"], given["concurrent"] || given["c"]
	if timeoutGiven && *checkTimeout <= 0 {
		fmt.Fprintln(stderr, "orchestra init: --check-timeout must be a positive duration such as 5m")
		return dispatch.ExitSetup
	}
	if concurrentGiven && (concurrent < 1 || concurrent > project.MaxConcurrency) {
		fmt.Fprintf(stderr, "orchestra init: --concurrent must be between 1 and %d\n", project.MaxConcurrency)
		return dispatch.ExitSetup
	}

	out, err := command.Output(dir, "git", "rev-parse", "--show-toplevel")
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
	promptText, _ := os.ReadFile(project.Locate(repo).Prompt)
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

	ui := tui.NewInitScreen(stdout)
	ui.Header(repo)
	if isTerminal(stdin) && isTerminal(stdout) && !(checkGiven && timeoutGiven && concurrentGiven) {
		if err := tui.AskInit(stdin, stdout, &choice, !checkGiven, !timeoutGiven, !concurrentGiven); err != nil {
			ui.Cancelled()
			return dispatch.ExitSetup
		}
	}

	steps, err := project.Init(repo, choice.Check, *force)
	if err == nil {
		var s project.Step
		s, err = project.ApplySettings(repo, choice)
		steps = append(steps, s)
	}
	if err != nil {
		ui.Steps(steps)
		fmt.Fprintln(stderr, "orchestra init:", err)
		return dispatch.ExitSetup
	}
	pre := project.Prerequisites(repo)
	ui.Steps(steps)
	ui.Prerequisites(pre)
	ui.Next(project.NextSteps(repo, steps, pre))
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
