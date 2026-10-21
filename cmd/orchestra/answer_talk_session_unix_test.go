//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// answerIssue is an issue as the fake bd of the answer talk's tests keeps it: deps are what it depends
// on, by ID and the link's type.
type answerIssue struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Status      string            `json:"status"`
	Labels      []string          `json:"labels,omitempty"`
	Description string            `json:"description,omitempty"`
	Design      string            `json:"design,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	CloseReason string            `json:"close_reason,omitempty"`
	Metadata    json.RawMessage   `json:"metadata,omitempty"`
	Deps        map[string]string `json:"deps,omitempty"`
}

// answerBdVar names, in the fake bd's environment, the file that holds its issues.
const answerBdVar = "ANSWER_BD_STATE"

// answerBd is a bd keeping its issues in the file answerBdVar names: it lists the questions, shows an
// issue with its dependencies, lists what depends on one, records an answer (bd human respond, which
// closes the question) and replaces a design from stdin (bd update --design-file -), as bd does.
func answerBd(args []string) int {
	state := os.Getenv(answerBdVar) //nolint:forbidigo // the fake's own environment, which WriteFunc set
	b, err := os.ReadFile(state)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var issues map[string]*answerIssue
	if err := json.Unmarshal(b, &issues); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	shown := func(i *answerIssue, typ string) map[string]any {
		m := map[string]any{"id": i.ID, "title": i.Title, "status": i.Status, "labels": i.Labels,
			"description": i.Description, "design": i.Design, "notes": i.Notes, "close_reason": i.CloseReason,
			"metadata": i.Metadata}
		if typ != "" {
			m["dependency_type"] = typ
		}
		return m
	}
	answer := func(data any) int {
		out, _ := json.Marshal(map[string]any{"schema_version": 1, "data": data})
		fmt.Println(string(out))
		return 0
	}
	ids := slices.Sorted(func(yield func(string) bool) {
		for id := range issues {
			if !yield(id) {
				return
			}
		}
	})
	switch {
	case len(args) >= 1 && args[0] == "list" && slices.Contains(args, dispatch.HumanLabel):
		var qs []map[string]any
		for _, id := range ids {
			if i := issues[id]; slices.Contains(i.Labels, dispatch.HumanLabel) && i.Status != "closed" {
				qs = append(qs, shown(i, ""))
			}
		}
		return answer(qs)
	case len(args) >= 2 && args[0] == "show":
		i, ok := issues[args[1]]
		if !ok {
			fmt.Fprintln(os.Stderr, "Error: no issue found matching "+args[1])
			return 1
		}
		m := shown(i, "")
		var deps []map[string]any
		for id, typ := range i.Deps {
			deps = append(deps, shown(issues[id], typ))
		}
		m["dependencies"] = deps
		return answer([]any{m})
	case len(args) >= 3 && args[0] == "dep" && args[1] == "list":
		var ds []map[string]any
		for _, id := range ids {
			if typ, ok := issues[id].Deps[args[2]]; ok {
				ds = append(ds, shown(issues[id], typ))
			}
		}
		return answer(ds)
	case len(args) == 5 && args[0] == "human" && args[1] == "respond" && args[3] == "--response":
		issues[args[2]].Status, issues[args[2]].CloseReason = "closed", "Responded"
		issues[args[2]].Notes += "\nResponse: " + args[4]
	case len(args) == 4 && args[0] == "update" && args[2] == "--design-file" && args[3] == "-":
		design, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 1
		}
		issues[args[1]].Design = string(design)
	default:
		fmt.Fprintf(os.Stderr, "answerBd: unexpected %q\n", args)
		return 1
	}
	if b, err = json.Marshal(issues); err == nil {
		err = os.WriteFile(state+".new", b, 0o644)
	}
	if err == nil {
		err = os.Rename(state+".new", state)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

var _ = faketool.Func("answer-bd", answerBd)

// answerBacklog is the fake bd's issues: the question k-7, with its options, the second recommended,
// asked from k-3 and blocking k-1, which waits on its answer.
func answerBacklog() map[string]*answerIssue {
	return map[string]*answerIssue{
		"k-1": {ID: "k-1", Title: "Encode the output", Status: "open", Design: "Encode it.",
			Notes: "Waiting on k-7: Which encoding?", Deps: map[string]string{"k-7": "blocks"}},
		"k-3": {ID: "k-3", Title: "Audit the formats", Status: "closed"},
		"k-7": {ID: "k-7", Title: "Which encoding?", Status: "open", Labels: []string{dispatch.HumanLabel},
			Description: "JSON or YAML? A. YAML B. JSON (recommended)",
			Metadata: json.RawMessage(`{"options":[{"label":"YAML","description":"Readable, slower"},` +
				`{"label":"JSON","description":"Fast, every tool reads it"}],"recommended":1}`),
			Deps: map[string]string{"k-3": "discovered-from"}},
	}
}

// answerSetup puts the fake bd, keeping the issues given, and a claude on t's PATH, and returns the
// main checkout, the bd's path and its issues' file.
func answerSetup(t *testing.T, issues map[string]*answerIssue) (repo, bd, state string) {
	t.Helper()
	tools := t.TempDir()
	state = filepath.Join(tools, "issues.json")
	b, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, b, 0o644); err != nil {
		t.Fatal(err)
	}
	bd = faketool.WriteFunc(t, tools, "answer-bd", answerBdVar+"="+state)
	if err := os.Link(bd, filepath.Join(tools, "bd")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(bd+".sh", filepath.Join(tools, "bd.sh")); err != nil {
		t.Fatal(err)
	}
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	return t.TempDir(), bd, state
}

// readIssues reads the fake bd's issues from its file.
func readIssues(t *testing.T, state string) map[string]*answerIssue {
	t.Helper()
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var issues map[string]*answerIssue
	if err := json.Unmarshal(b, &issues); err != nil {
		t.Fatal(err)
	}
	return issues
}

// paneAnswerTalk is the answer talk in a pane split off orchestra's (w1:p1), on bd in repo.
func paneAnswerTalk(t *testing.T, repo string, panes *fakePanes, out, errOut io.Writer) answerTalk {
	a := answerTalk{claude: claudeSession{repo: repo, tools: answerToolArgs()}, pane: "w1:p1", panes: panes,
		tracker: beads.Tracker{Repo: repo}, every: answerCountEvery, out: out, err: errOut}
	a.terminal = func(context.Context, string) error {
		t.Error("the talk took orchestra's terminal")
		return nil
	}
	return a
}

// The answer talk starts claude in a pane beside orchestra's with its tools, its instructions and the
// questions, which its first message brings in. A session that answers through bd, as its
// instructions have it, closes the question and adds the decision to the blocked ticket's design;
// orchestra reads from bd that every question is closed, closes the pane once claude's turn is over,
// and says the question was answered.
func TestAnswerTalkRecordsTheAnswerThroughBd(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		const decided = "Encode it.\n\n## Decided (k-7)\n\nWhich encoding? JSON: every tool reads it.\n"
		panes := &fakePanes{
			agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking, dispatch.StateIdle},
			step: func(read int) {
				if read != 2 {
					return
				}
				// The session, as its instructions have it: the blocked ticket's design, then the answer.
				update := exec.Command(bd, "update", "k-1", "--design-file", "-")
				update.Stdin = strings.NewReader(decided)
				if out, err := update.CombinedOutput(); err != nil {
					t.Errorf("bd update: %v\n%s", err, out)
				}
				if out, err := exec.Command(bd, "human", "respond", "k-7", "--response", "JSON").CombinedOutput(); err != nil {
					t.Errorf("bd human respond: %v\n%s", err, out)
				}
			},
		}
		var out, errOut strings.Builder
		r, err := paneAnswerTalk(t, repo, panes, &out, &errOut).answer(runCtx(t), "", "")
		if err != nil || !slices.Equal(r.Answered, []string{"k-7"}) || r.Open != nil {
			t.Fatalf("answer = %+v, %v; stderr:\n%s", r, err, errOut.String())
		}
		if want := []string{"split w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
			t.Errorf("the terminal was asked to %q, want %q", panes.calls, want)
		}
		prompt := filepath.Join(repo, project.RunPath("answer-prompt.md"))
		want := slices.Concat([]string{"--append-system-prompt-file", prompt}, answerToolArgs(),
			[]string{"--strict-mcp-config", "--", "Let's answer the 1 open question in @.orchestra/run/questions.md, " +
				"one at a time."})
		if !slices.Equal(panes.launched, want) {
			t.Errorf("claude started with %q, want %q", panes.launched, want)
		}
		if b, err := os.ReadFile(prompt); err != nil || !strings.Contains(string(b), "AskUserQuestion") {
			t.Errorf("the instructions at %s aren't the answer talk's (%v):\n%s", prompt, err, b)
		}
		questions, err := os.ReadFile(filepath.Join(repo, project.RunPath(project.AnswerQuestionsName)))
		if err != nil || !strings.Contains(string(questions), "1. JSON (Recommended): Fast, every tool reads it\n"+
			"2. YAML: Readable, slower\n") || !strings.Contains(string(questions), "- k-1 (open): Encode the output") ||
			!strings.Contains(string(questions), "Asked from:\n\n- k-3 (closed): Audit the formats") {
			t.Errorf("the questions (%v):\n%s", err, questions)
		}
		if want := "Answering 1 question with claude in the pane on the right, which closes once they are " +
			"answered. Type /exit there to leave the rest open; Ctrl+C here stops.\n"; out.String() != want {
			t.Errorf("orchestra's pane:\n%s\nwant:\n%s", out.String(), want)
		}
		issues := readIssues(t, state)
		if q := issues["k-7"]; q.Status != "closed" || !strings.Contains(q.Notes, "Response: JSON") {
			t.Errorf("the question: %+v", q)
		}
		if got := issues["k-1"].Design; got != decided {
			t.Errorf("the blocked ticket's design %q, want %q", got, decided)
		}
	})
}

// A question left open, the user leaving the session (/exit) before answering it, is said to be open;
// the pane, gone with claude, isn't waited on further.
func TestAnswerTalkLeftWithAQuestionOpen(t *testing.T) {
	t.Parallel()
	repo, _, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle,
			dispatch.StateGone}}
		var out, errOut strings.Builder
		r, err := paneAnswerTalk(t, repo, panes, &out, &errOut).answer(runCtx(t), "", "")
		if err != nil || r.Answered != nil || !slices.Equal(r.Open, []string{"k-7"}) {
			t.Fatalf("answer = %+v, %v; stderr:\n%s", r, err, errOut.String())
		}
		if q := readIssues(t, state)["k-7"]; q.Status != "open" {
			t.Errorf("the question: %+v", q)
		}
	})
}

// A question with no options in its metadata, asked by hand or before the rule, is offered with
// options the session drafts from its description, which the questions file asks for.
func TestAnswerTalkHasOptionsDraftedForAQuestionWithout(t *testing.T) {
	t.Parallel()
	backlog := answerBacklog()
	backlog["k-7"].Metadata = nil
	repo, _, _ := answerSetup(t, backlog)
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateGone}}
		var out, errOut strings.Builder
		if _, err := paneAnswerTalk(t, repo, panes, &out, &errOut).answer(runCtx(t), "", "k-7"); err != nil {
			t.Fatal(err)
		}
		questions, err := os.ReadFile(filepath.Join(repo, project.RunPath(project.AnswerQuestionsName)))
		if err != nil || !strings.Contains(string(questions), "Options: none recorded. Draft 2 to 4 from the "+
			"description, the one you recommend first, and say that you drafted them.") ||
			strings.Contains(string(questions), "(Recommended)") {
			t.Errorf("the questions (%v):\n%s", err, questions)
		}
	})
}

// Where orchestra's output isn't a terminal, the talk can't take it: it says how to answer instead.
func TestAnswerTalkNeedsATerminal(t *testing.T) {
	t.Parallel()
	repo, _, _ := answerSetup(t, answerBacklog())
	var out strings.Builder
	a := answerTalk{claude: claudeSession{repo: repo}, tracker: beads.Tracker{Repo: repo}, out: &out, err: &out}
	a.terminal = func(ctx context.Context, message string) error {
		return answerOnTerminal(ctx, nil, a.claude, message, strings.NewReader(""), &out, &out)
	}
	r, err := a.answer(runCtx(t), "", "")
	if err == nil || !strings.Contains(err.Error(), "bd human respond <question>") || !slices.Equal(r.Open, []string{"k-7"}) {
		t.Errorf("answer = %+v, %v", r, err)
	}
}
