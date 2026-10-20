//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// memFiler is a treeFiler over issues in memory, filed already or by Create, which gives each new
// issue its parent's ID and its number among the parent's children, as filingBd does. calls has each
// write, a line each; fail names a method that fails, and failAt the Create call (from 1) that does.
type memFiler struct {
	issues map[string]dispatch.Ticket
	calls  []string
	fail   string
	failAt int
}

func newMemFiler(filed ...dispatch.Ticket) *memFiler {
	m := &memFiler{issues: map[string]dispatch.Ticket{}}
	for _, t := range filed {
		m.issues[t.ID] = t
	}
	return m
}

func (m *memFiler) Show(_ context.Context, id string) (dispatch.Ticket, error) {
	if t, ok := m.issues[id]; ok {
		return t, nil
	}
	return dispatch.Ticket{}, &beads.NotFoundError{ID: id, Err: errors.New("no issue found matching " + id)}
}

func (m *memFiler) Create(_ context.Context, t beads.NewTicket) (string, error) {
	n := 0
	for _, c := range m.calls {
		if strings.HasPrefix(c, "create ") {
			n++
		}
	}
	if m.failAt == n+1 {
		return "", errors.New("bd create: database is locked")
	}
	children := 0
	for _, is := range m.issues {
		if is.Parent == t.Parent {
			children++
		}
	}
	id := fmt.Sprintf("%s.%d", t.Parent, children+1)
	meta, err := json.Marshal(t.Metadata)
	if err != nil {
		return "", err
	}
	m.calls = append(m.calls, fmt.Sprintf("create %s %s under %s, priority %d, waits for %v, labels %v, files %v, "+
		"metadata %s, acceptance %q", id, t.Type, t.Parent, t.Priority, t.BlockedBy, t.Labels, t.Files, meta, t.Acceptance))
	m.issues[id] = dispatch.Ticket{ID: id, IssueType: t.Type, Parent: t.Parent, Title: t.Title}
	return id, nil
}

func (m *memFiler) AddBlock(_ context.Context, blocker, blocked string) error {
	if m.fail == "AddBlock" {
		return errors.New("bd dep add: database is locked")
	}
	m.calls = append(m.calls, "block "+blocked+" on "+blocker)
	return nil
}

func (m *memFiler) AppendNotes(_ context.Context, id, note string) error {
	if m.fail == "AppendNotes" {
		return errors.New("bd update: database is locked")
	}
	m.calls = append(m.calls, "note "+id+": "+note)
	return nil
}

func (m *memFiler) SetMetadata(_ context.Context, id, key, value string) error {
	if m.fail == "SetMetadata" {
		return errors.New("bd update: database is locked")
	}
	m.calls = append(m.calls, "set "+id+" "+key+"="+value)
	return nil
}

// treeRoot is the root epic s-1 of testSpec as its breakdown files it, with the spec's path alone.
var treeRoot = issue("s-1", "epic", "", `{"spec":".orchestra/specs/app.md"}`, "")

