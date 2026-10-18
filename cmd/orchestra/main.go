// Command orchestra works through a Beads backlog, as many tickets at once as --concurrent allows,
// handing each ticket to a coding agent in its own tab (in Herdr or tmux) and git worktree.
//
// Each ticket gets branch wt/<ticket>, created from the current branch of the main checkout, in
// $WT_ROOT/<ticket>. Beads resolves to the main checkout's database from inside a worktree, so
// workers see the same tickets. When a ticket closes with a commit and a clean worktree, the branch
// is fast-forwarded into the main checkout's branch and the worktree, branch and tab are removed.
// Anything else keeps the worktree and the tab for review.
//
// Run from anywhere inside the main checkout (not from a worktree), in a Herdr pane, or with tmux
// installed from any terminal:
//
//	orchestra
//
// Worker tabs open in the pane's own Herdr workspace, or under tmux in orchestra's own session or a
// detached one, unless --workspace says otherwise; --terminal chooses between Herdr and tmux.
//
// Every event is shown in the terminal and appended to .orchestra/orchestra.log, and as JSON, for
// scripts and agents, to .orchestra/run/events.jsonl. Set a project up with 'orchestra init'.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func main() { os.Exit(runMain(os.Args)) }

// runMain is orchestra with args, in the process's working directory and environment, on its standard
// streams: what main runs, and tests run in a process of its own. It returns the exit code, as the run's end
// record says.
func runMain(args []string) int {
	e := cmdEnv{dir: ".", getenv: os.Getenv, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		version: buildVersion()}
	err := run(context.Background(), args, e)
	if err != nil && !errors.As(err, new(exitStatus)) {
		fmt.Fprintln(os.Stderr, "orchestra:", err)
	}
	return exitCode(err)
}
