package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// Spec, at the question of what to work on, runs a spec on this machine as --spec runs it, refined
// with claude first when asked, as --refine has it.

// On the screen once the spec's file, or the choice to refine it, has the focus.
const (
	onSpecFile = "┃ Spec file"
	onRefine   = "┃ Refine it with claude first?"
)

// pickSpec picks Spec, the question's second option, from the first.
const pickSpec = keyDown + keyEnter

func TestWorkQuestionTakesASpec(t *testing.T) {
	repo := t.TempDir()
	spec := writeSpec(t, filepath.Join(repo, "docs", "app.md"), "# An app\n")
	for _, tc := range []struct {
		name, keys string
		refine     bool
		said       string
	}{
		{"as it is", keyEnter, false, "Spec: " + spec + ", run as it is\n"},
		{"refined", keyDown + keyEnter, true, "Spec: " + spec + ", refined with claude first\n"},
	} {
		term, answer := askWorkIn(context.Background(), t, repo, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, pickSpec, false)
		term.waitFor(t, onSpecFile)
		term.typeKeys(t, "docs/app.md"+keyEnter, false)
		term.waitFor(t, onRefine)
		term.typeKeys(t, tc.keys, true)
		a := answer()
		if want := (tui.Work{Kind: tui.WorkSpec, Spec: spec, Refine: tc.refine}); a.work != want ||
			a.code != dispatch.ExitOK || a.err != "" {
			t.Errorf("%s: got %+v, exit %d, stderr %q; want %+v", tc.name, a.work, a.code, a.err, want)
		}
		// Said once the form is gone, as a feature's description is.
		if !strings.HasSuffix(a.out, tc.said) {
			t.Errorf("%s: the spec isn't said after the form:\n%s", tc.name, a.out)
		}
	}
}

// Esc or Ctrl+C at the spec's file or at the choice to refine it ends the run before anything
// changes.
func TestWorkQuestionCancelledAtASpecExits130(t *testing.T) {
	const stopped = "orchestra: stopped before the run started; nothing was changed.\n"
	repo := t.TempDir()
	writeSpec(t, filepath.Join(repo, "app.md"), "# An app\n")
	for _, cancelKey := range []string{keyEsc, keyCtrlC} {
		term, answer := askWorkIn(context.Background(), t, repo, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, pickSpec, false)
		term.waitFor(t, onSpecFile)
		term.typeKeys(t, "app.m", false)
		term.waitFor(t, "app.m")
		term.typeKeys(t, cancelKey, true)
		if a := answer(); a.work != (tui.Work{}) || a.code != dispatch.ExitInterrupted || a.err != stopped {
			t.Errorf("%q at the file: got %+v, exit %d, stderr %q", cancelKey, a.work, a.code, a.err)
		}

		term, answer = askWorkIn(context.Background(), t, repo, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, pickSpec, false)
		term.waitFor(t, onSpecFile)
		term.typeKeys(t, "app.md"+keyEnter, false)
		term.waitFor(t, onRefine)
		term.typeKeys(t, cancelKey, true)
		if a := answer(); a.work != (tui.Work{}) || a.code != dispatch.ExitInterrupted || a.err != stopped {
			t.Errorf("%q at the choice to refine: got %+v, exit %d, stderr %q", cancelKey, a.work, a.code, a.err)
		}
	}
}

// chosenSession is a session of the run that loadConfig makes of args in repo, holding its lock, as
// one is when it asks what to work on.
func chosenSession(t *testing.T, repo string, args ...string) (*session, *strings.Builder) {
	t.Helper()
	c, problems := loadWith(t, args...)
	if len(problems) > 0 {
		t.Fatalf("setup problems: %q", problems)
	}
	holder := project.Holder{PID: 1, Branch: "main"}
	lock, err := project.LockRun(t.Context(), repo, holder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	var errOut strings.Builder
	return &session{cfg: c, e: cmdEnv{stderr: &errOut}, holder: holder, lock: lock}, &errOut
}

// A spec chosen at the question runs as one given with --spec does: no ticket limit and workers in
// auto permission mode unless the flags say otherwise, and kept in the repository where --spec's
// would be, as the run lock says from then on.
func TestSpecChosenRunsAsSpecGivenDoes(t *testing.T) {
	repo := specFixture(t)
	t.Setenv("WORKER_PERMISSION_MODE", "")
	spec := writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n")
	const kept = ".orchestra/specs/app.md"
	for _, tc := range []struct {
		name   string
		args   []string
		refine bool
		limit  int
		mode   string
	}{
		{"as it is", nil, false, dispatch.NoLimit, project.PermissionAuto},
		{"refined, with a limit and a permission mode given",
			[]string{"--limit", "5", "--worker-permission-mode", project.PermissionAcceptEdits},
			true, 5, project.PermissionAcceptEdits},
	} {
		s, errOut := chosenSession(t, repo, tc.args...)
		code := s.specChosen(t.Context(), tui.Work{Kind: tui.WorkSpec, Spec: spec, Refine: tc.refine})
		c := s.cfg
		if code != dispatch.ExitOK || errOut.Len() > 0 {
			t.Fatalf("%s: exit %d, stderr:\n%s", tc.name, code, errOut)
		}
		if c.specFile != spec || c.Refine != tc.refine || c.Spec != kept || c.Limit != tc.limit ||
			c.WorkerPermissionMode != tc.mode {
			t.Errorf("%s: spec %q, refine %v, kept at %q, limit %d, permission mode %q; want %q, %v, %q, %d, %q",
				tc.name, c.specFile, c.Refine, c.Spec, c.Limit, c.WorkerPermissionMode,
				spec, tc.refine, kept, tc.limit, tc.mode)
		}
		if h, held, err := project.RunHolder(t.Context(), repo); err != nil || !held || h.Spec != kept || h.PID != 1 {
			t.Errorf("%s: the run lock: %+v, held %v, %v", tc.name, h, held, err)
		}
		_ = s.lock.Close() // for the next case's
	}
}

// A spec chosen where git wouldn't commit it ends the run, exit 2, as one given with --spec would
// have stopped it from starting; the run lock doesn't name it.
func TestSpecChosenWhereGitIgnoresIt(t *testing.T) {
	repo := specFixture(t)
	writeSpec(t, filepath.Join(repo, ".gitignore"), "private/\n")
	ignored := writeSpec(t, filepath.Join(repo, "private", "app.md"), "# Mine\n")
	s, errOut := chosenSession(t, repo)
	if code := s.specChosen(t.Context(), tui.Work{Kind: tui.WorkSpec, Spec: ignored}); code != dispatch.ExitSetup {
		t.Errorf("exit %d, want %d", code, dispatch.ExitSetup)
	}
	if want := "orchestra cannot run that spec:\n  - git ignores private/app.md, where the spec (chosen at the start) " +
		"is kept, and workers only see committed files"; !strings.HasPrefix(errOut.String(), want) {
		t.Errorf("stderr:\n%s\nwant it to start with:\n%s", errOut, want)
	}
	if h, _, err := project.RunHolder(t.Context(), repo); err != nil || h.Spec != "" {
		t.Errorf("the run lock: %+v, %v", h, err)
	}
}
