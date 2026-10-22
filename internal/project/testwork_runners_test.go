package project

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The ticket that sets up tests from scratch says which runners check nothing as init left them: a
// check-fast.sh kept is the project's own, and check-full.sh runs it.
func TestScratchTicketReadsTheRunnersAsWritten(t *testing.T) {
	kept := "#!/bin/sh\nmake test\n"
	none := []Suite{}
	for _, tc := range []struct {
		name    string
		replace bool
		empty   []string
		says    []string
		notSays []string
	}{
		{"check-fast.sh kept", false, nil,
			[]string{FastRunner + " and " + FullRunner + " run the checks the project had."},
			[]string{"checking nothing", "nothing merged is checked"}},
		{"check-fast.sh replaced", true, []string{FastRunner, FullRunner},
			[]string{"orchestra init wrote " + FastRunner + " and " + FullRunner + " checking nothing (exit 0): " +
				"until this ticket is done, nothing merged is checked."},
			[]string{"the checks the project had"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, FastRunner), []byte(kept), 0o755); err != nil {
				t.Fatal(err)
			}
			c := Choice{Tests: TestsScratch, Fast: &none, Full: &none, ReplaceFast: tc.replace}
			if _, err := ApplyRunners(context.Background(), repo, c); err != nil {
				t.Fatal(err)
			}
			empty, err := RunnersCheckingNothing(repo)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(empty, tc.empty) {
				t.Fatalf("checking nothing: %q; want %q", empty, tc.empty)
			}
			plan := TestWork(c, "main", empty)
			if len(plan) != 2 {
				t.Fatalf("plan: %+v", plan)
			}
			desc := plan[1].Description
			for _, want := range tc.says {
				if !strings.Contains(desc, want) {
					t.Errorf("doesn't say %q:\n%s", want, desc)
				}
			}
			for _, unwanted := range tc.notSays {
				if strings.Contains(desc, unwanted) {
					t.Errorf("says %q:\n%s", unwanted, desc)
				}
			}
		})
	}
}

func TestRunnersAsWritten(t *testing.T) {
	for _, tc := range []struct {
		empty []string
		want  string
	}{
		{[]string{FastRunner, FullRunner}, "orchestra init wrote " + FastRunner + " and " + FullRunner +
			" checking nothing (exit 0): until this ticket is done, nothing merged is checked."},
		{[]string{FastRunner}, "orchestra init wrote " + FastRunner + " checking nothing (exit 0); " + FullRunner +
			" runs the checks the project had: until this ticket is done, nothing merged is checked."},
		{[]string{FullRunner}, "orchestra init wrote " + FullRunner + " checking nothing (exit 0); " + FastRunner +
			" runs the checks the project had."},
		{nil, FastRunner + " and " + FullRunner + " run the checks the project had."},
	} {
		if got := runnersAsWritten(tc.empty); got != tc.want {
			t.Errorf("%q:\n%s\nwant\n%s", tc.empty, got, tc.want)
		}
	}
}

// A runner missing, or one that runs nothing but exit 0, checks nothing; an unreadable one is an error.
func TestRunnersCheckingNothingReadsEachRunner(t *testing.T) {
	repo := t.TempDir()
	empty, err := RunnersCheckingNothing(repo)
	if err != nil || !slices.Equal(empty, []string{FastRunner, FullRunner}) {
		t.Errorf("no runners: %q, %v", empty, err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, FastRunner), []byte("#!/bin/sh\n# nothing\n\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, FullRunner), []byte(FullScript(nil, false)), 0o755); err != nil {
		t.Fatal(err)
	}
	if empty, err = RunnersCheckingNothing(repo); err != nil || !slices.Equal(empty, []string{FastRunner}) {
		t.Errorf("exit 0 and a check-full.sh running it: %q, %v", empty, err)
	}
	unread := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unread, FullRunner), 0o755); err != nil { // a directory: no script to read
		t.Fatal(err)
	}
	if _, err := RunnersCheckingNothing(unread); err == nil {
		t.Error("an unreadable check-full.sh: no error")
	}
}
