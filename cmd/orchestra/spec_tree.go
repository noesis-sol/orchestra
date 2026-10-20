package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A headless breakdown of a spec for the first time files the root epic, and the environment ticket
// where the spec needs one, with bd, so that the run can start on that ticket while the rest is filed
// (spec_early.go); every other issue of the tree it gives in its answer, which orchestra files. A
// session that filed each issue itself took a turn for each bd create, every turn reading its whole
// context again: on tutti's of 2026-10-16, 46 of its 56 turns, in 21.5 minutes. orchestra then sets
// the root epic's spec_sha256 and spec_commit, which mark the tree as filed. An update of the tree,
// and a breakdown with the user, still file through bd as they go.

// specTreeSchema is the answer of a breakdown of a spec for the first time: specSchema's, with the
// issues of the tree it leaves to orchestra to file.
const specTreeSchema = `{"type":"object","properties":{"root":{"type":"string"},` +
	`"assumptions":{"type":"array","items":{"type":"string"}},"problem":{"type":"string"},` +
	`"issues":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},` +
	`"parent":{"type":"string"},"type":{"type":"string","enum":["epic","task","feature","bug","chore"]},` +
	`"priority":{"type":"integer","minimum":0,"maximum":4},"title":{"type":"string"},` +
	`"description":{"type":"string"},"acceptance":{"type":"string"},` +
	`"labels":{"type":"array","items":{"type":"string"}},"files":{"type":"array","items":{"type":"string"}},` +
	`"waits_for":{"type":"array","items":{"type":"string"}}},` +
	`"required":["key","type","priority","title","description"]}}},` +
	`"required":["root","assumptions","issues"]}`

