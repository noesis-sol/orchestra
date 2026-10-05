package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
	"golang.org/x/term"
)

// runInit sets up .orchestra/ in the repository around dir, asking what it needs when run in a
// terminal, and returns the exit code. It reads the form's answers from stdin and prints to stdout
// and stderr.
func runInit(
	ctx context.Context, dir string, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer,
) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.String("check", "", "the project's check command (lint, build, tests)")
	checkTimeout := fs.Duration("check-timeout", 0, "how long the check command may run on a rebased ticket, "+
		"e.g. 5m (asked when omitted; default "+project.DefaultCheckTimeoutText+")")
	force := fs.Bool("force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	union := fs.Bool("changelog-union", false, "add 'CHANGELOG.md merge=union' to .gitattributes, "+
		"so two tickets' changelog entries don't conflict (asked when omitted; =false declines)")
	mcpList := fs.String("mcp", "", "the MCP servers workers get, by name, comma-separated, e.g. postgres,firecrawl; "+
		"\"\" for none (asked when omitted)")
	installBeads := fs.Bool("install-beads", false, "install Beads (bd) where it is missing, with Homebrew or "+
		"the Beads install script (asked when omitted; =false declines)")
	agent := fs.String("agent", envOr(getenv, "AGENT_KIND", "claude"), "the workers' Herdr agent kind, "+
		"whose skill folder gets the skills test work needs: .claude/skills for claude, .agents/skills for codex "+
		"[AGENT_KIND]")
	var concurrent int
	fs.IntVar(&concurrent, "concurrent", 0, "tickets to run at the same time by default (asked when omitted)")
	fs.IntVar(&concurrent, "c", 0, "shorthand for --concurrent")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra init [--check \"<command>\"] [--check-timeout D] [--concurrent N] "+
			"[--mcp names] [--changelog-union] [--install-beads] [--force]\n\n"+
			"Set up Beads and .orchestra/ in this repository. Where bd is missing, it offers to install it\n"+
			"(with Homebrew, or the Beads install script); where Beads isn't set up, it runs bd init. Then\n"+
			"the worker prompt (from the built-in template, or moved from .claude/worker-prompt.md),\n"+
			"settings.json (the check command, its time limit, how many tickets run at the same time and\n"+
			"the MCP servers workers get, by name), a .gitignore for the log, reports and per-ticket\n"+
			"files, and a check of what orchestra needs. Where the project keeps a CHANGELOG.md, it\n"+
			"offers to merge it by union in .gitattributes. In a terminal it asks for anything the flags\n"+
			"don't give.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
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
	concurrentGiven, mcpGiven, installGiven := given["concurrent"] || given["c"], given["mcp"], given["install-beads"]
	if timeoutGiven && *checkTimeout <= 0 {
		fmt.Fprintln(stderr, "orchestra init: --check-timeout must be a positive duration such as 5m")
		return dispatch.ExitSetup
	}
	if concurrentGiven && (concurrent < 1 || concurrent > project.MaxConcurrency) {
		fmt.Fprintf(stderr, "orchestra init: --concurrent must be between 1 and %d\n", project.MaxConcurrency)
		return dispatch.ExitSetup
	}

	repo, err := git.Git{}.TopLevel(ctx, dir)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra init: not inside a git repository")
		return dispatch.ExitSetup
	}
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
		choice.CheckTimeout, choice.ReplacedTimeout = command.ShortDuration(*checkTimeout), ""
	}
	if mcpGiven {
		names := mcp.ParseNames(*mcpList)
		choice.MCP = &names
	}
	choice.Servers, choice.ServersErr = mcp.Discover(mcp.UserConfig(getenv), project.ConfigRoots(ctx, repo)...)
	askUnion := !unionGiven && project.OffersUnion(ctx, repo)
	choice.Union = *union || askUnion // offered as yes
	choice.Install = project.FindBeadsInstall(runtime.GOOS)
	bd, _ := project.LocateBd(getenv)
	askInstall := !installGiven && bd == "" && choice.Install.Command != ""
	choice.InstallBeads = *installBeads || askInstall // offered as yes
	choice.Agent = *agent

	ui := tui.NewInitScreen(stdout)
	ui.Header(repo)
	ask := tui.Ask{Check: !checkGiven, Timeout: !timeoutGiven, Concurrent: !concurrentGiven, Union: askUnion,
		MCP: !mcpGiven, Install: askInstall}
	if isTerminal(stdin) && isTerminal(stdout) && ask != (tui.Ask{}) {
		if err := tui.AskInit(stdin, stdout, &choice, ask); err != nil {
			ui.Cancelled()
			return dispatch.ExitSetup
		}
	} else {
		if askUnion {
			choice.Union, choice.UnionUnasked = false, true
		}
		if askInstall {
			choice.InstallBeads, choice.InstallUnasked = false, true
		}
		choice.MCPUnasked = !mcpGiven && choice.MCP == nil
	}

	// Beads first: bd init commits what was staged, and init stages a moved worker prompt. Ctrl+C
	// stops an install or bd init under way, which run in their own process groups, and init with it.
	beadsCtx, stopBeads := signal.NotifyContext(ctx, stopSignals...)
	steps := project.SetUpBeads(beadsCtx, repo, choice, getenv, ui.Working)
	stopped := beadsCtx.Err() != nil
	stopBeads()
	if stopped {
		ui.Steps(steps)
		fmt.Fprintln(stderr, "orchestra init: stopped")
		return dispatch.ExitInterrupted
	}

	initSteps, err := project.Init(ctx, repo, choice.Check, *force)
	steps = append(steps, initSteps...)
	if err == nil {
		var s project.Step
		s, err = project.ApplySettings(repo, choice)
		steps = append(steps, s, project.MCPStep(choice))
	}
	if err == nil {
		var s project.Step
		var ok bool
		if s, ok, err = project.ApplyUnion(ctx, repo, choice); ok && err == nil {
			steps = append(steps, s)
		}
	}
	if err == nil {
		var s project.Step
		var ok bool
		if s, ok, err = project.ApplySkill(repo, choice); ok && err == nil {
			steps = append(steps, s)
		}
	}
	if err != nil {
		ui.Steps(steps)
		fmt.Fprintln(stderr, "orchestra init:", err)
		return dispatch.ExitSetup
	}
	pre := project.Prerequisites(repo, getenv)
	ui.Steps(steps)
	ui.Prerequisites(pre)
	ui.Next(project.NextSteps(ctx, repo, steps, pre, choice))
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
