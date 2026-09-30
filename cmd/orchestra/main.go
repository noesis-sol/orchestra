// Command orchestra works through a Beads backlog one ticket at a time, handing each ticket to a
// coding agent in its own Herdr tab and git worktree.
//
// Each ticket gets branch wt/<ticket>, created from the current branch of the main checkout, in
// $WT_ROOT/<ticket>. Beads resolves to the main checkout's database from inside a worktree, so
// workers see the same tickets. When a ticket closes with a commit and a clean worktree, the branch
// is fast-forwarded into the main checkout's branch and the worktree, branch and tab are removed.
// Anything else keeps the worktree and the tab for review.
//
// Run from anywhere inside the main checkout (not from a worktree), in a Herdr pane:
//
//	orchestra
//
// Worker tabs open in the pane's own Herdr workspace unless --workspace says otherwise.
//
// Every event is shown in the terminal and appended to .orchestra/orchestra.log. Set a project up
// with 'orchestra init'.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := run(context.Background(), os.Args, os.Getenv, os.Stdin, os.Stdout, os.Stderr); err != nil {
		var code exitStatus
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "orchestra:", err)
		os.Exit(1)
	}
}
