//go:build unix

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// setupBd is specTreeBd answering bd init too, as bd does where Beads isn't set up: it makes .beads/
// and, where the file bd-commits is beside it, commits that, as bd 1.3 does.
var setupBd = strings.Replace(specTreeBd, "case \"$cmd\" in\n", "case \"$cmd\" in\n"+
	"init) mkdir -p .beads && echo 'issue-prefix: app' > .beads/config.yaml\n"+
	"\tif [ -e \"$d/bd-commits\" ]; then git add .beads && git commit -q -m 'bd init' -- .beads; fi ;;\n", 1)

// emptyRepoFixture is specRunFixture with what git init alone makes: a repository with no commit,
// HEAD naming main, and nothing of orchestra's or Beads' in it. Its bd is setupBd.
func emptyRepoFixture(t *testing.T) (repo, spec, tools string) {
	t.Helper()
	repo = specFixture(t)
	for _, dir := range []string{project.Dir, ".beads"} {
		if err := os.RemoveAll(filepath.Join(repo, dir)); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "update-ref", "-d", "HEAD") // main, and with it the fixture's only commit
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0"} {
		setenv(t, k, v)
	}
	tools = t.TempDir()
	faketool.Write(t, tools, "bd", setupBd)
	faketool.Write(t, tools, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	spec = writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n\nIt lists things, sorted.\n")
	return repo, spec, tools
}

// A spec run in a repository with no commit, which orchestra init hasn't set up, sets it up as init
// does without a terminal (bd init, the worker prompt, runners that check nothing yet, the
// settings: concurrency from -c, else 1, and no MCP servers; no test work, no skill), commits that
// with the spec on the branch HEAD names, and goes on to the breakdown and the run with those
// settings. Where bd init commits its own files, as bd does, the setup's commit holds the rest. A
// file of the user's that the checkout's check leaves aside stays out of it.
func TestSpecRunSetsUpAnEmptyRepository(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	const kept = ".orchestra/specs/app.md"
	setUp := ".orchestra/.gitignore .orchestra/settings.json " + kept + " .orchestra/worker-prompt.md " +
		"scripts/check-fast.sh scripts/check-full.sh"
	for _, tc := range []struct {
		name       string
		args       []string
		bdCommits  bool
		commits    string // in the branch
		files      string // in the setup's commit
		concurrent int
	}{
		{"bd commits nothing", nil, false, "1", ".beads/config.yaml " + setUp, 1},
		{"bd commits its own, with -c", []string{"-c", "2"}, true, "2", setUp, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, spec, tools := emptyRepoFixture(t)
			if tc.bdCommits {
				writeSpec(t, filepath.Join(tools, "bd-commits"), "yes\n")
			}
			mine := writeSpec(t, filepath.Join(repo, ".claude", "notes.md"), "mine\n")
			f := &fakeBreakdown{repo: repo, root: "s-1"}
			useBreakdown(t, f)

			stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"),
				append([]string{"--spec", spec, "--plain"}, tc.args...)...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			head := gitIn(t, repo, "rev-parse", "main")
			if n := gitIn(t, repo, "rev-list", "--count", "main"); n != tc.commits || gitIn(t, repo, "rev-parse", "HEAD") != head {
				t.Errorf("%s commits on main, want %s; HEAD isn't main", n, tc.commits)
			}
			if subject := gitIn(t, repo, "log", "-1", "--format=%s"); subject != "orchestra: set up for spec app.md" {
				t.Errorf("HEAD: %q", subject)
			}
			if files := strings.Fields(gitIn(t, repo, "show", "--name-only", "--format=", "HEAD")); strings.Join(files, " ") != tc.files {
				t.Errorf("the setup's commit holds:\n%s\nwant:\n%s", strings.Join(files, " "), tc.files)
			}
			if status := gitIn(t, repo, "status", "--porcelain", "--", ".", ":(exclude).claude"); status != "" {
				t.Errorf("left uncommitted:\n%s", status)
			}
			if _, err := os.Stat(mine); err != nil || gitIn(t, repo, "ls-files", ".claude") != "" {
				t.Errorf("the user's file in .claude/ is gone or committed (%v)", err)
			}
			if f.got.Path != kept || f.got.Commit != head {
				t.Errorf("the breakdown was asked %+v, want %s at %s", f.got, kept, head)
			}

			settings, _, err := project.LoadSettings(repo)
			if err != nil {
				t.Fatal(err)
			}
			given := project.Given[int]{Value: tc.concurrent, Given: tc.args != nil}
			want := project.ChoiceSettings(project.Settings{}, specSetupChoice(repo, given, "claude"))
			// The worker prompt written from the template, whose version the settings record; no verifier.
			want.Templates = &project.Templates{WorkerPrompt: project.CurrentTemplates().WorkerPrompt}
			if !reflect.DeepEqual(settings, want) {
				t.Errorf("settings %+v, the run read %+v", settings, want)
			}
			if settings.Concurrency != tc.concurrent || settings.MCPServers == nil || len(*settings.MCPServers) != 0 {
				t.Errorf("settings: concurrent %d, MCP servers %v", settings.Concurrency, settings.MCPServers)
			}
			if _, err := os.Stat(filepath.Join(repo, ".claude", "skills")); !os.IsNotExist(err) {
				t.Errorf("a skill was installed (%v)", err)
			}
			calls := read(t, filepath.Join(tools, "bd-calls"))
			if !strings.Contains(calls, "init|--non-interactive|--role|maintainer|--init-if-missing|\n") ||
				strings.Count(calls, "create|") != 4 {
				t.Errorf("bd init wasn't run, or test work was filed besides the breakdown's 4 tickets:\n%s", calls)
			}

			short := head[:12]
			for _, want := range []string{"check-fast: wrote scripts/check-fast.sh: it checks nothing yet",
				"committed the setup with the spec " + kept + " as " + short + " (",
				"the spec " + kept + " is committed already (sha256 ",
				fmt.Sprintf("concurrent: %d,", tc.concurrent), "MCP servers: none)"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if log := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(log,
				"SPEC: committed the setup with the spec "+kept+" as "+short+" (") {
				t.Errorf("the log:\n%s", log)
			}
		})
	}
}

