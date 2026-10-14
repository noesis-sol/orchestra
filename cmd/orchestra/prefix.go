package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// readPrefix fills in the choice's ticket prefix, as init asks for it or takes --prefix: the one the
// Beads set up in repo has, or for a repository without Beads the one bd init would give it. Where
// Beads is set up it also counts the tickets a rename would rename, and says why they can't be renamed
// now. The error is bd failing to say the prefix: init then can't change it.
func readPrefix(ctx context.Context, repo string, c *project.Choice) error {
	if _, err := os.Stat(filepath.Join(repo, ".beads")); errors.Is(err, os.ErrNotExist) {
		c.PrefixNew, c.PrefixWas, c.Prefix = true, project.DefaultPrefix(repo), project.DefaultPrefix(repo)
		return nil
	}
	was, err := project.BeadsPrefix(ctx, repo)
	if err == nil && was == "" {
		err = errors.New("bd config get issue_prefix gives none")
	}
	if err != nil {
		return err
	}
	c.PrefixWas, c.Prefix = was, was
	if c.PrefixTickets, c.PrefixExample, err = project.PrefixTickets(ctx, repo); err != nil {
		return err
	}
	c.PrefixRefusal = prefixRefusal(ctx, repo, was)
	return nil
}

// prefixRefusal says why the tickets whose IDs start with prefix can't be renamed now, or returns "":
// what still names their IDs would name IDs that no longer exist. That is the branches (wt/<id>) of
// tickets not closed, and with them their worktrees; the workers the last run left in
// .orchestra/run/state.json, which the next one carries on with; and the tickets left unmerged, whose
// unmerged_fork names their branch.
func prefixRefusal(ctx context.Context, repo, prefix string) string {
	var held []string
	state, err := project.LoadState(repo)
	if err != nil {
		return "orchestra can't read the workers the last run left: " + err.Error()
	}
	if len(state.Workers) > 0 {
		var ids []string
		for _, w := range state.Workers {
			ids = append(ids, w.Ticket)
		}
		held = append(held, "the last run left workers on "+strings.Join(ids, ", ")+" (in "+
			project.RunPath(project.StateName)+"; run orchestra to finish them)")
	}
	tracker := beads.Tracker{Repo: repo}
	branches, err := git.Git{}.BranchesStartingWith(ctx, repo, "wt/"+prefix+"-")
	if err != nil {
		return "git can't list the tickets' branches: " + err.Error()
	}
	if len(branches) > 0 {
		unclosed, err := tracker.Unclosed(ctx)
		if err != nil {
			return "bd can't list the tickets that aren't closed: " + err.Error()
		}
		open := map[string]bool{}
		for _, t := range unclosed {
			open[t.ID] = true
		}
		var named []string
		for _, b := range branches {
			if open[strings.TrimPrefix(b, "wt/")] {
				named = append(named, b)
			}
		}
		if len(named) > 0 {
			held = append(held, "tickets not closed have branches, and their worktrees: "+strings.Join(named, ", "))
		}
	}
	unmerged, err := tracker.Closed(ctx, dispatch.UnmergedLabel)
	if err != nil {
		return "bd can't list the tickets left unmerged: " + err.Error()
	}
	if len(unmerged) > 0 {
		var ids []string
		for _, t := range unmerged {
			ids = append(ids, t.ID)
		}
		held = append(held, "tickets are left unmerged (labelled "+dispatch.UnmergedLabel+"; their "+
			dispatch.ForkKey+" names their branch): "+strings.Join(ids, ", "))
	}
	return strings.Join(held, "; ")
}
