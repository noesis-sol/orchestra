package main

import (
	"io"
	"slices"
	"strings"
	"testing"
)

// --overnight (or ORCHESTRA_OVERNIGHT=1) runs the current tickets, or with --ticket that ticket, and
// never asks what to work on; it can't be combined with a planned run, and --force-overnight goes
// only with it.

func TestOvernightFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		args      []string
		env       map[string]string
		problem   string // "" for none
		kind      workKind
		overnight bool
	}{
		{"flag", []string{"--overnight"}, nil, "", workTickets, true},
		{"variable", nil, map[string]string{"ORCHESTRA_OVERNIGHT": "1"}, "", workTickets, true},
		{"variable off", nil, map[string]string{"ORCHESTRA_OVERNIGHT": "0"}, "", workUnsaid, false},
		{"flag over the variable", []string{"--overnight=false"}, map[string]string{"ORCHESTRA_OVERNIGHT": "1"}, "",
			workUnsaid, false},
		{"with --tickets", []string{"--overnight", "--tickets"}, nil, "", workTickets, true},
		{"with --ticket", []string{"--overnight", "--ticket", "k-1"}, nil, "", workTicket, true},
		{"forced", []string{"--overnight", "--force-overnight"}, nil, "", workTickets, true},
		{"forced alone", []string{"--force-overnight"}, nil, "--force-overnight only applies with --overnight.",
			workUnsaid, false},
		{"with --spec", []string{"--overnight", "--spec", "shop.md"}, nil,
			"--spec can't be combined with --overnight: one runs a spec, the other the current tickets overnight.",
			workSpec, true},
		{"variable with --feature", []string{"--feature", "Add a flag"}, map[string]string{"ORCHESTRA_OVERNIGHT": "1"},
			"--feature can't be combined with ORCHESTRA_OVERNIGHT: one runs a feature request, " +
				"the other the current tickets overnight.", workFeature, true},
		{"bad variable", nil, map[string]string{"ORCHESTRA_OVERNIGHT": "tonight"},
			"ORCHESTRA_OVERNIGHT must be 1 or 0", workUnsaid, false},
	} {
		getenv := func(k string) string { return tc.env[k] }
		f, problems, err := readFlags(tc.args, getenv, io.Discard)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		switch {
		case tc.problem == "" && len(problems) > 0:
			t.Errorf("%s: problems %q, want none", tc.name, problems)
		case tc.problem != "" && !slices.ContainsFunc(problems, func(p string) bool {
			return strings.HasPrefix(p, tc.problem)
		}):
			t.Errorf("%s: problems %q, want %q", tc.name, problems, tc.problem)
		}
		if f.work().kind != tc.kind || f.Overnight != tc.overnight {
			t.Errorf("%s: works on %v, overnight %v; want %v, %v", tc.name, f.work().kind, f.Overnight, tc.kind,
				tc.overnight)
		}
		if asksWork(f.options, true) && tc.overnight {
			t.Errorf("%s: an overnight run asks what to work on", tc.name)
		}
	}
}
