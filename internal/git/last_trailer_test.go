package git

import (
	"context"
	"testing"
)

// LastTrailer reads a trailer from the latest commit of a path that has it, past later commits of the
// path without it and those of other paths; before the first commit, and for a path no commit with
// it changed, it is "".
func TestLastTrailer(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	if got := answered[string](t)(g.LastTrailer(ctx, repo, "spec.md", "Spec-Source")); got != "" {
		t.Errorf("before the first commit: %q", got)
	}
	write(t, repo, "spec.md", "one\n")
	git("add", "spec.md")
	git("commit", "-q", "-m", "add spec\n\nsha256 1\n\nSpec-Source: ~/a/spec.md\n")
	write(t, repo, "spec.md", "two\n")
	git("commit", "-q", "-am", "update spec\n\nSpec-Source: ~/b/my spec.md\n")
	write(t, repo, "spec.md", "three\n")
	git("commit", "-q", "-am", "edit spec by hand")
	write(t, repo, "other.md", "x\n")
	git("add", "other.md")
	git("commit", "-q", "-m", "other\n\nSpec-Source: ~/c/other.md\n")
	for path, want := range map[string]string{"spec.md": "~/b/my spec.md", "other.md": "~/c/other.md", "none.md": ""} {
		if got := answered[string](t)(g.LastTrailer(ctx, repo, path, "Spec-Source")); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}