// The answer's issues are filed parents first, in its order otherwise, each under its parent (the
// root epic when it names none) with the spec's metadata, its labels, files and acceptance criteria,
// and its links to those filed already; a link to one filed later is added after them all, as is a
// link to an issue filed before the answer. Then the root epic gets the assumptions in its notes, and
// last of all the spec's SHA-256 and commit, where it lacks them.
func TestFileAnswerTreeFilesParentsFirstThenTheLinks(t *testing.T) {
	t.Parallel()
	env := issue("s-1.1", "task", "s-1", `{"spec":".orchestra/specs/app.md"}`, "Sets it up.")
	root := treeRoot
	root.Metadata = json.RawMessage(`{"spec":".orchestra/specs/app.md","spec_commit":"c0ffee"}`)
	bd := newMemFiler(root, env)
	var filed []string
	a := specAnswer{Root: "s-1", Assumptions: []string{"Sorted A to Z (Sort the things).", "Names in English."},
		Issues: []specIssue{
			{Key: "sort", Parent: "listing", Type: "task", Priority: 2, Title: "Sort the things", Acceptance: "Sorts",
				Files: []string{" sort.go ", ""}, WaitsFor: []string{"list", "s-1.1", "list"}},
			{Key: " listing ", Type: "epic", Priority: 1, Title: "Listing"},
			{Key: "list", Parent: "listing", Type: "task", Priority: 1, Title: "List the things", Acceptance: "Lists",
				Labels: []string{"solo", " "}, Files: []string{"list.go"}, WaitsFor: []string{"s-1.1"}},
			{Key: "docs", Type: "chore", Priority: 3, Title: "Document it", Files: []string{"README.md"},
				WaitsFor: []string{"sort"}},
		}}
	got, err := fileAnswerTree(context.Background(), bd, testSpec, a, func(f filedIssue, id string) {
		filed = append(filed, fmt.Sprintf("%s %v %s", id, f.Epic, f.Title))
	})
	if err != nil {
		t.Fatal(err)
	}
	const meta = `{"spec":".orchestra/specs/app.md"}`
	want := []string{
		"create s-1.2 epic under s-1, priority 1, waits for [], labels [], files [], metadata " + meta + `, acceptance ""`,
		"create s-1.2.1 task under s-1.2, priority 2, waits for [s-1.1], labels [], files [sort.go], metadata " + meta +
			`, acceptance "Sorts"`,
		"create s-1.2.2 task under s-1.2, priority 1, waits for [s-1.1], labels [solo], files [list.go], metadata " +
			meta + `, acceptance "Lists"`,
		"create s-1.3 chore under s-1, priority 3, waits for [s-1.2.1], labels [], files [README.md], metadata " + meta +
			`, acceptance ""`,
		"block s-1.2.1 on s-1.2.2",
		"note s-1: Assumptions\n\n- Sorted A to Z (Sort the things).\n- Names in English.",
		"set s-1 spec_sha256=" + testSpec.SHA256,
	}
	if !reflect.DeepEqual(bd.calls, want) {
		t.Errorf("bd was called with\n%s\nwant\n%s", strings.Join(bd.calls, "\n"), strings.Join(want, "\n"))
	}
	if wantFiled := []string{"s-1.2 true Listing", "s-1.2.1 false Sort the things", "s-1.2.2 false List the things",
		"s-1.3 false Document it"}; !reflect.DeepEqual(filed, wantFiled) {
		t.Errorf("filed %q, want %q", filed, wantFiled)
	}
	if got.issues != 4 || got.links != 1 || len(got.warnings) > 0 {
		t.Errorf("got %+v", got)
	}
}

// An answer that can't be filed as given is refused before anything is filed, saying why.
func TestFileAnswerTreeRefusesAnAnswerItCantFile(t *testing.T) {
	t.Parallel()
	task := func(key, parent string, waits ...string) specIssue {
		return specIssue{Key: key, Parent: parent, Type: "task", Title: "Do " + key, WaitsFor: waits}
	}
	epic := func(key, parent string) specIssue {
		return specIssue{Key: key, Parent: parent, Type: "epic", Title: "Part " + key}
	}
	for _, tc := range []struct {
		name   string
		issues []specIssue
		want   string
	}{
		{"no key", []specIssue{task("a", ""), {Type: "task", Title: "Nameless"}}, `its issue "Nameless" has no key`},
		{"a key twice", []specIssue{task("a", ""), task(" a", "")}, "it gives the key a to two issues"},
		{"an unknown parent", []specIssue{task("a", "partx")},
			"a's parent is partx, which is neither a key of its own nor an issue bd has"},
		{"an unknown blocker", []specIssue{task("a", ""), task("b", "", "a", "s-9")},
			"b waits for s-9, which is neither a key of its own nor an issue bd has"},
		{"a blocks cycle", []specIssue{task("a", "", "c"), task("b", "", "a"), task("c", "", "b")},
			"its blocks links make a cycle: a → c → b → a"},
		{"waiting for itself", []specIssue{task("a", "", "a")}, "its blocks links make a cycle: a → a"},
		{"a parent cycle", []specIssue{epic("p", "q"), epic("q", "p"), task("a", "p")},
			"its issues are each other's parents: p → q → p"},
	} {
		bd := newMemFiler(treeRoot)
		_, err := fileAnswerTree(context.Background(), bd, testSpec, specAnswer{Root: "s-1", Issues: tc.issues}, nil)
		if want := "its answer can't be filed: " + tc.want; err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", tc.name, err, want)
		}
		if len(bd.calls) > 0 {
			t.Errorf("%s: filed %q", tc.name, bd.calls)
		}
	}
}

