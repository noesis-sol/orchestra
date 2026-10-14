package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oneStep runs apply, which says at most one thing, and returns its step; ok is false where it says
// nothing.
func oneStep(t *testing.T, apply Applier, repo string, c Choice) (s Step, ok bool, err error) {
	t.Helper()
	steps, err := apply(context.Background(), repo, c)
	switch len(steps) {
	case 0:
		return Step{}, false, err
	case 1:
		return steps[0], true, err
	}
	t.Fatalf("%d steps, want at most one: %+v", len(steps), steps)
	return Step{}, false, nil
}

func TestApplySetsTheProjectUpInOrder(t *testing.T) {
	repo, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, changelogName), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := DefaultChoice(Settings{CheckFast: "make check"}, "")
	c.Union, c.Verifier, c.Tests, c.Agent = true, true, TestsScratch, "claude"
	steps, err := Apply(context.Background(), repo, c, false)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, s := range steps {
		labels = append(labels, s.Label)
	}
	want := "worker prompt, .gitignore, check-fast, check-full, settings, MCP servers, changelog, skill, verifier"
	if got := strings.Join(labels, ", "); got != want {
		t.Errorf("steps: %s\nwant:  %s", got, want)
	}
	// The verifier reads what was written before it: the runner's suites, and the worker prompt.
	if got := readRepoFile(t, repo, VerifierPath); !strings.Contains(got, "`make check`") {
		t.Errorf("the verifier lacks the runner's suite:\n%s", got)
	}
	if got := readRepoFile(t, repo, Dir+"/"+promptName); !strings.HasSuffix(got, verifierSection) {
		t.Errorf("the worker prompt doesn't end with the Verify section:\n%s", got)
	}
}

// An applier's error stops Apply: the steps so far are kept, and nothing after it runs.
func TestApplyStopsAtTheFirstError(t *testing.T) {
	repo, _ := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	// Settings that can't be read: ApplySettings fails, after the prompt and the runners.
	if err := os.WriteFile(SettingsPath(repo), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, changelogName), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps, err := Apply(context.Background(), repo, Choice{Fast: &[]Suite{}, Concurrent: 1, Union: true}, false)
	if err == nil {
		t.Fatal("no error for settings that can't be read")
	}
	if n := len(steps); n == 0 || steps[n-1].Label != "check-full" {
		t.Errorf("steps %+v, want those up to the runners", steps)
	}
	if fileExists(filepath.Join(repo, attributesName)) {
		t.Error("the changelog's union was added after the error")
	}
}

func TestInitPromptForceReplacesThePrompt(t *testing.T) {
	repo, _ := gitRepo(t)
	if _, err := initPrompt(false)(context.Background(), repo, Choice{}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(repo, Dir, promptName)
	if err := os.WriteFile(p, []byte("the project's own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		force bool
		kind  StepKind
		ours  bool // the project's prompt is still there
	}{{false, StepKept, true}, {true, StepDone, false}} {
		steps, err := initPrompt(tc.force)(context.Background(), repo, Choice{})
		if err != nil {
			t.Fatal(err)
		}
		if steps[0].Kind != tc.kind || (read(t, p) == "the project's own\n") != tc.ours {
			t.Errorf("force %v: %+v, prompt %q", tc.force, steps[0], read(t, p))
		}
	}
	if got := read(t, p); !strings.Contains(got, "`"+FastRunner+"`") {
		t.Errorf("the template's check command isn't the runner:\n%s", got)
	}
}

func TestQuestionsAny(t *testing.T) {
	for _, tc := range []struct {
		q    Questions
		want bool
	}{
		{Questions{}, false},
		{Questions{Check: true}, true},
		{Questions{Prefix: true}, true},
		{Questions{Concurrent: true, MCP: true}, true},
	} {
		if got := tc.q.Any(); got != tc.want {
			t.Errorf("%+v.Any() = %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestDeclineUnasked(t *testing.T) {
	none := []string{}
	for _, tc := range []struct {
		name    string
		c       Choice
		q       Questions
		want    Questions
		offered bool // Union, InstallBeads and Verifier stay as offered
	}{
		{"nothing unasked", Choice{Union: true, InstallBeads: true, Verifier: true}, Questions{}, Questions{}, true},
		{"the offers declined",
			Choice{Union: true, InstallBeads: true, Verifier: true},
			Questions{Union: true, Install: true, Verifier: true, Setup: true},
			Questions{Union: true, Install: true, Verifier: true, Setup: true}, false},
		{"concurrency fallen back", Choice{Unasked: Questions{Concurrent: true}},
			Questions{Concurrent: true}, Questions{Concurrent: true}, false},
		{"concurrency from the settings", Choice{Concurrent: 3},
			Questions{Concurrent: true}, Questions{}, false},
		{"concurrency given", Choice{Unasked: Questions{Concurrent: true}}, Questions{}, Questions{}, false},
		{"MCP servers unset", Choice{}, Questions{MCP: true}, Questions{MCP: true}, false},
		{"MCP servers from the settings", Choice{MCP: &none}, Questions{MCP: true}, Questions{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			c.DeclineUnasked(tc.q)
			if c.Unasked != tc.want {
				t.Errorf("Unasked = %+v, want %+v", c.Unasked, tc.want)
			}
			offered := tc.c.Union && c.Union && c.InstallBeads && c.Verifier
			if offered != tc.offered || (!tc.offered && (c.Union || c.InstallBeads || c.Verifier)) {
				t.Errorf("Union %v, InstallBeads %v, Verifier %v", c.Union, c.InstallBeads, c.Verifier)
			}
		})
	}
}
