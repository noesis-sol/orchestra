package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fullChoice is a choice that has Apply write every file it writes: the changelog's union, the
// verifier and the skill among them.
func fullChoice(check string) Choice {
	c := DefaultChoice(Settings{CheckFast: check}, "")
	c.Union, c.Verifier, c.Tests, c.Agent = true, true, TestsScratch, "claude"
	return c
}

// repoTree is what is in the repository at repo outside .git: each file's mode and content, and each
// folder, by its path from the top.
func repoTree(t *testing.T, repo string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repo, p) // p is under repo
		switch {
		case rel == ".git":
			return filepath.SkipDir
		case rel == ".":
			return nil
		case d.IsDir():
			tree[filepath.ToSlash(rel)] = "folder"
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		tree[filepath.ToSlash(rel)] = fmt.Sprintf("%v %q", fi.Mode(), b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// treeDiff is the paths whose state differs between the trees a and b, sorted.
func treeDiff(a, b map[string]string) []string {
	var diff []string
	for p, s := range a {
		if b[p] != s {
			diff = append(diff, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			diff = append(diff, p)
		}
	}
	slices.Sort(diff)
	return diff
}

// Every file Apply writes is one of setupPaths, which a failure puts back.
func TestSetupPathsHoldWhatApplyWrites(t *testing.T) {
	repo, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, changelogName), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ReadmeName), []byte("# App\n\n"+RequiresStart+"\n"+RequiresEnd+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	c := fullChoice("make check")
	before := repoTree(t, repo)
	if _, err := Apply(context.Background(), repo, c, false); err != nil {
		t.Fatal(err)
	}
	paths := setupPaths(c)
	diff := treeDiff(before, repoTree(t, repo))
	for _, p := range diff {
		if !slices.Contains(paths, p) {
			t.Errorf("Apply wrote %s, which isn't among setupPaths", p)
		}
	}
	for _, want := range []string{Dir + "/" + promptName, Dir + "/" + SettingsName, FastRunner, VerifierPath,
		attributesName, ReadmeName} {
		if !slices.Contains(diff, want) {
			t.Errorf("Apply didn't write %s: %q", want, diff)
		}
	}
}

// A failure at any of Apply's appliers, once it has written what it writes, leaves the repository as
// it was: not set up where it wasn't, and with its old setup where it was, never a worker prompt
// without the settings.
func TestApplyFailingPutsTheSetUpBack(t *testing.T) {
	failed := errors.New("disk full")
	for _, setUp := range []bool{false, true} {
		for i := range setupAppliers(false) {
			t.Run(fmt.Sprintf("set up %v, applier %d", setUp, i), func(t *testing.T) {
				t.Parallel()
				repo, _ := gitRepo(t)
				if err := os.WriteFile(filepath.Join(repo, changelogName), []byte("# Changelog\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				c := fullChoice("make check")
				if setUp {
					if _, err := Apply(context.Background(), repo, Choice{Fast: &[]Suite{{Command: "go test ./..."}},
						Concurrent: 2}, false); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(repo, Dir, promptName), []byte("the project's own\n"),
						0o600); err != nil {
						t.Fatal(err)
					}
					c.ReplaceFast = true
				}
				before := repoTree(t, repo)
				appliers := setupAppliers(setUp)
				apply := appliers[i]
				appliers[i] = func(ctx context.Context, repo string, c Choice) ([]Step, error) {
					steps, err := apply(ctx, repo, c)
					if err != nil {
						t.Fatalf("applier %d: %v", i, err)
					}
					return steps, failed
				}
				steps, err := applyAll(context.Background(), repo, c, appliers)
				if !errors.Is(err, failed) || steps != nil {
					t.Errorf("steps %+v, error %v; want none, %v", steps, err, failed)
				}
				if diff := treeDiff(before, repoTree(t, repo)); len(diff) > 0 {
					t.Errorf("left changed: %q", diff)
				}
				if IsSetUp(repo) != setUp {
					t.Errorf("IsSetUp = %v", !setUp)
				}
			})
		}
	}
}

// A write that fails, the runners' folder a file, puts back the worker prompt written before it.
func TestApplyFailingToWriteRemovesThePrompt(t *testing.T) {
	repo, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "scripts"), []byte("not a folder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := repoTree(t, repo)
	steps, err := Apply(context.Background(), repo, fullChoice("make check"), false)
	if err == nil || steps != nil || !strings.Contains(err.Error(), "the setup is as it was before") {
		t.Errorf("steps %+v, error %v; want none, and an error", steps, err)
	}
	if diff := treeDiff(before, repoTree(t, repo)); len(diff) > 0 {
		t.Errorf("left changed: %q", diff)
	}
	if IsSetUp(repo) {
		t.Error("set up")
	}
}

// A worker prompt that init moved from .claude/ goes back there when a later step fails, git's index
// with it.
func TestApplyFailingMovesTheOldPromptBack(t *testing.T) {
	repo, git := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, legacyPrompt), []byte("Work on TICKET_ID.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", legacyPrompt)
	git(repo, "commit", "-q", "-m", "prompt")
	before, status := repoTree(t, repo), git(repo, "status", "--porcelain")
	failed := errors.New("disk full")
	appliers := append(setupAppliers(false), func(context.Context, string, Choice) ([]Step, error) {
		return nil, failed
	})
	if _, err := applyAll(context.Background(), repo, fullChoice("make check"), appliers); !errors.Is(err, failed) {
		t.Fatalf("error %v, want %v", err, failed)
	}
	if diff := treeDiff(before, repoTree(t, repo)); len(diff) > 0 {
		t.Errorf("left changed: %q", diff)
	}
	if got := git(repo, "status", "--porcelain"); got != status {
		t.Errorf("git status:\n%s\nwant:\n%s", got, status)
	}
	if !IsSetUp(repo) {
		t.Error("the project set up before orchestra init isn't any more")
	}
}