// A root epic that bd doesn't have, or that isn't an epic, has nothing filed under it; with nothing
// to file, it is left to checkBreakdown, which says so.
func TestFileAnswerTreeNeedsTheRootEpic(t *testing.T) {
	t.Parallel()
	issues := []specIssue{{Key: "a", Type: "task", Title: "Do a"}}
	notEpic := issue("s-1", "task", "", "{}", "")
	for _, tc := range []struct {
		name   string
		filed  []dispatch.Ticket
		issues []specIssue
		want   string
	}{
		{"not filed", nil, issues, "its answer can't be filed: bd has no s-1, its root epic"},
		{"a task", []dispatch.Ticket{notEpic}, issues, "s-1, which it named as the root epic, is a task, not an epic"},
		{"not filed, nothing to file", nil, nil, ""},
		{"a task, nothing to file", []dispatch.Ticket{notEpic}, nil, ""},
	} {
		bd := newMemFiler(tc.filed...)
		_, err := fileAnswerTree(context.Background(), bd, testSpec, specAnswer{Root: "s-1", Issues: tc.issues,
			Assumptions: []string{"One."}}, nil)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
		if len(bd.calls) > 0 {
			t.Errorf("%s: bd was called with %q", tc.name, bd.calls)
		}
	}
}

// bd failing to file an issue or a link stops the filing, saying how far it got; failing to note the
// assumptions or set the root epic's metadata is a warning.
func TestFileAnswerTreeWhenBdFails(t *testing.T) {
	t.Parallel()
	issues := []specIssue{
		{Key: "a", Type: "task", Title: "Do a", WaitsFor: []string{"b"}},
		{Key: "b", Type: "task", Title: "Do\nb"},
	}
	a := specAnswer{Root: "s-1", Issues: issues, Assumptions: []string{"One."}}
	bd := newMemFiler(treeRoot)
	bd.failAt = 2
	got, err := fileAnswerTree(context.Background(), bd, testSpec, a, nil)
	if want := "orchestra filed 1 of its answer's 2 issues, then couldn't file b (Do b): bd create: database is locked"; err == nil ||
		err.Error() != want || got.issues != 1 {
		t.Errorf("a create failing: %+v, %v; want %q", got, err, want)
	}
	bd = newMemFiler(treeRoot)
	bd.fail = "AddBlock"
	got, err = fileAnswerTree(context.Background(), bd, testSpec, a, nil)
	if want := "orchestra filed its answer's 2 issues and 0 of its 1 later blocks links, then couldn't have s-1.1 " +
		"wait for s-1.2: bd dep add: database is locked"; err == nil || err.Error() != want ||
		slices.ContainsFunc(bd.calls, func(c string) bool { return strings.HasPrefix(c, "set ") }) {
		t.Errorf("a link failing: %+v, %v; want %q (%q)", got, err, want, bd.calls)
	}
	for _, method := range []string{"AppendNotes", "SetMetadata"} {
		bd := newMemFiler(treeRoot)
		bd.fail = method
		got, err := fileAnswerTree(context.Background(), bd, testSpec, a, nil)
		if err != nil || len(got.warnings) == 0 || !strings.Contains(got.warnings[0], "database is locked") {
			t.Errorf("%s failing: %+v, %v", method, got, err)
		}
	}
}

// The tree's schema asks for specSchema's answer and the issues, each with its key, type, priority,
// title and description, its type one of bd's and its priority from 0 to 4.
func TestSpecTreeSchema(t *testing.T) {
	t.Parallel()
	type property struct {
		Type    string   `json:"type"`
		Enum    []string `json:"enum"`
		Minimum *int     `json:"minimum"`
		Maximum *int     `json:"maximum"`
	}
	var s struct {
		Properties struct {
			Root, Assumptions, Problem property
			Issues                     struct {
				Type  string `json:"type"`
				Items struct {
					Properties map[string]property `json:"properties"`
					Required   []string            `json:"required"`
				} `json:"items"`
			}
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(specTreeSchema), &s); err != nil {
		t.Fatal(err)
	}
	p, items := s.Properties, s.Properties.Issues.Items
	if p.Root.Type != "string" || p.Assumptions.Type != "array" || p.Problem.Type != "string" ||
		p.Issues.Type != "array" || !reflect.DeepEqual(s.Required, []string{"root", "assumptions", "issues"}) {
		t.Errorf("schema %+v", s)
	}
	if !reflect.DeepEqual(items.Required, []string{"key", "type", "priority", "title", "description"}) ||
		!reflect.DeepEqual(items.Properties["type"].Enum, []string{"epic", "task", "feature", "bug", "chore"}) ||
		*items.Properties["priority"].Minimum != 0 || *items.Properties["priority"].Maximum != 4 {
		t.Errorf("an issue's schema %+v", items)
	}
	var fields []string
	for name := range items.Properties {
		fields = append(fields, name)
	}
	slices.Sort(fields)
	var tags []string
	for f := range reflect.TypeFor[specIssue]().Fields() {
		tags = append(tags, f.Tag.Get("json"))
	}
	slices.Sort(tags)
	if !reflect.DeepEqual(fields, tags) {
		t.Errorf("an issue's schema has %q, specIssue %q", fields, tags)
	}
}
