package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A spec run is the same command every night, orchestra --spec <file>, and a rerun carries on rather
// than break the spec down again. Once the spec is committed, orchestra looks for its root epic,
// which an earlier run's breakdown filed: the epic carrying the spec's path in its metadata whose
// parent doesn't. Its spec_sha256 and spec_commit, which a breakdown sets last of all, name the
// version of the spec its tree was filed for:
//
//   - the spec unchanged since: no breakdown, and the run is scoped to the root epic and carries on,
//     with the last run's workers carried over as usual; once everything is merged, the
//     nothing-to-run box says the spec is built;
//   - the spec changed: the breakdown runs in update mode, given the diff from that version and the
//     tree under the root epic, open and closed, and files only what changed, under the same root;
//   - no spec_sha256: the breakdown that filed it didn't finish, and runs again, carrying on from it.
//
// With no root epic the spec is broken down as a new one; with two, the run can't tell which to
// carry on with, a setup problem.

// specUpdate is what a breakdown in update mode is given besides the spec: the root epic filed for an
// earlier version of it, that version, the change since and the tree under the root epic. The diff
// and the tree are written to File for the session to read.
type specUpdate struct {
	Root string
	// Was is the version the tree was filed for, as the root epic's metadata has it: its SHA-256, and
	// its commit, "" when the metadata names none.
	Was    project.Spec
	Diff   string // git's diff of the spec from Was.Commit to the commit now
	NoDiff string // why there is no Diff, when git can't give it
	Tree   string // the tree under Root, open and closed, as bd has it: an issue a line (see treeText)
	File   string // where they are written, relative to the main checkout: .orchestra/run/spec-update.md
	// Before is what carried the spec's metadata before the update, which a refinement's count of
	// what it files leaves out.
	Before filedCount
}

// carried is what a spec run does with the root epic an earlier run filed for its spec: run it as it
// is, root, the spec unchanged; or have it updated for the change, update. Neither, the spec is
// broken down as for a new one.
type carried struct {
	root   string
	update *specUpdate
}

// carryOn looks for the root epic an earlier run filed for spec, and says what the run does with it
// (see carried). It returns ExitOK, or the exit code having said why the run can't go on: bd can't
// say, the spec has more than one root epic, or Ctrl+C.
func (r specRun) carryOn(ctx context.Context, spec project.Spec) (carried, int) {
	issues, err := r.tracker.WithMetadata(ctx, dispatch.SpecKey, spec.Path)
	switch {
	case ctx.Err() != nil:
		return carried{}, r.stopped(false, "")
	case err != nil:
		detail := dispatch.FirstLine(err.Error())
		fmt.Fprintln(r.err, "orchestra can't look for the spec's root epic, which an earlier run filed:", detail)
		r.logLine("SPEC: can't look for the root epic: " + detail)
		return carried{}, dispatch.ExitTool
	}
	roots := topEpics(issues)
	switch {
	case len(roots) == 0:
		return carried{}, dispatch.ExitOK
	case len(roots) > 1:
		return carried{}, r.manyRoots(spec, roots)
	}
	root := roots[0]
	was := project.Spec{Path: spec.Path, SHA256: root.MetadataText(project.SpecSHA256Key),
		Commit: root.MetadataText(project.SpecCommitKey)}
	switch {
	case was.SHA256 == "":
		fmt.Fprintf(r.out, "the breakdown that filed %s didn't finish: carrying it on\n", root.ID)
		r.logLine(fmt.Sprintf("SPEC: %s has no %s: the breakdown that filed it didn't finish; carrying it on",
			root.ID, project.SpecSHA256Key))
		return carried{}, dispatch.ExitOK
	case was.SHA256 == spec.SHA256 && root.Status == dispatch.StatusClosed:
		// Built, as the nothing-to-run box then says, unless an update filed tickets under it since.
		r.logLine(fmt.Sprintf("SPEC: %s is unchanged, and %s is closed", spec.Path, root.ID))
		return carried{root: root.ID}, dispatch.ExitOK
	case was.SHA256 == spec.SHA256:
		fmt.Fprintf(r.out, "the spec %s is unchanged; carrying on with %s\n", spec.Path, root.ID)
		r.logLine(fmt.Sprintf("SPEC: %s is unchanged; carrying on with %s", spec.Path, root.ID))
		return carried{root: root.ID}, dispatch.ExitOK
	}
	u, code := r.readyUpdate(ctx, spec, root, was)
	if u != nil {
		u.Before = countOf(issues)
	}
	return carried{update: u}, code
}

