package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyUnionOnlyAddsTheLineWhenChosen(t *testing.T) {
	repo, git := gitRepo(t)
	if OffersUnion(context.Background(), repo) {
		t.Error("offered without a CHANGELOG.md")
	}
	if _, ok, err := ApplyUnion(context.Background(), repo, Choice{Union: true}); ok || err != nil {
		t.Errorf("no CHANGELOG.md: ok = %v, err = %v", ok, err)
	}

	os.WriteFile(filepath.Join(repo, changelogName), []byte("# Changelog\n"), 0o644)
	attrs := filepath.Join(repo, attributesName)
	os.WriteFile(attrs, []byte("*.png binary"), 0o644) // no final newline
	if !OffersUnion(context.Background(), repo) {
		t.Fatal("not offered for a CHANGELOG.md without merge=union")
	}
	for _, c := range []Choice{{UnionUnasked: true}, {}} {
		s, ok, err := ApplyUnion(context.Background(), repo, c)
		if !ok || err != nil || read(t, attrs) != "*.png binary" {
			t.Errorf("%+v: ok = %v, err = %v, .gitattributes = %q", c, ok, err, read(t, attrs))
		}
		if c.UnionUnasked && (s.Kind != StepCaution || !strings.Contains(s.Detail, "--changelog-union")) {
			t.Errorf("unasked: %+v", s)
		}
	}

	s, ok, err := ApplyUnion(context.Background(), repo, Choice{Union: true})
	if !ok || err != nil || s.Kind != StepDone {
		t.Fatalf("chosen: %+v, ok = %v, err = %v", s, ok, err)
	}
	if got := read(t, attrs); got != "*.png binary\nCHANGELOG.md merge=union\n" {
		t.Errorf(".gitattributes = %q", got)
	}
	if OffersUnion(context.Background(), repo) {
		t.Error("offered again once .gitattributes has the line")
	}
	if s, _, _ := ApplyUnion(context.Background(), repo, Choice{Union: true}); s.Kind != StepKept || strings.Count(read(t, attrs), "merge=union") != 1 {
		t.Errorf("second run: %+v, .gitattributes = %q", s, read(t, attrs))
	}
	if next := strings.Join(NextSteps(context.Background(), repo, nil, nil), "\n"); !strings.Contains(next, "Commit .gitattributes.") {
		t.Errorf("the new line is to be committed: %q", next)
	}
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "attributes")

	// However .gitattributes says it, a union merge is not offered again.
	os.WriteFile(attrs, []byte("*.md merge=union\n"), 0o644)
	if OffersUnion(context.Background(), repo) {
		t.Error("offered although *.md merges by union")
	}
}
