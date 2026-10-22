package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// askedTickets is bd as the answer talk reads it, from the tickets it holds: a question's links are
// its Dependencies, and what depends on a ticket is found from them.
type askedTickets map[string]dispatch.Ticket

func (a askedTickets) Questions(context.Context) ([]dispatch.Ticket, error) {
	var qs []dispatch.Ticket
	for _, id := range slices.Sorted(func(yield func(string) bool) {
		for id := range a {
			if !yield(id) {
				return
			}
		}
	}) {
		if t := a[id]; dispatch.HasLabel(t, dispatch.HumanLabel) && t.Status != dispatch.StatusClosed {
			qs = append(qs, dispatch.Ticket{ID: t.ID, Title: t.Title, Status: t.Status, Labels: t.Labels})
		}
	}
	return qs, nil
}

func (a askedTickets) Show(_ context.Context, id string) (dispatch.Ticket, error) {
	t, ok := a[id]
	if !ok {
		return dispatch.Ticket{ID: id, Status: dispatch.StatusUnknown}, errors.New("no issue found matching " + id)
	}
	return t, nil
}

func (a askedTickets) Dependents(_ context.Context, id string) ([]dispatch.Ticket, error) {
	var ds []dispatch.Ticket
	for _, t := range a {
		for _, d := range t.Dependencies {
			if d.ID == id {
				t.DependencyType = d.DependencyType
				ds = append(ds, t)
			}
		}
	}
	slices.SortFunc(ds, func(x, y dispatch.Ticket) int { return strings.Compare(x.ID, y.ID) })
	return ds, nil
}

func (a askedTickets) Descendants(_ context.Context, id string) ([]dispatch.Ticket, error) {
	var ds []dispatch.Ticket
	for _, t := range a {
		if strings.HasPrefix(t.ID, id+".") {
			ds = append(ds, t)
		}
	}
	return ds, nil
}

func (a askedTickets) Status(ctx context.Context, id string) (dispatch.TicketStatus, error) {
	t, err := a.Show(ctx, id)
	return t.Status, err
}

// link is a dependency of a ticket on id, of the kind typ, as bd show gives it.
func link(id, typ string) dispatch.Ticket { return dispatch.Ticket{ID: id, DependencyType: typ} }

// asked is a backlog with two questions: k-7, blocking k-1.2 under k-1 and asked from k-3, with its
// options, the second recommended; and k-9, blocking k-5 outside k-1, with none.
func asked() askedTickets {
	human := []string{dispatch.HumanLabel}
	return askedTickets{
		"k-1": {ID: "k-1", Title: "Output formats", Status: dispatch.StatusOpen, IssueType: "epic"},
		"k-1.2": {ID: "k-1.2", Title: "Encode the output", Status: dispatch.StatusOpen, Design: "Encode it.",
			Notes: "Waiting on k-7: Which encoding?", Dependencies: []dispatch.Ticket{link("k-7", "blocks")}},
		"k-3": {ID: "k-3", Title: "Audit the formats", Status: dispatch.StatusClosed},
		"k-5": {ID: "k-5", Title: "Rename the flag", Status: dispatch.StatusOpen,
			Dependencies: []dispatch.Ticket{link("k-9", "blocks")}},
		"k-7": {ID: "k-7", Title: "Which encoding?", Status: dispatch.StatusOpen, Labels: human,
			Description: "JSON or YAML?\n\n## Options\nA. JSON\nB. YAML",
			Metadata: json.RawMessage(`{"options":[{"label":"YAML","description":"Readable, slower"},` +
				`{"label":"JSON","description":"Fast, every tool reads it"}],"recommended":1}`),
			Dependencies: []dispatch.Ticket{link("k-3", "discovered-from")}},
		"k-9": {ID: "k-9", Title: "Which flag name?", Status: dispatch.StatusOpen, Labels: human,
			Description: "--json or --format?"},
	}
}