// topEpics are the epics among issues, those carrying a spec's metadata, whose parent isn't among
// them: the spec's root epics, of which it has one, once broken down.
func topEpics(issues []dispatch.Ticket) []dispatch.Ticket {
	ids := map[string]bool{}
	for _, t := range issues {
		ids[t.ID] = true
	}
	var tops []dispatch.Ticket
	for _, t := range issues {
		if t.IssueType == "epic" && !ids[t.Parent] {
			tops = append(tops, t)
		}
	}
	return tops
}

// manyRoots reports the setup problem of a spec with more than one root epic (roots), one filed by
// hand, say: the run can't tell which to carry on with. It suggests keeping the one a breakdown
// marked as filed, if any, and returns the exit code.
func (r specRun) manyRoots(spec project.Spec, roots []dispatch.Ticket) int {
	keep := roots[0]
	for _, t := range roots {
		if t.MetadataText(project.SpecSHA256Key) != "" {
			keep = t
			break
		}
	}
	var ids, others []string
	for _, t := range roots {
		ids = append(ids, t.ID)
		if t.ID != keep.ID {
			others = append(others, t.ID)
		}
	}
	listed := strings.Join(ids[:len(ids)-1], ", ") + " and " + ids[len(ids)-1]
	count, them, it := "two", "the others", "them"
	if len(roots) > 2 {
		count = fmt.Sprint(len(roots))
	} else {
		them, it = "the other", "it"
	}
	moved := strings.Join(others, " ")
	fmt.Fprintf(r.err, "orchestra cannot start:\n  - The spec %s has %s root epics, %s: epics carrying its "+
		"metadata (%s) whose parent doesn't. A spec run carries on with one, so keep one: put %s under it "+
		"(bd update %s --parent %s), or take the spec's metadata off %s (bd update %s --unset-metadata %s).\n",
		spec.Path, count, listed, dispatch.SpecKey, them, moved, keep.ID, it, moved, dispatch.SpecKey)
	r.logLine(fmt.Sprintf("SPEC: not run: %s has %s root epics, %s", spec.Path, count, listed))
	return dispatch.ExitSetup
}

// readyUpdate readies the update of the tree under root, filed for the version was of spec, which has
// changed since: it reads the change from git, and the tree from bd, and writes them where the
// breakdown reads them (project.WriteSpecUpdate). It returns the update, or the exit code having said
// why there is none.
func (r specRun) readyUpdate(ctx context.Context, spec project.Spec, root dispatch.Ticket, was project.Spec,
) (*specUpdate, int) {
	u := &specUpdate{Root: root.ID, Was: was}
	if was.Commit == "" {
		u.NoDiff = root.ID + " names no " + project.SpecCommitKey
	} else {
		diff, err := git.Git{}.PathDiff(ctx, r.repo, was.Commit, spec.Commit, spec.Path)
		e, isCmd := errors.AsType[*command.Error](err)
		switch {
		case isCmd && e.Stderr != "": // git's own words, without its command line
			u.NoDiff = dispatch.FirstLine(e.Stderr)
		case err != nil:
			u.NoDiff = dispatch.FirstLine(err.Error())
		case strings.TrimSpace(diff) == "":
			u.NoDiff = "git shows no change to it between the two commits"
		default:
			u.Diff = diff
		}
	}
	under, err := r.tracker.Descendants(ctx, root.ID)
	switch {
	case ctx.Err() != nil:
		return nil, r.stopped(false, "")
	case err != nil:
		detail := dispatch.FirstLine(err.Error())
		fmt.Fprintf(r.err, "orchestra can't list the tree under %s, to update it for the spec's change: %s\n",
			root.ID, detail)
		r.logLine("SPEC: can't list the tree under " + root.ID + ": " + detail)
		return nil, dispatch.ExitTool
	}
	u.Tree = treeText(root, under)
	if u.File, err = project.WriteSpecUpdate(r.repo, updateText(spec, u)); err != nil {
		fmt.Fprintln(r.err, "orchestra can't write the spec's change for its breakdown:", err)
		r.logLine("SPEC: can't write the change: " + dispatch.FirstLine(err.Error()))
		return nil, dispatch.ExitSetup
	}
	fmt.Fprintf(r.out, "the spec %s changed since %s was filed: updating its tickets\n", spec.Path, root.ID)
	r.logLine(fmt.Sprintf("SPEC: %s changed since %s was filed for sha256 %s; updating its tree for the change",
		spec.Path, root.ID, was.ShortSHA256()))
	if u.NoDiff != "" {
		r.logLine("SPEC: no diff of the change: " + u.NoDiff)
	}
	return u, dispatch.ExitOK
}

