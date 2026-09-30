package beads

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestParseReadySortsOpenTicketsByPriority(t *testing.T) {
	raw := `[
	  {"id":"a","title":"P3 first in output","status":"open","priority":3},
	  {"id":"b","title":"claimed","status":"in_progress","priority":0},
	  {"id":"c","title":"P1","status":"open","priority":1},
	  {"id":"d","title":"no priority","status":"open"},
	  {"id":"e","title":"second P3","status":"open","priority":3}
	]`
	got, err := parseReady([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tk := range got {
		ids = append(ids, tk.ID)
	}
	if want := "c a e d"; strings.Join(ids, " ") != want {
		t.Errorf("order = %v, want %s", ids, want)
	}
	if got[0].Title != "P1" {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestParseReadyAcceptsTheEnvelopeAndEmptyResults(t *testing.T) {
	got, err := parseReady([]byte(`{"schema_version":2,"data":[{"id":"x","status":"open","priority":2,"dependency_count":1}]}`), nil)
	if err != nil || len(got) != 1 || got[0].ID != "x" {
		t.Errorf("envelope: %v %v", got, err)
	}
	if n := got[0].DependencyCount; n == nil || *n != 1 {
		t.Errorf("dependency count = %v, want 1", n)
	}
	if got, err := parseReady([]byte(`[]`), nil); err != nil || len(got) != 0 {
		t.Errorf("empty: %v %v", got, err)
	}
	if _, err := parseReady([]byte(`not json`), nil); err == nil {
		t.Error("garbage should be an error")
	}
	if _, err := parseReady(nil, nil); err == nil {
		t.Error("no output should be an error")
	}
}

func TestEpicsAreNeverDispatched(t *testing.T) {
	// bd ready returns an open, unblocked epic alongside its children; the children are the work.
	raw := `[{"id":"k-1","title":"Payments","status":"open","priority":1,"issue_type":"epic"},
	         {"id":"k-1.1","title":"work","status":"open","priority":2,"issue_type":"task"}]`
	got, err := parseReady([]byte(raw), []string{"epic"})
	if err != nil || len(got) != 1 || got[0].ID != "k-1.1" {
		t.Errorf("got %+v %v; an epic must be skipped", got, err)
	}
}

func TestExcludedTypesAreNeverDispatched(t *testing.T) {
	raw := `[{"id":"k-1","status":"open","priority":1,"issue_type":"epic"},
	         {"id":"k-2","status":"open","priority":1,"issue_type":"decision"},
	         {"id":"k-3","status":"open","priority":2,"issue_type":"milestone"},
	         {"id":"k-4","status":"open","priority":2,"issue_type":"task"}]`
	ids := func(exclude []string) string {
		got, err := parseReady([]byte(raw), exclude)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, tk := range got {
			ids = append(ids, tk.ID)
		}
		return strings.Join(ids, " ")
	}
	if got := ids([]string{"decision", "milestone"}); got != "k-1 k-4" {
		t.Errorf("excluding decision and milestone: %s", got)
	}
	if got := ids(nil); got != "k-1 k-2 k-3 k-4" {
		t.Errorf("excluding nothing: %s", got)
	}
}

func TestParseTicketStatus(t *testing.T) {
	cases := map[string]string{
		`[{"id":"x","status":"closed"}]`:               "closed",
		`{"id":"x","status":"deferred"}`:               "deferred",
		`{"data":[{"id":"x","status":"in_progress"}]}`: "in_progress",
		`[]`:                    "unknown",
		`[{"id":"x"}]`:          "unknown",
		``:                      "unknown",
		`error: no issue found`: "unknown",
	}
	for raw, want := range cases {
		got := "unknown"
		if tk, ok := parseTicket([]byte(raw)); ok {
			got = tk.Status
		}
		if got != want {
			t.Errorf("parseTicket(%q) status = %q, want %q", raw, got, want)
		}
	}
}

func TestQuestionsAreNeverDispatched(t *testing.T) {
	raw := `[{"id":"q","title":"Decision for k-1: MIT or Apache?","status":"open","priority":1,"labels":["human"]},
	         {"id":"k-2","title":"work","status":"open","priority":2,"labels":["api"]}]`
	got, err := parseReady([]byte(raw), nil)
	if err != nil || len(got) != 1 || got[0].ID != "k-2" {
		t.Errorf("got %+v %v; a human-labelled question must be skipped", got, err)
	}
}

func TestOpenQuestionFromBdShow(t *testing.T) {
	// The shape of 'bd show --json': dependencies carry their own status and labels.
	raw := `[{"id":"k-1","status":"open","labels":["legal"],"dependencies":[
	  {"id":"k-0","status":"closed","labels":["refactor"],"dependency_type":"blocks"},
	  {"id":"q-1","title":"Decision for k-1: MIT or Apache?","status":"open","labels":["human"],"dependency_type":"blocks"}]}]`
	tk, ok := parseTicket([]byte(raw))
	if !ok || tk.Status != "open" {
		t.Fatalf("parseTicket: %v %+v", ok, tk)
	}
	if d := tk.Dependencies[0]; d.ID != "k-0" || d.DependencyType != "blocks" {
		t.Errorf("dependency = %+v, want k-0 blocking", d)
	}
	if q := dispatch.OpenQuestion(tk); q == nil || q.ID != "q-1" {
		t.Errorf("open question = %+v", q)
	}
	tk.Dependencies[1].Status = "closed" // answered
	if q := dispatch.OpenQuestion(tk); q != nil {
		t.Errorf("an answered question should not block: %+v", q)
	}
	if _, ok := parseTicket([]byte("error: not found")); ok {
		t.Error("unreadable output should not parse")
	}
}

func TestOnlyABlocksLinkIsAQuestion(t *testing.T) {
	// A related or parent-child link to an open question doesn't block, so bd ready still
	// returns the ticket; it must be handled by its own status, not reported as ASKED.
	raw := `[{"id":"k-1","status":"deferred","labels":["api"],"dependencies":[
	  {"id":"q-1","title":"Decision for k-0: MIT or Apache?","status":"open","labels":["human"],"dependency_type":"related"},
	  {"id":"q-2","title":"Epic question","status":"open","labels":["human"],"dependency_type":"parent-child"}]}]`
	tk, ok := parseTicket([]byte(raw))
	if !ok || tk.Dependencies[0].DependencyType != "related" {
		t.Fatalf("parseTicket: %v %+v", ok, tk)
	}
	if q := dispatch.OpenQuestion(tk); q != nil {
		t.Errorf("a non-blocking link was taken for an open question: %+v", q)
	}
}

func TestParseClosedKeepsOnlyClosedTickets(t *testing.T) {
	raw := `{"schema_version":1,"data":[{"id":"k-1","status":"closed","labels":["unmerged"]},
	         {"id":"k-2","status":"open","labels":["unmerged"]}]}`
	got, err := parseClosed([]byte(raw))
	if err != nil || len(got) != 1 || got[0].ID != "k-1" || !dispatch.HasLabel(got[0], dispatch.UnmergedLabel) {
		t.Errorf("got %+v %v", got, err)
	}
	if got, err := parseClosed([]byte(`{"data":[],"schema_version":1}`)); err != nil || len(got) != 0 {
		t.Errorf("empty: %v %v", got, err)
	}
	if _, err := parseClosed([]byte(`not json`)); err == nil {
		t.Error("garbage should be an error")
	}
}
