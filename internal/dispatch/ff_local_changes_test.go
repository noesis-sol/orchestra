package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/git"
)

// The check before a merge leaves out .claude/, .beads/ and .orchestra/, though tickets change the
// files tracked there: uncommitted changes to one a branch changes keep it from fast-forwarding, and
// the run stops with DIRTY_TREE naming them, not with a MERGE_FAILED that says the branch doesn't
// fast-forward.

// writeFile writes content to file under dir, making its folders.
func writeFile(t *testing.T, dir, file, content string) {
	t.Helper()
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeStopsOnLocalChangesUnderExcludedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		tracked    bool // committed on main before the ticket; else the ticket adds it, untracked in the checkout
	}{
		{"tracked under .orchestra", ".orchestra/settings.json", true},
		{"tracked under .claude", ".claude/settings.json", true},
		{"untracked under .beads", ".beads/config.yaml", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMergeFixture(t, "true")
			if tc.tracked {
				writeFile(t, f.repo, tc.file, "{}\n")
				f.git(f.repo, "add", ".")
				f.git(f.repo, "commit", "-q", "-m", "track "+tc.file)
			}
			wt := filepath.Join(t.TempDir(), "k-1")
			f.git(f.repo, "worktree", "add", "-q", "-b", "wt/k-1", wt, "main")
			writeFile(t, wt, tc.file, "{\"ticket\": true}\n")
			f.git(wt, "add", ".")
			f.git(wt, "commit", "-q", "-m", "k-1: change "+tc.file)
			writeFile(t, f.repo, tc.file, "{\"by hand\": true}\n")
			before := f.git(f.repo, "rev-parse", "main")
			s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"})
			if s == nil || s.code != ExitDirty || s.kind != stopDirtyTree {
				t.Fatalf("stop = %v, want DIRTY_TREE", s)
			}
			if line := s.Error(); !strings.Contains(line, "uncommitted changes in "+f.repo+" to "+tc.file) ||
				strings.Contains(line, "fast-forward") {
				t.Errorf("line = %q, want it to name %s", line, tc.file)
			}
			if s.blocked != "main checkout has uncommitted changes" {
				t.Errorf("blocked = %q", s.blocked)
			}
			if ce := new(command.Error); !errors.As(s, &ce) || !strings.Contains(ce.Stderr, tc.file) {
				t.Errorf("git's error is not kept: cause = %v", s.cause)
			}
			if why := f.orch.unmerged["k-1"]; why != "DIRTY_TREE" {
				t.Errorf("k-1 left unmerged for %q, want DIRTY_TREE", why)
			}
			if f.git(f.repo, "rev-parse", "main") != before {
				t.Error("main must not move")
			}
			if _, err := os.Stat(wt); err != nil {
				t.Error("the worktree must be kept for review")
			}
		})
	}
}

// refusingMerger is git whose fast-forwards fail, for no reason in the checkout.
type refusingMerger struct{ git.Git }

var errNoFastForward = errors.New("git merge --ff-only --quiet wt/k-1: exit status 128: fatal: something else")

func (refusingMerger) FastForward(ctx context.Context, repo, branch string) (string, error) {
	return "", errNoFastForward
}

// A fast-forward refused for another reason, local changes to files the branch doesn't change
// included, still stops with MERGE_FAILED, which now says what git said.
func TestMergeFailedSaysWhatGitSaid(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.merger = refusingMerger{}
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	writeFile(t, f.repo, ".orchestra/settings.json", "{\"by hand\": true}\n") // a.txt is not in the way
	s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"})
	if s == nil || s.code != ExitMerge || s.kind != stopMergeFailed {
		t.Fatalf("stop = %v, want MERGE_FAILED", s)
	}
	if !errors.Is(s, errNoFastForward) || !strings.Contains(s.Error(), "fatal: something else") {
		t.Errorf("stop = %q, want it to keep and say git's error", s)
	}
	if why := f.orch.unmerged["k-1"]; why != "MERGE_FAILED" {
		t.Errorf("k-1 left unmerged for %q, want MERGE_FAILED", why)
	}
}