// treeText is the tree under root, its descendants under as bd lists them, for a breakdown to read:
// an issue a line, "<ID> (<status> <type>): <title>", each under its parent, two spaces in.
func treeText(root dispatch.Ticket, under []dispatch.Ticket) string {
	children := map[string][]dispatch.Ticket{}
	for _, t := range under {
		children[t.Parent] = append(children[t.Parent], t)
	}
	var b strings.Builder
	var add func(t dispatch.Ticket, depth int)
	add = func(t dispatch.Ticket, depth int) {
		fmt.Fprintf(&b, "%s- %s (%s %s): %s\n", strings.Repeat("  ", depth), t.ID, t.Status, t.IssueType,
			planLine(t.Title))
		for _, c := range children[t.ID] {
			add(c, depth+1)
		}
	}
	add(root, 0)
	return b.String()
}

// updateText is what a breakdown in update mode reads besides the spec, u's change and tree, in
// Markdown.
func updateText(spec project.Spec, u *specUpdate) []byte {
	var b strings.Builder
	filedFor := "sha256 " + u.Was.SHA256
	if u.Was.Commit != "" {
		filedFor = "commit " + u.Was.Commit + ", " + filedFor
	}
	fmt.Fprintf(&b, "# How the spec changed\n\n%s, the root epic of the spec at %s, and the tree under it were "+
		"filed for the spec as it was at %s. The spec is now at commit %s, sha256 %s.\n\n", u.Root, spec.Path,
		filedFor, spec.Commit, spec.SHA256)
	if u.Diff == "" {
		fmt.Fprintf(&b, "git can't give the change (%s): compare the spec as it is now with the tree.\n\n", u.NoDiff)
	} else {
		fence := strings.Repeat("`", max(3, longestRun(u.Diff, '`')+1))
		fmt.Fprintf(&b, "The change, as `git diff %s %s -- %s` gives it:\n\n%sdiff\n%s", shortCommit(u.Was.Commit),
			shortCommit(spec.Commit), command.ShellWord(spec.Path), fence, u.Diff)
		if !strings.HasSuffix(u.Diff, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(fence + "\n\n")
	}
	fmt.Fprintf(&b, "# The tree under %s\n\nEvery issue under the root epic, open and closed, as bd has it now: its "+
		"ID, status, type and title, under its parent. `bd show <ID>` gives the rest.\n\n%s", u.Root, u.Tree)
	return []byte(b.String())
}

// shortCommit is the start of a commit's hash, as git commands show it.
func shortCommit(commit string) string { return commit[:min(12, len(commit))] }

// longestRun is the length of the longest run of c in s.
func longestRun(s string, c byte) int {
	longest, n := 0, 0
	for i := range len(s) {
		if s[i] != c {
			n = 0
			continue
		}
		n++
		longest = max(longest, n)
	}
	return longest
}
