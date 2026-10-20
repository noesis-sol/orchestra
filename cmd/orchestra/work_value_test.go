package main

import (
	"io"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/tui"
)

// What the flags and variables say a run works on is one work value, a spec before a feature before
// a ticket before the current tickets; a planned run's epic, once filed, scopes it without changing it.
func TestWorkFromTheFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want work
	}{
		{"nothing", nil, nil, work{}},
		{"--tickets", []string{"--tickets"}, nil, work{kind: workTickets}},
		{"ORCHESTRA_TICKETS=1", nil, map[string]string{"ORCHESTRA_TICKETS": "1"}, work{kind: workTickets}},
		{"--ticket", []string{"--ticket", "x-1"}, nil, work{kind: workTicket, ticket: "x-1"}},
		{"--ticket with --tickets", []string{"--ticket", "x-1", "--tickets"}, nil, work{kind: workTicket, ticket: "x-1"}},
		{"--feature", []string{"--feature", " Add a flag "}, nil, work{kind: workFeature, feature: "Add a flag"}},
		{"--feature over ORCHESTRA_TICKETS=1", []string{"--feature", "Add a flag"},
			map[string]string{"ORCHESTRA_TICKETS": "1"}, work{kind: workFeature, feature: "Add a flag"}},
		{"--spec --refine", []string{"--spec", "/specs/app.md", "--refine"}, nil,
			work{kind: workSpec, spec: "/specs/app.md", refine: true}},
		{"ORCHESTRA_SPEC", nil, map[string]string{"ORCHESTRA_SPEC": "/specs/app.md"},
			work{kind: workSpec, spec: "/specs/app.md"}},
	} {
		f, problems, err := readFlags(tc.args, specEnv(tc.env), io.Discard)
		if err != nil || len(problems) > 0 || f.work() != tc.want {
			t.Errorf("%s: work %+v, want %+v (%v, %q)", tc.name, f.work(), tc.want, err, problems)
		}
	}

	c := options{specFile: "/specs/app.md"}
	c.Ticket = "s-1" // the root epic its breakdown filed
	if w := c.work(); w.kind != workSpec || !w.planned() {
		t.Errorf("a spec run scoped to its epic: %+v", w)
	}
}

// A spec or a feature goes with no other work; --refine only with one of them, --ticket included.
func TestWorkFlagsGivenTogether(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"--refine with --ticket", []string{"--ticket", "x-1", "--refine"},
			"--refine only applies with --spec or --feature."},
		{"--refine with --tickets", []string{"--tickets", "--refine"}, "--refine only applies with --spec or --feature."},
		{"--refine with --feature", []string{"--feature", "Add a flag", "--refine"}, ""},
		{"--feature with --tickets", []string{"--feature", "Add a flag", "--tickets"},
			"--feature can't be combined with --tickets: one runs a feature request, the other the current tickets."},
		{"--feature with --ticket", []string{"--feature", "Add a flag", "--ticket", "x-1"},
			"--feature can't be combined with --ticket (or ORCHESTRA_TICKET): a feature run is scoped to the epic it files."},
		{"--spec with a limit reached", []string{"--spec", "/specs/app.md", "--limit", "2", "--done-so-far", "2"},
			"--spec would break the spec down and run none of it: --done-so-far (2) has reached --limit (2)."},
	} {
		_, problems, err := readFlags(tc.args, specEnv(nil), io.Discard)
		if err != nil || tc.want == "" && len(problems) > 0 ||
			tc.want != "" && (len(problems) != 1 || !strings.HasPrefix(problems[0], tc.want)) {
			t.Errorf("%s: problems %q, want one starting %q (%v)", tc.name, problems, tc.want, err)
		}
	}
}

// The start question's answer is the work its flags would have given.
func TestWorkAnswered(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		answer tui.Work
		want   work
	}{
		{tui.Work{Kind: tui.WorkTickets}, work{kind: workTickets}},
		{tui.Work{Kind: tui.WorkSpec, Spec: "/specs/app.md", Refine: true},
			work{kind: workSpec, spec: "/specs/app.md", refine: true}},
		{tui.Work{Kind: tui.WorkFeature, Description: "Add a flag"}, work{kind: workFeature, feature: "Add a flag"}},
	} {
		var c options
		c.putWork(answered(tc.answer))
		if got := c.work(); got != tc.want {
			t.Errorf("%+v: work %+v, want %+v", tc.answer, got, tc.want)
		}
	}
}
