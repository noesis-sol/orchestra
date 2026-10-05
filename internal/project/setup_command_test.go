package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFindSetupOffersACommandPerEcosystemsLockfile(t *testing.T) {
	for _, tc := range []struct {
		files     []string
		command   string
		lockfiles []string
	}{
		{nil, "", nil},
		{[]string{"go.sum", "go.mod"}, "", nil},
		{[]string{"package-lock.json"}, "npm ci", []string{"package-lock.json"}},
		{[]string{"pnpm-lock.yaml"}, "pnpm install --frozen-lockfile", []string{"pnpm-lock.yaml"}},
		{[]string{"yarn.lock"}, "yarn install --frozen-lockfile", []string{"yarn.lock"}},
		{[]string{"uv.lock"}, "uv sync", []string{"uv.lock"}},
		{[]string{"poetry.lock"}, "poetry install", []string{"poetry.lock"}},
		{[]string{"Gemfile.lock"}, "bundle install", []string{"Gemfile.lock"}},
		// One command per ecosystem, the first lockfile's.
		{[]string{"yarn.lock", "package-lock.json", "Gemfile.lock"}, "npm ci && bundle install",
			[]string{"package-lock.json", "Gemfile.lock"}},
		// Only at the repository's top.
		{[]string{"web/package-lock.json"}, "", nil},
	} {
		repo := t.TempDir()
		for _, f := range tc.files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, f)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, f), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		command, lockfiles := FindSetup(repo)
		if command != tc.command || !slices.Equal(lockfiles, tc.lockfiles) {
			t.Errorf("%q: FindSetup = %q, %q; want %q, %q", tc.files, command, lockfiles, tc.command, tc.lockfiles)
		}
	}
}

func TestSetupStepSaysWhatInitDidWithTheSetupCommand(t *testing.T) {
	found := Choice{SetupOffer: "npm ci", SetupFrom: []string{"package-lock.json"}}
	with := func(c Choice, setup, was string, unasked bool) Choice {
		c.Setup, c.SetupWas, c.SetupUnasked = setup, was, unasked
		return c
	}
	for _, tc := range []struct {
		name string
		c    Choice
		kind StepKind
		want string // in the detail; "" for no step
	}{
		{"nothing to offer", Choice{}, 0, ""},
		{"set", with(found, "npm ci", "", false), StepDone, "'npm ci' runs before check-fast"},
		{"kept", with(Choice{}, "make deps", "make deps", false), StepKept, "settings.json has it; --setup replaces it"},
		{"replaced", with(found, "pnpm i", "npm ci", false), StepDone, "'pnpm i' runs"},
		{"removed", with(found, "", "npm ci", false), StepDone, "removed 'npm ci'"},
		{"unasked", with(found, "", "", true), StepCaution,
			"not asked (no terminal): found package-lock.json, and a ticket rebased"},
		{"unasked flag", with(found, "", "", true), StepCaution, "--setup 'npm ci' sets"},
		{"declined", with(found, "", "", false), StepKept, "found package-lock.json; no setup command"},
	} {
		s, ok := SetupStep(tc.c)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: a step %+v, want none", tc.name, s)
			}
			continue
		}
		if !ok || s.Kind != tc.kind || s.Label != "setup" || !strings.Contains(s.Detail, tc.want) {
			t.Errorf("%s: step %+v, ok %v; want kind %v with %q", tc.name, s, ok, tc.kind, tc.want)
		}
	}
}