// specIssue is an issue of the tree a breakdown's answer gives, for orchestra to file. It is known
// by a key of the answer's own; its parent and the issues it waits for are each the key of another
// in the answer or the ID of an issue filed already, its parent "" for the root epic.
type specIssue struct {
	Key         string   `json:"key"`
	Parent      string   `json:"parent"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Acceptance  string   `json:"acceptance"`
	Labels      []string `json:"labels"`
	Files       []string `json:"files"`
	WaitsFor    []string `json:"waits_for"`
}

// treeFiler is bd as orchestra files the tree a breakdown's answer gives under its root epic.
type treeFiler interface {
	Show(ctx context.Context, id string) (dispatch.Ticket, error)
	Create(ctx context.Context, t beads.NewTicket) (string, error)
	AddBlock(ctx context.Context, blocker, blocked string) error
	AppendNotes(ctx context.Context, id, note string) error
	SetMetadata(ctx context.Context, id, key, value string) error
}

// treeFiled is what fileAnswerTree filed: the issues, the blocks links added once both ends were
// filed, and the warnings about what it couldn't note or set on the root epic.
type treeFiled struct {
	issues, links int
	warnings      []string
}

// fileAnswerTree files the issues of the answer a, a breakdown's of spec, under its root epic,
// telling filed of each as it is filed, each carrying the spec's metadata. Parents are filed before
// their children, each issue with its links to those it waits for that are filed already; the other
// links are added after. Then the root epic gets the answer's assumptions in its notes, under
// Assumptions, and last of all its spec_sha256 and spec_commit; bd failing at either is a warning
// (checkBreakdown sets the metadata again). An answer that can't be filed as given (an issue without
// a key, a key given twice, a name that is neither a key of the answer nor an issue bd has, a parent
// or blocks cycle) is an error before anything is filed; bd failing to file is one, saying how far
// it got.
func fileAnswerTree(ctx context.Context, bd treeFiler, spec project.Spec, a specAnswer,
	filed func(f filedIssue, id string)) (treeFiled, error) {
	root, err := bd.Show(ctx, a.Root)
	switch {
	case (err != nil || root.IssueType != "epic") && len(a.Issues) == 0:
		return treeFiled{}, nil // nothing to file under it: checkBreakdown says what is amiss
	case errors.Is(err, dispatch.ErrNoTicket):
		return treeFiled{}, fmt.Errorf("its answer can't be filed: bd has no %s, its root epic", a.Root)
	case err != nil:
		return treeFiled{}, fmt.Errorf("can't read %s, the root epic it named: %w", a.Root, err)
	case root.IssueType != "epic":
		return treeFiled{}, fmt.Errorf("%s, which it named as the root epic, is a %s, not an epic", a.Root,
			root.IssueType)
	}
	issues := trimmedIssues(a.Issues)
	order, err := planTree(ctx, bd, a.Root, issues)
	if err != nil {
		return treeFiled{}, fmt.Errorf("its answer can't be filed: %w", err)
	}
	var done treeFiled
	ids := map[string]string{} // the answer's keys filed, by key
	keys := map[string]bool{}
	for _, is := range issues {
		keys[is.Key] = true
	}
	ref := func(r string) string { // the ID r names: a key's, once filed, or an issue's filed already
		if keys[r] {
			return ids[r]
		}
		return r
	}
	type link struct{ blocker, blocked string } // keys of the answer's
	var later []link
	for _, i := range order {
		is := issues[i]
		t := beads.NewTicket{Title: is.Title, Description: is.Description, Acceptance: is.Acceptance, Type: is.Type,
			Priority: is.Priority, Parent: cmp.Or(ref(is.Parent), a.Root), Files: is.Files, Labels: is.Labels,
			Metadata: map[string]string{dispatch.SpecKey: spec.Path}}
		for _, w := range is.WaitsFor {
			if id := ref(w); id != "" {
				t.BlockedBy = append(t.BlockedBy, id)
			} else {
				later = append(later, link{blocker: w, blocked: is.Key})
			}
		}
		id, err := bd.Create(ctx, t)
		if err != nil {
			return done, fmt.Errorf("orchestra filed %d of its answer's %d issues, then couldn't file %s (%s): %w",
				done.issues, len(issues), is.Key, planLine(is.Title), err)
		}
		ids[is.Key] = id
		done.issues++
		if filed != nil {
			filed(filedIssue{Epic: is.Type == "epic", Title: is.Title}, id)
		}
	}
	for _, l := range later {
		if err := bd.AddBlock(ctx, ids[l.blocker], ids[l.blocked]); err != nil {
			return done, fmt.Errorf("orchestra filed its answer's %d issues and %d of its %d later blocks links, then "+
				"couldn't have %s wait for %s: %w", done.issues, done.links, len(later), ids[l.blocked], ids[l.blocker], err)
		}
		done.links++
	}
	if note := assumptionsNote(a.Assumptions); note != "" {
		if err := bd.AppendNotes(ctx, a.Root, note); err != nil {
			done.warnings = append(done.warnings, fmt.Sprintf("can't note the assumptions on %s: %s", a.Root,
				dispatch.FirstLine(err.Error())))
		}
	}
	for _, kv := range [][2]string{{project.SpecSHA256Key, spec.SHA256}, {project.SpecCommitKey, spec.Commit}} {
		if root.MetadataText(kv[0]) == kv[1] {
			continue
		}
		if err := bd.SetMetadata(ctx, a.Root, kv[0], kv[1]); err != nil {
			done.warnings = append(done.warnings, fmt.Sprintf("can't set %s's %s metadata: %s", a.Root, kv[0],
				dispatch.FirstLine(err.Error())))
		}
	}
	return done, nil
}

// trimmedIssues are issues with their keys, references, labels and files trimmed, those left empty
// dropped, and an issue waiting for the same one twice waiting for it once.
func trimmedIssues(issues []specIssue) []specIssue {
	words := func(ws []string) []string {
		var kept []string
		for _, w := range ws {
			if w = strings.TrimSpace(w); w != "" && !slices.Contains(kept, w) {
				kept = append(kept, w)
			}
		}
		return kept
	}
	out := make([]specIssue, len(issues))
	for i, is := range issues {
		is.Key, is.Parent, is.Type = strings.TrimSpace(is.Key), strings.TrimSpace(is.Parent), strings.TrimSpace(is.Type)
		is.Labels, is.Files, is.WaitsFor = words(is.Labels), words(is.Files), words(is.WaitsFor)
		out[i] = is
	}
	return out
}

// planTree checks the issues of an answer whose root epic, which bd has, is root before any is filed,
// and returns the order to file them in: each after its parent, in the answer's order otherwise.
func planTree(ctx context.Context, bd treeFiler, root string, issues []specIssue) ([]int, error) {
	index := map[string]int{}
	for i, is := range issues {
		switch _, twice := index[is.Key]; {
		case is.Key == "":
			return nil, fmt.Errorf("its issue %q has no key", planLine(is.Title))
		case twice:
			return nil, fmt.Errorf("it gives the key %s to two issues", is.Key)
		}
		index[is.Key] = i
	}
	known := map[string]bool{root: true}
	check := func(id, what string) error { // an ID filed already, which bd must have
		if known[id] {
			return nil
		}
		_, err := bd.Show(ctx, id)
		switch {
		case errors.Is(err, dispatch.ErrNoTicket):
			return fmt.Errorf("%s %s, which is neither a key of its own nor an issue bd has", what, id)
		case err != nil:
			return fmt.Errorf("can't read %s, which %s: %w", id, strings.TrimSuffix(what, " is"), err)
		}
		known[id] = true
		return nil
	}
	for _, is := range issues {
		if _, key := index[is.Parent]; is.Parent != "" && !key {
			if err := check(is.Parent, is.Key+"'s parent is"); err != nil {
				return nil, err
			}
		}
		for _, w := range is.WaitsFor {
			if _, key := index[w]; !key {
				if err := check(w, is.Key+" waits for"); err != nil {
					return nil, err
				}
			}
		}
	}
	among := func(refs ...string) []int { // the issues of the answer that refs name
		var to []int
		for _, r := range refs {
			if i, ok := index[r]; ok {
				to = append(to, i)
			}
		}
		return to
	}
	keysOf := func(c []int) string {
		var ks []string
		for _, i := range c {
			ks = append(ks, issues[i].Key)
		}
		return strings.Join(ks, " → ")
	}
	if c := cycleAmong(len(issues), func(i int) []int { return among(issues[i].Parent) }); c != nil {
		return nil, fmt.Errorf("its issues are each other's parents: %s", keysOf(c))
	}
	if c := cycleAmong(len(issues), func(i int) []int { return among(issues[i].WaitsFor...) }); c != nil {
		return nil, fmt.Errorf("its blocks links make a cycle: %s", keysOf(c))
	}
	var order []int
	placed := make([]bool, len(issues))
	var place func(i int)
	place = func(i int) {
		if placed[i] {
			return
		}
		placed[i] = true
		for _, p := range among(issues[i].Parent) {
			place(p)
		}
		order = append(order, i)
	}
	for i := range issues {
		place(i)
	}
	return order, nil
}

// cycleAmong is a cycle among n issues whose edges next gives, as their indexes from one round to it
// again, or nil when there is none.
func cycleAmong(n int, next func(int) []int) []int {
	const (
		unseen = iota
		onPath
		done
	)
	state := make([]int, n)
	var path []int
	var visit func(i int) []int
	visit = func(i int) []int {
		state[i] = onPath
		path = append(path, i)
		for _, j := range next(i) {
			switch state[j] {
			case onPath:
				return append(slices.Clone(path[slices.Index(path, j):]), j)
			case unseen:
				if c := visit(j); c != nil {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		state[i] = done
		return nil
	}
	for i := range n {
		if state[i] == unseen {
			if c := visit(i); c != nil {
				return c
			}
		}
	}
	return nil
}

// assumptionsNote is the root epic's note of the assumptions a breakdown made: a list under the
// heading Assumptions, or "" for none.
func assumptionsNote(assumed []string) string {
	if len(assumed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Assumptions\n")
	for _, a := range assumed {
		b.WriteString("\n- " + a)
	}
	return b.String()
}