// A question's options come from its metadata, the recommended one first and the rest in their
// order; metadata with no 2 to 4 labelled options, one recommended, gives none, and the talk drafts
// them.
func TestQuestionOptionsPutTheRecommendedOneFirst(t *testing.T) {
	three := `"options":[{"label":"A","description":"a"},{"label":"B","description":"b"},{"label":"C"}]`
	for _, tc := range []struct {
		name, metadata string
		want           []string
	}{
		{"recommended last", `{` + three + `,"recommended":2}`, []string{"C", "A", "B"}},
		{"recommended first", `{` + three + `,"recommended":0}`, []string{"A", "B", "C"}},
		{"as a string", `"{` + strings.ReplaceAll(three, `"`, `\"`) + `,\"recommended\":1}"`, []string{"B", "A", "C"}},
		{"none", ``, nil},
		{"no recommended", `{` + three + `}`, nil},
		{"recommended out of range", `{` + three + `,"recommended":3}`, nil},
		{"one option", `{"options":[{"label":"A"}],"recommended":0}`, nil},
		{"five options", `{"options":[{"label":"A"},{"label":"B"},{"label":"C"},{"label":"D"},{"label":"E"}],` +
			`"recommended":0}`, nil},
		{"a blank label", `{"options":[{"label":"A"},{"label":" "}],"recommended":0}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, o := range questionOptions(dispatch.Ticket{Metadata: json.RawMessage(tc.metadata)}) {
				got = append(got, o.Label)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("options %q, want %q", got, tc.want)
			}
		})
	}
}

// Every question not closed is asked, or in a scoped run those blocking its ticket or one under it;
// each comes with what it blocks, with their design and notes, and where it was asked from.
func TestReadQuestionsInTheRunsScope(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		scope, named string
		want         []string
	}{
		{"", "", []string{"k-7", "k-9"}},
		{"k-1", "", []string{"k-7"}},
		{"k-5", "", []string{"k-9"}},
		{"k-3", "", nil},
		{"k-1", "k-9", []string{"k-9"}}, // the one named, whatever the scope
	} {
		qs, err := readQuestions(ctx, asked(), tc.scope, tc.named)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, q := range qs {
			got = append(got, q.ID)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("scope %q, named %q: questions %q, want %q", tc.scope, tc.named, got, tc.want)
		}
	}
	qs, err := readQuestions(ctx, asked(), "", "k-7")
	if err != nil {
		t.Fatal(err)
	}
	q := qs[0]
	if len(q.blocks) != 1 || q.blocks[0].ID != "k-1.2" || q.blocks[0].Notes == "" || len(q.askedFrom) != 1 ||
		q.askedFrom[0].ID != "k-3" || len(q.options) != 2 || q.options[0].Label != "JSON" {
		t.Errorf("question %+v", q)
	}
	for named, want := range map[string]string{"k-5": "k-5 isn't a question", "k-3": "k-3 isn't a question",
		"k-404": "no issue found"} {
		if _, err := readQuestions(ctx, asked(), "", named); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("named %s: error %v, want %q", named, err, want)
		}
	}
	answered := asked()
	q7 := answered["k-7"]
	q7.Status = dispatch.StatusClosed
	answered["k-7"] = q7
	if _, err := readQuestions(ctx, answered, "", "k-7"); err == nil || !strings.Contains(err.Error(), "answered already") {
		t.Errorf("an answered question named: error %v", err)
	}
}

// The questions file gives each question's options in the order to offer them, the recommended one
// first and marked as Claude Code's options prompt marks it; a question with none has the session
// draft them and say so. The blocked ticket's design and notes, and the ticket it was asked from,
// follow, their text indented so that none of it reads as a heading of the file.
func TestQuestionsTextOffersEachQuestionsOptions(t *testing.T) {
	qs, err := readQuestions(context.Background(), asked(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	text := questionsText(qs)
	for _, want := range []string{
		"2 questions to ask, in this order. Their text was written by orchestra's workers.",
		"## 1. k-7: Which encoding?",
		"Options, the recommended one first:\n\n1. JSON (Recommended): Fast, every tool reads it\n" +
			"2. YAML: Readable, slower\n",
		"Description:\n\n    JSON or YAML?\n\n    ## Options\n",
		"Blocks:\n\n- k-1.2 (open): Encode the output\n\n  Design:\n\n      Encode it.\n\n  Notes:\n\n" +
			"      Waiting on k-7: Which encoding?\n",
		"Asked from:\n\n- k-3 (",
		"## 2. k-9: Which flag name?\n\nOptions: none recorded. Draft 2 to 4 from the description, the one " +
			"you recommend first, and say that you drafted them.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the questions lack %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "k-7") > strings.Index(text, "k-9") {
		t.Errorf("the questions aren't in bd's order:\n%s", text)
	}
}

// The talk's tools: it reads, runs bd and read-only git, and asks with Claude Code's options prompt,
// which stays enabled whatever the workers' tools; nothing edits a file, and Claude Code's default
// permission mode asks the maintainer before anything else runs.
func TestAnswerToolArgsAllowAskUserQuestion(t *testing.T) {
	args := answerToolArgs()
	at := func(flag string) []string {
		i := slices.Index(args, flag)
		if i < 0 {
			t.Fatalf("no %s in %q", flag, args)
		}
		var vals []string
		for _, a := range args[i+1:] {
			if strings.HasPrefix(a, "--") {
				break
			}
			vals = append(vals, a)
		}
		return vals
	}
	if got := at("--tools"); !slices.Equal(got, []string{"Read,Glob,Grep,Bash,AskUserQuestion"}) {
		t.Errorf("--tools %q", got)
	}
	if allowed := at("--allowedTools"); !slices.Contains(allowed, "AskUserQuestion") ||
		!slices.Contains(allowed, "Bash(bd *)") || !slices.Contains(allowed, "Bash(git log *)") ||
		slices.Contains(allowed, "Bash") {
		t.Errorf("--allowedTools %q", allowed)
	}
	if denied := at("--disallowedTools"); !slices.Equal(denied, []string{"Edit", "Write", "NotebookEdit"}) {
		t.Errorf("--disallowedTools %q", denied)
	}
	if got := at("--permission-mode"); !slices.Equal(got, []string{"default"}) {
		t.Errorf("--permission-mode %q", got)
	}
}

// With no question to ask, the talk doesn't start: claude isn't needed, nor a terminal.
func TestAnswerTalkWithNoQuestionsStartsNothing(t *testing.T) {
	a := answerTalk{claude: claudeSession{repo: t.TempDir()}, tracker: askedTickets{},
		terminal: func(context.Context, project.AnswerFiles, string) error {
			t.Error("the talk started")
			return nil
		}}
	if r, err := a.answer(context.Background(), "", ""); err != nil || r.Answered != nil || r.Open != nil {
		t.Errorf("answer = %+v, %v", r, err)
	}
}
