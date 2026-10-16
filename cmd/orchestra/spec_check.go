package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The checks after a spec's breakdown, whichever breakdown it was (headless, or with the user), before
// the run starts on its root epic: the root epic must be the spec's, and what else is amiss in the
// tree filed is said, as orchestra runs it all the same.

// checkBreakdown checks the tree a breakdown filed for spec under root, as bd has it. root must be an
// epic, and the spec's: one carrying another spec's metadata is an error. It sets the root epic's
// spec metadata (its path, SHA-256 and commit) where the breakdown left it out or got it wrong. The
// warnings say what else is amiss: issues carrying the spec's metadata outside the root epic, issues
// under it that don't carry it, open tickets under it without files metadata or acceptance criteria,
// and blocks cycles among the spec's issues; and what couldn't be checked.
func checkBreakdown(ctx context.Context, bd specTracker, spec project.Spec, root string) ([]string, error) {
	t, err := bd.Show(ctx, root)
	switch {
	case err != nil:
		return nil, fmt.Errorf("can't read %s, the root epic it named: %w", root, err)
	case t.IssueType != "epic":
		return nil, fmt.Errorf("%s, which it named as the root epic, is a %s, not an epic", root, t.IssueType)
	}
	if other := t.MetadataText(dispatch.SpecKey); other != "" && other != spec.Path {
		return nil, fmt.Errorf("%s, which it named as the root epic, is the epic of another spec, %s", root, other)
	}
	warnings := setSpecMetadata(ctx, bd, t, spec)
	issues, err := bd.WithMetadata(ctx, dispatch.SpecKey, spec.Path)
	if err != nil {
		return append(warnings, "can't list the issues carrying the spec's metadata: "+dispatch.FirstLine(err.Error())),
			nil
	}
	under, err := bd.Descendants(ctx, root)
	if err != nil {
		return append(warnings, "can't list the issues under "+root+": "+dispatch.FirstLine(err.Error())), nil
	}
	warnings = append(warnings, treeWarnings(root, issues, under)...)
	cycles, err := bd.Cycles(ctx)
	if err != nil {
		return append(warnings, "can't look for blocks cycles: "+dispatch.FirstLine(err.Error())), nil
	}
	return append(warnings, cycleWarnings(root, issues, under, cycles)...), nil
}

// setSpecMetadata gives the root epic t the spec's metadata where it lacks it or has it wrong, and
// returns the warnings that say so.
func setSpecMetadata(ctx context.Context, bd specTracker, t dispatch.Ticket, spec project.Spec) []string {
	var set, warnings []string
	for _, kv := range [][2]string{{dispatch.SpecKey, spec.Path}, {project.SpecSHA256Key, spec.SHA256},
		{project.SpecCommitKey, spec.Commit}} {
		if t.MetadataText(kv[0]) == kv[1] {
			continue
		}
		if err := bd.SetMetadata(ctx, t.ID, kv[0], kv[1]); err != nil {
			warnings = append(warnings, fmt.Sprintf("can't set %s's %s metadata: %s", t.ID, kv[0],
				dispatch.FirstLine(err.Error())))
			continue
		}
		set = append(set, kv[0])
	}
	if len(set) > 0 {
		warnings = append(warnings, fmt.Sprintf("the breakdown left %s's %s metadata out or wrong; orchestra set it",
			t.ID, strings.Join(set, ", ")))
	}
	return warnings
}

// treeWarnings are the warnings about the tree under root (under, as bd lists it, without its text),
// given the issues carrying the spec's metadata (issues, with their text).
func treeWarnings(root string, issues, under []dispatch.Ticket) []string {
	inTree := map[string]bool{root: true}
	for _, t := range under {
		inTree[t.ID] = true
	}
	full := map[string]dispatch.Ticket{}
	var outside, unmarked, noFiles, noAcceptance []string
	for _, t := range issues {
		full[t.ID] = t
		if !inTree[t.ID] {
			outside = append(outside, t.ID)
		}
	}
	for _, t := range under {
		f, marked := full[t.ID]
		if !marked {
			unmarked = append(unmarked, t.ID)
		}
		if t.IssueType == "epic" || t.Status == dispatch.StatusClosed {
			continue
		}
		if len(dispatch.TicketFiles(t)) == 0 {
			noFiles = append(noFiles, t.ID)
		}
		if marked && strings.TrimSpace(f.AcceptanceCriteria) == "" { // the others' text isn't read
			noAcceptance = append(noAcceptance, t.ID)
		}
	}
	var warnings []string
	add := func(ids []string, what string) {
		if len(ids) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s (%d): %s", what, len(ids), idList(ids)))
		}
	}
	add(outside, "issues carrying the spec's metadata outside "+root+", which the run leaves out")
	add(unmarked, "issues under "+root+" without the spec's metadata, whose workers aren't pointed to the spec")
	add(noFiles, "tickets under "+root+" without files metadata, whose files orchestra guesses")
	add(noAcceptance, "tickets under "+root+" without acceptance criteria")
	return warnings
}

// cycleWarnings are the warnings about the blocks cycles bd found (cycles) that hold an issue of the
// spec's: the root epic, one under it (under), or one carrying its metadata (issues).
func cycleWarnings(root string, issues, under []dispatch.Ticket, cycles [][]string) []string {
	ours := map[string]bool{root: true}
	for _, t := range slices.Concat(issues, under) {
		ours[t.ID] = true
	}
	var warnings []string
	for _, c := range cycles {
		if slices.ContainsFunc(c, func(id string) bool { return ours[id] }) {
			warnings = append(warnings, fmt.Sprintf("a blocks cycle: %s → %s; none of them can start until it is "+
				"broken (bd dep remove)", strings.Join(c, " → "), c[0]))
		}
	}
	return warnings
}

// idListShown is how many IDs a warning lists.
const idListShown = 10

// idList lists ids, the first idListShown of them.
func idList(ids []string) string {
	if len(ids) <= idListShown {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:idListShown], ", ") + fmt.Sprintf(" and %d more", len(ids)-idListShown)
}