// A spec run in a repository set up already sets nothing up: no bd init, no runners, settings as
// they were, and only the spec committed.
func TestSpecRunLeavesASetUpRepositoryAlone(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, tools := specRunFixture(t)
	useBreakdown(t, &fakeBreakdown{repo: repo, root: "s-1"})
	before := gitIn(t, repo, "rev-parse", "HEAD")
	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if parent, subject := gitIn(t, repo, "rev-parse", "HEAD~1"), gitIn(t, repo, "log", "-1", "--format=%s"); parent != before ||
		subject != "orchestra: add spec app.md" {
		t.Errorf("HEAD: %q after %s, want the spec's commit after %s", subject, parent, before)
	}
	if strings.Contains(read(t, filepath.Join(tools, "bd-calls")), "init|") || strings.Contains(stdout, "setting it up") {
		t.Errorf("set the repository up; stdout:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(repo, "scripts")); !os.IsNotExist(err) {
		t.Errorf("wrote runners (%v)", err)
	}
	if settings := read(t, project.SettingsPath(repo)); settings != `{"concurrent": 1}` {
		t.Errorf("settings: %s", settings)
	}
}

// What stops a spec run before it sets anything up: a folder that isn't a git repository, bd
// missing (nothing is installed unattended), and a main checkout with uncommitted changes.
func TestSpecRunSetupProblems(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	t.Run("not a repository", func(t *testing.T) {
		_, spec, _ := emptyRepoFixture(t)
		chdir(t, t.TempDir())
		_, stderr, code := runSpecIn(t, "", func(string) {}, "--spec", spec, "--plain")
		if code != 2 || !strings.Contains(stderr, "  - "+noRepoForSpec+"\n") || strings.Contains(stderr, "orchestra init") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
	})
	t.Run("bd missing", func(t *testing.T) {
		repo, spec, _ := emptyRepoFixture(t)
		setenv(t, "PATH", faketool.Path(t, "", "git"))
		_, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
		if code != 2 || !strings.Contains(stderr, "  - Required command not found: bd\n") ||
			strings.Contains(stderr, "isn't set up") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
		notSetUp(t, repo)
	})
	t.Run("uncommitted changes", func(t *testing.T) {
		repo, spec, tools := emptyRepoFixture(t)
		writeSpec(t, filepath.Join(repo, "stray.txt"), "not mine\n")
		_, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
		if code != 5 || !strings.Contains(stderr, "orchestra cannot start: uncommitted changes in") {
			t.Errorf("exit %d, stderr:\n%s", code, stderr)
		}
		notSetUp(t, repo)
		if _, err := os.Stat(filepath.Join(tools, "bd-calls")); !os.IsNotExist(err) {
			t.Errorf("bd was called (%v)", err)
		}
	})
}

// notSetUp checks that the repository has no commit yet, and neither settings nor runners.
func notSetUp(t *testing.T, repo string) {
	t.Helper()
	if head, _ := (git.Git{}).Head(context.Background(), repo, "HEAD"); head != "" {
		t.Errorf("committed %s", head)
	}
	for _, p := range []string{project.SettingsPath(repo), filepath.Join(repo, "scripts")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s is there (%v)", p, err)
		}
	}
}
