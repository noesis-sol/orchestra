package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two specs from outside the repository with one file name would share one copy: the second is
// refused, naming the first's source and the way out, and the first's copy and commit are left as
// they were. The first, changed, is still committed, recording its source again.
func TestCommitSpecRefusesAnotherSpecOfTheSameName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: commits with real git")
	}
	repo, git := gitRepo(t)
	first := filepath.Join(t.TempDir(), "a", "PRD.md")
	second := filepath.Join(t.TempDir(), "b", "PRD.md")
	writeSpecFile(t, first, "# A\n")
	writeSpecFile(t, second, "# B\n")

	commitSpec(t, repo, first, git)
	const rel = ".orchestra/specs/PRD.md"
	trailer := SpecSourceTrailer + ": " + SpecSource(first)
	if body := git(repo, "log", "-1", "--format=%b"); !strings.Contains(body, "\n\n"+trailer+"\n") {
		t.Errorf("the commit doesn't record the spec's source, %q:\n%s", trailer, body)
	}
	added := head(t, repo, git)

	_, _, err := CommitSpec(context.Background(), repo, second)
	taken, ok := errors.AsType[*SpecTakenError](err)
	if !ok {
		t.Fatalf("the second spec: %v, want a SpecTakenError", err)
	}
	if *taken != (SpecTakenError{File: second, Path: rel, Source: SpecSource(first)}) {
		t.Errorf("the error: %+v", *taken)
	}
	if want := rel + " holds the copy of another spec, " + SpecSource(first) + ", and a spec from outside the " +
		"repository is kept under its file name: give " + second + " another file name"; !strings.HasPrefix(
		err.Error(), want) {
		t.Errorf("the message:\n%s\nwant it to start with:\n%s", err, want)
	}
	if again, ok := errors.AsType[*SpecTakenError](CheckSpecSource(context.Background(), repo, second)); !ok ||
		*again != *taken {
		t.Errorf("CheckSpecSource: %v", again)
	}
	if got := read(t, filepath.Join(repo, filepath.FromSlash(rel))); got != "# A\n" || head(t, repo, git) != added {
		t.Errorf("the first's copy holds %q, HEAD %s; want it untouched at %s", got, head(t, repo, git), added)
	}

	writeSpecFile(t, first, "# A, changed\n")
	if changed := commitSpec(t, repo, first, git); !changed.committed ||
		changed.subject != "orchestra: update spec PRD.md" {
		t.Errorf("the first, changed: committed %v, %q", changed.committed, changed.subject)
	}
	if body := git(repo, "log", "-1", "--format=%b"); !strings.Contains(body, trailer) {
		t.Errorf("the update doesn't record the spec's source:\n%s", body)
	}
}

// A copy whose commits record no source, made before orchestra recorded them, is taken by the spec
// of its name, whose commit records it from then on.
func TestCommitSpecTakesACopyWithNoSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: commits with real git")
	}
	repo, git := gitRepo(t)
	writeSpecFile(t, filepath.Join(repo, ".orchestra", "specs", "PRD.md"), "# Old\n")
	git(repo, "add", ".orchestra/specs/PRD.md")
	git(repo, "commit", "-q", "-m", "orchestra: add spec PRD.md")
	file := filepath.Join(t.TempDir(), "PRD.md")
	writeSpecFile(t, file, "# New\n")
	if got := commitSpec(t, repo, file, git); !got.committed || got.subject != "orchestra: update spec PRD.md" {
		t.Errorf("committed %v, %q", got.committed, got.subject)
	}
	if body := git(repo, "log", "-1", "--format=%b"); !strings.Contains(body, SpecSourceTrailer+": "+SpecSource(file)) {
		t.Errorf("the commit doesn't record the spec's source:\n%s", body)
	}
}

// A spec's source is its absolute path, symlinks resolved, with ~ for the user's home folder.
func TestSpecSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inHome := filepath.Join(home, "specs", "PRD.md")
	writeSpecFile(t, inHome, "x\n")
	if got := SpecSource(inHome); got != "~/specs/PRD.md" {
		t.Errorf("in the home folder: %q", got)
	}
	elsewhere := filepath.Join(t.TempDir(), "PRD.md")
	writeSpecFile(t, elsewhere, "x\n")
	link := filepath.Join(home, "link.md")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{elsewhere, link} {
		if got := SpecSource(file); got != want {
			t.Errorf("%s: %q, want %q", file, got, want)
		}
	}
}

// writeSpecFile writes text to path, making its folder.
func writeSpecFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
