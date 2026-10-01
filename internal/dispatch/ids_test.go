package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A ready ticket whose ID isn't a plain name is set aside before anything is made from it: no
// worktree, branch or worker, a note saying how to bring it back, and a warning line. The other
// tickets still run.
func TestTicketsWithPathCharactersAreSetAside(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"x-../../y", "x-a/b", "x-../../../../y"} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			around := t.TempDir()
			h.cfg.WTRoot = filepath.Join(around, "a", "wt")
			h.beads.add(bad, "first", 1)
			h.beads.add("B", "second", 2)
			h.worker("B", finishes("b.txt"))

			o, code := h.run()
			if code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if args := h.herdr.argsFor(bad); len(args) != 0 {
				t.Errorf("a worker was started for %s: %q", bad, args)
			}
			if st := h.statusOf(bad); st != "deferred" {
				t.Errorf("%s is %s, want deferred", bad, st)
			}
			why := "IDs with path characters can't be run"
			if notes := h.beads.notesOf(bad); !strings.Contains(notes, why) || !strings.Contains(notes, "bd rename "+bad) {
				t.Errorf("%s's notes don't say %q and how to bring it back:\n%s", bad, why, notes)
			}
			if out := h.sink.text(); !strings.Contains(out, "BAD_TICKET_ID: "+why+": "+bad) {
				t.Errorf("no BAD_TICKET_ID line for %s:\n%s", bad, out)
			}
			if branches := h.git(h.repo, "branch", "--list", "wt/x*"); strings.TrimSpace(branches) != "" {
				t.Errorf("a branch was made for %s: %s", bad, branches)
			}
			// Nothing was made beside the worktree root but B's worktree, removed once B merged.
			err := filepath.WalkDir(around, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				switch rel, _ := filepath.Rel(around, p); filepath.ToSlash(rel) {
				case ".", "a", "a/wt":
				case "a/wt/B":
					return filepath.SkipDir
				default:
					t.Errorf("%s was made", p)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if st := h.statusOf("B"); st != "closed" {
				t.Errorf("B is %s, want closed: the run goes on", st)
			}
			if !strings.Contains(h.mainLog(), "B") {
				t.Errorf("B was not merged:\n%s", h.mainLog())
			}
		})
	}
}

// A ticket set aside for its ID stays out of the run when bd can't defer it.
func TestBadTicketIDKeptOutWhenDeferFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("x-a/b", "first", 1)
	o := h.loop()
	o.notes = deferFails{h.beads}
	code := o.Run(t.Context())
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	out := h.sink.text()
	if !strings.Contains(out, "DEFER_FAILED: IDs with path characters can't be run: x-a/b") {
		t.Errorf("no DEFER_FAILED line:\n%s", out)
	}
	if n := strings.Count(out, "DEFER_FAILED"); n != 1 {
		t.Errorf("warned %d times, want once:\n%s", n, out)
	}
	if args := h.herdr.argsFor("x-a/b"); len(args) != 0 {
		t.Errorf("a worker was started: %q", args)
	}
}

// deferFails is a tracker whose bd defer fails.
type deferFails struct{ *fakeBeads }

func (deferFails) Defer(context.Context, string, string) error {
	return errors.New("database is locked")
}

func TestIDProblem(t *testing.T) {
	t.Parallel()
	const path, branch = "IDs with path characters can't be run", "IDs that can't name a git branch can't be run"
	for id, want := range map[string]string{
		"orchestra-k4i":   "",
		"orchestra-k4i.1": "",
		"B":               "",
		"x-a/b":           path,
		`x-a\b`:           path,
		"x-../../y":       path,
		"x-..":            path,
		"../y":            path,
		"/abs":            path,
		"":                path,
		".":               path,
		"x-a b":           branch,
		"x-a~1":           branch,
		"x-a:b":           branch,
		"x-a.lock":        branch,
		"x-a.":            branch,
		".x":              branch,
		"x-@{1}":          branch,
		"x-a*":            branch,
		"x-\x7f":          branch,
		"x-\n":            branch,
	} {
		if got := IDProblem(id); got != want {
			t.Errorf("IDProblem(%q) = %q, want %q", id, got, want)
		}
	}
}
