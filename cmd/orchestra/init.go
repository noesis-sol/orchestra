package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/term"
)

// runInit sets up .orchestra/ in the repository around dir, asking what it needs when run in a
// terminal, and returns the exit code.
func runInit(dir string, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	check := fs.String("check", "", "the project's check command (lint, build, tests)")
	force := fs.Bool("force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	var concurrent int
	fs.IntVar(&concurrent, "concurrent", 0, "tickets to run at the same time by default (asked when omitted)")
	fs.IntVar(&concurrent, "c", 0, "shorthand for --concurrent")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra init [--check \"<command>\"] [--concurrent N] [--force]\n\n"+
			"Set up .orchestra/ in this repository: the worker prompt (from the built-in template, or moved\n"+
			"from .claude/worker-prompt.md), settings.json (the check command and how many tickets run at\n"+
			"the same time), a .gitignore for the log, reports and per-ticket files, and a check of what\n"+
			"orchestra needs. In a terminal it asks for anything the flags don't give.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return dispatch.ExitOK
		}
		return dispatch.ExitSetup
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	checkGiven, concurrentGiven := given["check"], given["concurrent"] || given["c"]
	if concurrentGiven && (concurrent < 1 || concurrent > project.MaxConcurrency) {
		fmt.Fprintf(os.Stderr, "orchestra init: --concurrent must be between 1 and %d\n", project.MaxConcurrency)
		return dispatch.ExitSetup
	}

	out, err := command.Output(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init: not inside a git repository")
		return dispatch.ExitSetup
	}
	repo := strings.TrimSpace(out)
	existing, _, err := project.LoadSettings(repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestra init:", err)
		return dispatch.ExitSetup
	}
	promptText, _ := os.ReadFile(project.Locate(repo).Prompt)
	choice := project.DefaultChoice(existing, string(promptText))
	if checkGiven {
		choice.Check, choice.CheckFrom = *check, "--check"
	}
	if concurrentGiven {
		choice.Concurrent, choice.Unasked = concurrent, false
	}

	ui := newInitUI(os.Stdout)
	ui.header(repo)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if interactive && !(checkGiven && concurrentGiven) {
		if err := askInit(&choice, !checkGiven, !concurrentGiven); err != nil {
			ui.cancelled()
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
		ui.steps(steps)
		fmt.Fprintln(os.Stderr, "orchestra init:", err)
		return dispatch.ExitSetup
	}
	pre := project.Prerequisites(repo)
	ui.steps(steps)
	ui.prerequisites(pre)
	ui.next(project.NextSteps(repo, steps, pre))
	ready := true
	for _, p := range pre {
		ready = ready && p.Kind != project.StepMissing
	}
	ui.signOff(ready)
	return dispatch.ExitOK
}
