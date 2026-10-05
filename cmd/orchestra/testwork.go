package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// fileTestWork files the work the choice calls for on the project's tests (project.TestWork) with
// bd, and returns a step for each ticket: filed, or found open from an earlier init and not filed
// again. A ticket that can't be filed is a step saying why, as are those after it, not an error:
// the files init wrote stand, and init run again files what is missing.
func fileTestWork(ctx context.Context, repo string, c project.Choice) []project.Step {
	base, err := git.Git{}.CurrentBranch(ctx, repo)
	if err != nil || base == "" {
		base = "the branch orchestra runs on"
	}
	plan := project.TestWork(c, base)
	if len(plan) == 0 {
		return nil
	}
	const label = "ticket"
	tracker := beads.Tracker{Repo: repo}
	notFiled := func(from int, err error) []project.Step {
		var titles []string
		for _, t := range plan[from:] {
			titles = append(titles, "\""+t.Title+"\"")
		}
		return []project.Step{{Kind: project.StepCaution, Label: label, Detail: "not filed: " +
			strings.Join(titles, ", ") + " (" + err.Error() + "); run orchestra init again to file them"}}
	}
	filed, err := tracker.Filed(ctx, project.TestWorkLabel)
	if err != nil {
		return notFiled(0, err)
	}
	open := map[string]string{} // title → ID
	for _, t := range filed {
		if _, ok := open[t.Title]; !ok {
			open[t.Title] = t.ID
		}
	}
	var steps []project.Step
	ids := make([]string, len(plan))
	for i, t := range plan {
		if id, ok := open[t.Title]; ok {
			ids[i] = id
			steps = append(steps, project.Step{Kind: project.StepKept, Label: label,
				Detail: id + " is open: " + t.Title + "; not filed again"})
			continue
		}
		nt := beads.NewTicket{Title: t.Title, Description: t.Description, Acceptance: t.Acceptance, Type: t.Type,
			Priority: t.Priority, Labels: t.Labels}
		if t.Parent >= 0 {
			nt.Parent = ids[t.Parent]
		}
		for _, b := range t.BlockedBy {
			nt.BlockedBy = append(nt.BlockedBy, ids[b])
		}
		id, err := tracker.Create(ctx, nt)
		if err != nil {
			return append(steps, notFiled(i, err)...)
		}
		ids[i] = id
		kind := fmt.Sprintf("P%d", t.Priority)
		if t.Type == "epic" {
			kind = "epic"
		}
		if t.Solo() {
			kind += ", solo"
		}
		steps = append(steps, project.Step{Kind: project.StepDone, Label: label,
			Detail: "filed " + id + " (" + kind + "): " + t.Title})
	}
	return steps
}
