//go:build unix

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A spec's text as an earlier run broke it down, and as it is now, changed.
const (
	carryText    = "# An app\n\nIt lists things.\n"
	carryChanged = "# An app\n\nIt lists things, sorted.\n"
)

// carriedSpec is where the carry-on tests keep their spec, inside the repository.
const carriedSpec = "docs/app.md"

// sha256Of is text's SHA-256, in hex, as a committed spec has it.
func sha256Of(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// carryRepo is a git repository with the spec docs/app.md committed in it with text, as a spec run
// commits it. It returns the repository, the spec's file and the committed spec.
func carryRepo(t *testing.T, text string) (repo, file string, spec project.Spec) {
	t.Helper()
	repo, _ = gitRepo(t)
	file = writeSpec(t, filepath.Join(repo, filepath.FromSlash(carriedSpec)), text)
	spec, _, err := project.CommitSpec(context.Background(), repo, file)
	if err != nil {
		t.Fatal(err)
	}
	return repo, file, spec
}

// rootFiledFor is the root epic s-1 of the spec docs/app.md, An app, in status, its tree filed for
// spec as its metadata says: spec's SHA-256 and commit, each left out where "".
func rootFiledFor(spec project.Spec, status dispatch.TicketStatus) dispatch.Ticket {
	meta := map[string]string{dispatch.SpecKey: carriedSpec}
	if spec.SHA256 != "" {
		meta[project.SpecSHA256Key] = spec.SHA256
	}
	if spec.Commit != "" {
		meta[project.SpecCommitKey] = spec.Commit
	}
	b, err := json.Marshal(meta)
	if err != nil {
		panic(err)
	}
	t := issue("s-1", "epic", "", string(b), "")
	t.Title, t.Status = "An app", status
	return t
}

// carriedIssue is an issue under the spec's root epic: its ID, type, parent, title and status.
func carriedIssue(id, kind, parent, title string, status dispatch.TicketStatus) dispatch.Ticket {
	t := issue(id, kind, parent, `{"spec":"`+carriedSpec+`","files":["`+id+`.go"]}`, "Does "+id+".")
	t.Title, t.Status = title, status
	return t
}

// treeBreakdown is a breakdown into tree: asked, it keeps the request and what the update's file held
// then, files files into tree, and answers with root.
type treeBreakdown struct {
	repo  string
	tree  *specTree
	files []dispatch.Ticket
	root  string

	asked  bool
	got    breakdownRequest
	update string // the update's file, as the breakdown found it; "" when it had none
}

func (f *treeBreakdown) BreakDown(_ context.Context, r breakdownRequest) (breakdownResult, error) {
	f.asked, f.got = true, r
	if r.Update != nil {
		b, err := os.ReadFile(filepath.Join(f.repo, filepath.FromSlash(r.Update.File)))
		if err != nil {
			return breakdownResult{}, err
		}
		f.update = string(b)
	}
	f.tree.issues = append(f.tree.issues, f.files...)
	return breakdownResult{Root: f.root}, nil
}

// carryOutcome is how the step of a spec run before its loop ended: the root epic the run is scoped
// to, the exit code, and what it said on its output and its error output.
type carryOutcome struct {
	root     string
	code     int
	out, err string
}

// carry runs the step of a spec run before its loop for the spec file in repo, with bd as tree has it
// and the breakdown b.
func carry(t *testing.T, repo, file string, tree specTracker, b specBreakdown) carryOutcome {
	t.Helper()
	var out, errOut strings.Builder
	root, code := specRun{file: file, repo: repo, breakdown: b, tracker: tree, out: &out, err: &errOut}.run(testCtx(t.Context(), t))
	return carryOutcome{root, code, out.String(), errOut.String()}
}

// committedAlready is what a spec run says first of the spec docs/app.md, committed already.
func committedAlready(spec project.Spec) string {
	return "the spec " + carriedSpec + " is committed already (sha256 " + spec.ShortSHA256() + ")\n"
}

// The spec unchanged since its root epic's tree was filed, and the root epic open: there is no
// breakdown, and the run is scoped to the root epic, carrying on with it as it is.
func TestSpecUnchangedCarriesOnWithItsRootEpic(t *testing.T) {
	t.Parallel()
	repo, file, spec := carryRepo(t, carryText)
	tree := &specTree{issues: []dispatch.Ticket{rootFiledFor(spec, dispatch.StatusOpen),
		carriedIssue("s-1.1", "task", "s-1", "List the things", dispatch.StatusClosed),
		carriedIssue("s-1.2", "task", "s-1", "Show the list", dispatch.StatusInProgress)}}
	b := &treeBreakdown{repo: repo, tree: tree, root: "s-1"}
	o := carry(t, repo, file, tree, b)
	if o.root != "s-1" || o.code != dispatch.ExitOK || b.asked {
		t.Fatalf("got %q, exit %d, broken down %v; stderr:\n%s", o.root, o.code, b.asked, o.err)
	}
	if want := committedAlready(spec) + "the spec " + carriedSpec + " is unchanged; carrying on with s-1\n"; o.out != want ||
		o.err != "" {
		t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", o.out, want, o.err)
	}
	if len(tree.set) > 0 {
		t.Errorf("the root epic's metadata was set: %q", tree.set)
	}
}

// The spec unchanged and its root epic closed, everything under it merged: there is no breakdown, and
// the run, scoped to the root epic, has nothing to run, which the nothing-to-run box says (see
// TestSpecRunSaysABuiltSpecIsBuilt).
func TestSpecUnchangedAndClosedIsNotBrokenDown(t *testing.T) {
	t.Parallel()
	repo, file, spec := carryRepo(t, carryText)
	tree := &specTree{issues: []dispatch.Ticket{rootFiledFor(spec, dispatch.StatusClosed),
		carriedIssue("s-1.1", "task", "s-1", "List the things", dispatch.StatusClosed)}}
	b := &treeBreakdown{repo: repo, tree: tree, root: "s-1"}
	o := carry(t, repo, file, tree, b)
	if o.root != "s-1" || o.code != dispatch.ExitOK || b.asked || o.out != committedAlready(spec) || o.err != "" {
		t.Errorf("got %q, exit %d, broken down %v; stdout:\n%s\nstderr:\n%s", o.root, o.code, b.asked, o.out, o.err)
	}
}

// The nothing-to-run box of a spec run whose root epic is all done says the spec is built, on a
// terminal and in plain output; a run of another scope still says all done.
func TestNothingToRunSaysTheSpecIsBuilt(t *testing.T) {
	t.Parallel()
	n := dispatch.NothingToRun{Scope: "s-1", AllDone: true, Built: carriedSpec}
	var box, plain strings.Builder
	tui.Printer{Out: &box, Styled: true, Width: 80}.Nothing(n)
	if got := ansi.Strip(box.String()); !strings.Contains(got, "│ ✓ "+carriedSpec+" is built (s-1) │") {
		t.Errorf("the box:\n%s", got)
	}
	tui.Printer{Out: &plain}.Nothing(n)
	if !strings.HasSuffix(plain.String(), " ✓ "+carriedSpec+" is built (s-1)\n") {
		t.Errorf("plain:\n%s", plain.String())
	}
	n.Built = ""
	box.Reset()
	tui.Printer{Out: &box, Styled: true, Width: 80}.Nothing(n)
	if got := ansi.Strip(box.String()); !strings.Contains(got, "│ ✓ All done │") {
		t.Errorf("the box without a spec:\n%s", got)
	}
}

// The spec changed since its root epic's tree was filed: the new version is committed, and the
// breakdown runs in update mode, given the root epic, the version its tree was filed for, the diff
// from that version to the new one and the tree, open and closed, which it also finds written in
// .orchestra/run/spec-update.md. What it files goes under the same root epic, whose SHA-256 and
// commit are then the new version's.
func TestSpecChangedIsUpdatedUnderItsRootEpic(t *testing.T) {
	t.Parallel()
	repo, file, was := carryRepo(t, carryText)
	tree := &specTree{issues: []dispatch.Ticket{rootFiledFor(was, dispatch.StatusOpen),
		carriedIssue("s-1.1", "epic", "s-1", "Listing", dispatch.StatusClosed),
		carriedIssue("s-1.1.1", "task", "s-1.1", "List the things", dispatch.StatusClosed),
		carriedIssue("s-1.2", "task", "s-1", "Show the list", dispatch.StatusOpen)}}
	writeSpec(t, file, carryChanged)
	b := &treeBreakdown{repo: repo, tree: tree, root: "s-1",
		files: []dispatch.Ticket{carriedIssue("s-1.3", "task", "s-1", "Sort the things", dispatch.StatusOpen)}}
	o := carry(t, repo, file, tree, b)
	if o.root != "s-1" || o.code != dispatch.ExitOK || !b.asked {
		t.Fatalf("got %q, exit %d, broken down %v; stderr:\n%s", o.root, o.code, b.asked, o.err)
	}
	head := gitIn(t, repo, "rev-parse", "HEAD")
	if subject := gitIn(t, repo, "log", "-1", "--format=%s"); subject != "orchestra: update spec app.md" {
		t.Errorf("the new version wasn't committed: HEAD %q", subject)
	}
	now := project.Spec{Path: carriedSpec, SHA256: sha256Of(carryChanged), Commit: head}
	u := b.got.Update
	if b.got.Spec != now || u == nil {
		t.Fatalf("the breakdown was asked %+v, want the spec %+v in update mode", b.got, now)
	}
	if u.Root != "s-1" || u.Was != was || u.File != ".orchestra/run/spec-update.md" || u.NoDiff != "" ||
		u.Before != (filedCount{epics: 2, tickets: 2, ok: true}) {
		t.Errorf("the update: %+v", u)
	}
	if !strings.Contains(u.Diff, "--- a/"+carriedSpec+"\n+++ b/"+carriedSpec+"\n") ||
		!strings.Contains(u.Diff, "\n-It lists things.\n+It lists things, sorted.\n") {
		t.Errorf("the diff:\n%s", u.Diff)
	}
	if want := "- s-1 (open epic): An app\n  - s-1.1 (closed epic): Listing\n    - s-1.1.1 (closed task): List the things\n" +
		"  - s-1.2 (open task): Show the list\n"; u.Tree != want {
		t.Errorf("the tree:\n%s\nwant:\n%s", u.Tree, want)
	}
	for _, want := range []string{
		"s-1, the root epic of the spec at " + carriedSpec + ", and the tree under it were filed for the spec as it " +
			"was at commit " + was.Commit + ", sha256 " + was.SHA256 + ". The spec is now at commit " + head +
			", sha256 " + now.SHA256 + ".\n",
		"The change, as `git diff " + was.Commit[:12] + " " + head[:12] + " -- " + carriedSpec + "` gives it:\n\n```diff\n" +
			u.Diff + "```\n",
		"# The tree under s-1\n\n", "`bd show <ID>` gives the rest.\n\n" + u.Tree,
	} {
		if !strings.Contains(b.update, want) {
			t.Errorf("the update's file lacks %q:\n%s", want, b.update)
		}
	}
	if want := []string{"s-1 spec_sha256=" + now.SHA256, "s-1 spec_commit=" + head}; !slices.Equal(tree.set, want) {
		t.Errorf("the root epic's metadata set: %q, want %q", tree.set, want)
	}
	for _, want := range []string{"committed the spec " + carriedSpec + " (sha256 " + now.ShortSHA256() + ")\n",
		"the spec " + carriedSpec + " changed since s-1 was filed: updating its tickets\n",
		"updated s-1 for the spec's change (2 epics, 3 tickets)\n"} {
		if !strings.Contains(o.out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, o.out)
		}
	}
	if want := "warning: the breakdown left s-1's spec_sha256, spec_commit metadata out or wrong; orchestra set it\n"; o.err != want {
		t.Errorf("stderr:\n%s\nwant:\n%s", o.err, want)
	}
}

// Where git can't give the change, the root epic naming no commit or one git doesn't have, the update
// says why, and is to compare the spec as it is now with the tree.
func TestSpecUpdateWithoutADiff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, commit, why string
	}{
		{"no spec_commit", "", "s-1 names no spec_commit"},
		{"a commit git doesn't have", strings.Repeat("d", 40), "fatal: bad object " + strings.Repeat("d", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, file, was := carryRepo(t, carryText)
			was.Commit = tc.commit
			tree := &specTree{issues: []dispatch.Ticket{rootFiledFor(was, dispatch.StatusOpen)}}
			writeSpec(t, file, carryChanged)
			b := &treeBreakdown{repo: repo, tree: tree, root: "s-1"}
			if o := carry(t, repo, file, tree, b); o.code != dispatch.ExitOK || b.got.Update == nil {
				t.Fatalf("exit %d, update %+v; stderr:\n%s", o.code, b.got.Update, o.err)
			}
			if u := b.got.Update; u.Diff != "" || !strings.HasPrefix(u.NoDiff, tc.why) {
				t.Errorf("diff %q, why none %q, want it to start with %q", u.Diff, u.NoDiff, tc.why)
			}
			if want := "git can't give the change (" + b.got.Update.NoDiff + "): compare the spec as it is now with " +
				"the tree.\n"; !strings.Contains(b.update, want) {
				t.Errorf("the update's file lacks %q:\n%s", want, b.update)
			}
		})
	}
}

// An update that names another root epic than the one whose tree it updated is not run: the run ends,
// exit 2, listing the spec's tree.
func TestSpecUpdateNamingAnotherRootEpic(t *testing.T) {
	t.Parallel()
	repo, file, was := carryRepo(t, carryText)
	tree := &specTree{issues: []dispatch.Ticket{rootFiledFor(was, dispatch.StatusOpen),
		carriedIssue("s-1.1", "task", "s-1", "List the things", dispatch.StatusClosed)}}
	writeSpec(t, file, carryChanged)
	o := carry(t, repo, file, tree, &treeBreakdown{repo: repo, tree: tree, root: "s-9"})
	if o.root != "" || o.code != dispatch.ExitSetup {
		t.Fatalf("got %q, exit %d", o.root, o.code)
	}
	for _, want := range []string{"orchestra won't run what the spec's breakdown filed: it named s-9 as the root " +
		"epic, not s-1, whose tree it was to update\n",
		"The spec's tree has 1 epic and 1 ticket for " + carriedSpec + ", under s-1:\n  s-1  epic  An app\n" +
			"  s-1.1  task  List the things\n",
		"Run it again to carry on: orchestra --spec " + file + "\n"} {
		if !strings.Contains(o.err, want) {
			t.Errorf("stderr lacks %q:\n%s", want, o.err)
		}
	}
	if len(tree.set) > 0 {
		t.Errorf("the metadata was set on a root it refused: %q", tree.set)
	}
}

// Two root epics for one spec, one filed by hand, say, are a setup problem naming both, and nothing
// is broken down; the problem suggests keeping the one a breakdown marked as filed.
func TestSpecWithTwoRootEpics(t *testing.T) {
	t.Parallel()
	byHand := func(id string) dispatch.Ticket {
		return issue(id, "epic", "", `{"spec":"`+carriedSpec+`"}`, "")
	}
	for _, tc := range []struct {
		name  string
		hand  []string
		want  string
		count string
	}{
		{"two", []string{"s-7"}, "The spec docs/app.md has two root epics, s-7 and s-1: epics carrying its metadata " +
			"(spec) whose parent doesn't. A spec run carries on with one, so keep one: put the other under it " +
			"(bd update s-7 --parent s-1), or take the spec's metadata off it (bd update s-7 --unset-metadata spec).",
			"two root epics, s-7 and s-1"},
		{"three", []string{"s-7", "s-8"}, "The spec docs/app.md has 3 root epics, s-7, s-8 and s-1: epics carrying its " +
			"metadata (spec) whose parent doesn't. A spec run carries on with one, so keep one: put the others under it " +
			"(bd update s-7 s-8 --parent s-1), or take the spec's metadata off them " +
			"(bd update s-7 s-8 --unset-metadata spec).", "3 root epics"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, file, spec := carryRepo(t, carryText)
			tree := &specTree{}
			for _, id := range tc.hand {
				tree.issues = append(tree.issues, byHand(id))
			}
			tree.issues = append(tree.issues, rootFiledFor(spec, dispatch.StatusOpen),
				carriedIssue("s-1.1", "task", "s-1", "List the things", dispatch.StatusOpen))
			b := &treeBreakdown{repo: repo, tree: tree, root: "s-1"}
			o := carry(t, repo, file, tree, b)
			if o.root != "" || o.code != dispatch.ExitSetup || b.asked {
				t.Fatalf("got %q, exit %d, broken down %v", o.root, o.code, b.asked)
			}
			if want := "orchestra cannot start:\n  - " + tc.want + "\n"; o.err != want {
				t.Errorf("stderr:\n%s\nwant:\n%s", o.err, want)
			}
		})
	}
}

// With no root epic for the spec, the spec is broken down as a new one; and so it is, carrying on,
// when its root epic has no SHA-256: the breakdown that filed it didn't finish, as one sets it last.
func TestSpecWithoutAFinishedRootEpicIsBrokenDown(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		filed  []dispatch.Ticket
		said   string
		counts string
	}{
		{"no root epic", nil, "", "(1 epic, 1 ticket)"},
		{"tickets but no epic", []dispatch.Ticket{carriedIssue("s-2", "task", "", "Stray", dispatch.StatusOpen)}, "",
			"(1 epic, 1 ticket)"},
		{"the breakdown didn't finish", []dispatch.Ticket{rootFiledFor(project.Spec{}, dispatch.StatusOpen)},
			"the breakdown that filed s-1 didn't finish: carrying it on\n", "(1 epic, 1 ticket)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, file, spec := carryRepo(t, carryText)
			tree := &specTree{issues: tc.filed}
			files := []dispatch.Ticket{carriedIssue("s-1.1", "task", "s-1", "List the things", dispatch.StatusOpen)}
			if !slices.ContainsFunc(tc.filed, func(t dispatch.Ticket) bool { return t.ID == "s-1" }) {
				files = append([]dispatch.Ticket{rootFiledFor(project.Spec{}, dispatch.StatusOpen)}, files...)
			}
			b := &treeBreakdown{repo: repo, tree: tree, root: "s-1", files: files}
			o := carry(t, repo, file, tree, b)
			if o.root != "s-1" || o.code != dispatch.ExitOK || !b.asked || b.got.Update != nil || b.got.Spec != spec {
				t.Fatalf("got %q, exit %d, asked %+v (%v); stderr:\n%s", o.root, o.code, b.got, b.asked, o.err)
			}
			if want := committedAlready(spec) + tc.said + "broke the spec down into s-1 " + tc.counts + "\n"; o.out != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", o.out, want)
			}
		})
	}
}

// failingLister is a spec tree whose bd can't list the issues carrying a spec's metadata.
type failingLister struct{ *specTree }

func (failingLister) WithMetadata(context.Context, string, string) ([]dispatch.Ticket, error) {
	return nil, errors.New("bd list: database is locked")
}

// When bd can't say whether the spec has a root epic, nothing is broken down: the run ends, exit 4,
// saying so; Ctrl+C before bd answers is a stop, exit 130.
func TestSpecRootEpicUnread(t *testing.T) {
	t.Parallel()
	repo, file, _ := carryRepo(t, carryText)
	tree := &specTree{}
	b := &treeBreakdown{repo: repo, tree: tree, root: "s-1"}
	o := carry(t, repo, file, failingLister{tree}, b)
	if o.code != dispatch.ExitTool || b.asked || o.err != "orchestra can't look for the spec's root epic, which an "+
		"earlier run filed: bd list: database is locked\n" {
		t.Errorf("exit %d, broken down %v; stderr:\n%s", o.code, b.asked, o.err)
	}
	ctx, cancel := context.WithCancel(testCtx(t.Context(), t))
	cancel()
	var out, errOut strings.Builder
	if _, code := (specRun{file: file, repo: repo, breakdown: b, tracker: failingLister{tree}, out: &out,
		err: &errOut}).carryOn(ctx, project.Spec{Path: carriedSpec}); code != dispatch.ExitInterrupted {
		t.Errorf("stopped: exit %d; stderr:\n%s", code, errOut.String())
	}
}

// A breakdown in update mode is told so in its first message: the headless one, on lines, naming the
// root epic, the version its tree was filed for and where the change and the tree are; the refinement,
// in one line.
func TestSpecUpdateFirstMessages(t *testing.T) {
	t.Parallel()
	u := &specUpdate{Root: "s-1", Was: project.Spec{Path: testSpec.Path, SHA256: "0ld", Commit: "c1"},
		File: ".orchestra/run/spec-update.md"}
	if got, want := specMessage(breakdownRequest{Spec: testSpec, Update: u}, time.Hour),
		"The spec at .orchestra/specs/app.md has changed since its Beads tickets were filed under s-1: update them "+
			"for the change.\n\n- path: .orchestra/specs/app.md\n- sha256: "+testSpec.SHA256+"\n- commit: c0ffee\n"+
			"- root epic: s-1\n- filed for: sha256 0ld, commit c1\n- the change and the tree: .orchestra/run/spec-update.md\n"+
			"- time limit: 1h, after which orchestra stops this session\n"; got != want {
		t.Errorf("headless:\n%s\nwant:\n%s", got, want)
	}
	if got, want := refineMessage(testSpec, u), "The spec at .orchestra/specs/app.md has changed since its Beads "+
		"tickets were filed under s-1 (for sha256 0ld, commit c1): update them for the change with me (sha256 "+
		testSpec.SHA256+", commit c0ffee). The change and the tree are in .orchestra/run/spec-update.md."; got != want {
		t.Errorf("refinement:\n%s\nwant:\n%s", got, want)
	}
	u.Was.Commit = ""
	if got := specMessage(breakdownRequest{Spec: testSpec, Update: u}, time.Hour); !strings.Contains(got,
		"- filed for: sha256 0ld, commit not recorded\n") {
		t.Errorf("no commit:\n%s", got)
	}
}

// filedLine is an issue as filingBd keeps it in its filed file, once filed.
func filedLine(id, kind, parent, title string, status dispatch.TicketStatus, metadata string) string {
	return fmt.Sprintf(`{"id":"%s","title":"%s","status":"%s","issue_type":"%s","parent":"%s",`+
		`"acceptance_criteria":"Does it.","metadata":%s}`+"\n", id, title, status, kind, parent, metadata)
}

// fileForBd gives filingBd, its folder tools, issues filed already, filedLine's lines.
func fileForBd(t *testing.T, tools string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tools, "filed"), []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// keptSpec is where a spec run keeps a spec from outside the repository called app.md.
const keptSpec = ".orchestra/specs/app.md"

// keptRoot is the root epic s-1 of keptSpec as filingBd has it, in status, filed for the version of
// the spec with SHA-256 sha and commit commit.
func keptRoot(status dispatch.TicketStatus, sha, commit string) string {
	return filedLine("s-1", "epic", "", "An app", status, `{"spec":"`+keptSpec+`","spec_sha256":"`+sha+
		`","spec_commit":"`+commit+`"}`)
}

// keptTicket is a ticket under keptSpec's root epic as filingBd has it.
func keptTicket(id, parent, title string, status dispatch.TicketStatus) string {
	return filedLine(id, "task", parent, title, status, `{"spec":"`+keptSpec+`","files":["`+id+`.go"]}`)
}

// notToRun is a fake claude session's body that fails the run if it is started.
const notToRun = "echo 'the breakdown ran' >&2\nexit 1\n"

// The nightly command again with the spec unchanged and its root epic open: the run carries on with
// the root epic, without a breakdown, its last run's worker carried over; the log says so.
func TestSpecRunCarriesOnWithItsRootEpic(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, tools := breakdownFixture(t, "", notToRun)
	fileForBd(t, tools, keptRoot(dispatch.StatusOpen, sha256Of(read(t, spec)), "c0ffee"),
		keptTicket("s-1.1", "s-1", "List the things", dispatch.StatusClosed),
		keptTicket("s-1.2", "s-1", "Sort the things", dispatch.StatusInProgress))
	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if _, err := os.Stat(filepath.Join(tools, "claude-args")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the spec was broken down again (%v); stderr:\n%s", err, stderr)
	}
	for _, want := range []string{"the spec " + keptSpec + " is unchanged; carrying on with s-1\n",
		"  spec: epic s-1, broken down from: " + keptSpec + "\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if calls := read(t, filepath.Join(tools, "bd-calls")); !strings.Contains(calls,
		"ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|--parent|s-1|\n") ||
		strings.Contains(calls, "create|") || strings.Contains(calls, "update|") {
		t.Errorf("the run wasn't scoped to s-1 as it is:\n%s", calls)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	for _, want := range []string{"SPEC: " + keptSpec + " is unchanged; carrying on with s-1\n",
		"  s-1.2, carried over from the last run, is dropped: its worktree "} { // made up by leaveWorker
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if recs := streamRecords(t, repo); recs[0]["scope"] != "s-1" || recs[0]["spec"] != keptSpec {
		t.Errorf("start record: %v", recs[0])
	}
}

// The nightly command again once everything is merged, the spec unchanged and its root epic closed:
// the nothing-to-run box says the spec is built, exit 0, and nothing is broken down.
func TestSpecRunSaysABuiltSpecIsBuilt(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, tools := breakdownFixture(t, "", notToRun)
	fileForBd(t, tools, keptRoot(dispatch.StatusClosed, sha256Of(read(t, spec)), "c0ffee"),
		keptTicket("s-1.1", "s-1", "List the things", dispatch.StatusClosed))
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 0 || !strings.Contains(stdout, " ✓ "+keptSpec+" is built (s-1)\n") || strings.Contains(stdout, "START") {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if _, err := os.Stat(filepath.Join(tools, "claude-args")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the spec was broken down again (%v)", err)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	for _, want := range []string{"SPEC: " + keptSpec + " is unchanged, and s-1 is closed\n",
		"READY_EMPTY after 0 tickets: everything is done; SCOPE_DONE: s-1 and its 1 subticket are merged\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}

// The nightly command again with the spec changed: the new version is committed, and the headless
// breakdown runs in update mode: its instructions are the update's, its first message names the root
// epic, the version its tree was filed for and the update's file, which has the diff and the tree.
// What it files goes under the root epic, whose SHA-256 orchestra then sets to the new version's.
func TestSpecRunUpdatesTheTreeForTheChange(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, tools := breakdownFixture(t, helpWithPrompts, filing(t, "toolu_1", "bd create --parent s-1 --type task "+
		"--title 'Sort the things' --acceptance 'Sorts them' --metadata "+ticketMeta("sort.go")+" --silent")+
		answering(t, specAnswer{Root: "s-1"}))
	old := writeSpec(t, filepath.Join(t.TempDir(), "app.md"), carryText) // as an earlier night committed it
	was, _, err := project.CommitSpec(context.Background(), repo, old)
	if err != nil {
		t.Fatal(err)
	}
	fileForBd(t, tools, keptRoot(dispatch.StatusOpen, was.SHA256, was.Commit),
		keptTicket("s-1.1", "s-1", "List the things", dispatch.StatusClosed))
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	head := gitIn(t, repo, "rev-parse", "HEAD")
	if subject := gitIn(t, repo, "log", "-1", "--format=%s"); subject != "orchestra: update spec app.md" {
		t.Errorf("HEAD: %q", subject)
	}
	for _, want := range []string{"the spec " + keptSpec + " changed since s-1 was filed: updating its tickets\n",
		"updated s-1 for the spec's change (1 epic, 2 tickets)\n", " ○ Nothing under s-1 is ready to run\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{"The spec at " + keptSpec + " has changed since its Beads tickets were filed under " +
		"s-1: update them for the change.\n", "- commit: " + head + "\n", "- root epic: s-1\n",
		"- filed for: sha256 " + was.SHA256 + ", commit " + was.Commit + "\n",
		"- the change and the tree: .orchestra/run/spec-update.md\n"} {
		if in := read(t, filepath.Join(tools, "claude-in")); !strings.Contains(in, want) {
			t.Errorf("the first message lacks %q:\n%s", want, in)
		}
	}
	if prompt := read(t, filepath.Join(repo, project.RunPath("spec-prompt.md"))); !strings.Contains(prompt,
		"## Updating the tree") || !strings.Contains(prompt, "## Answering") {
		t.Errorf("the instructions aren't the headless update's:\n%s", prompt)
	}
	update := read(t, filepath.Join(repo, project.RunPath(project.SpecUpdateName)))
	for _, want := range []string{"\n-It lists things.\n+It lists things, sorted.\n",
		"- s-1 (open epic): An app\n  - s-1.1 (closed task): List the things\n"} {
		if !strings.Contains(update, want) {
			t.Errorf("the update's file lacks %q:\n%s", want, update)
		}
	}
	if calls := read(t, filepath.Join(tools, "bd-calls")); !strings.Contains(calls,
		"update|s-1|--set-metadata|spec_sha256="+sha256Of(carryChanged)+"|\n") ||
		!strings.Contains(calls, "update|s-1|--set-metadata|spec_commit="+head+"|\n") {
		t.Errorf("the root epic's SHA-256 and commit weren't set to the new version's:\n%s", calls)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	for _, want := range []string{"SPEC: " + keptSpec + " changed since s-1 was filed for sha256 " + was.ShortSHA256() +
		"; updating its tree for the change\n", "SPEC: updated s-1 for the change (1 epic, 2 tickets)\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
}

// In update mode, the count under orchestra's pane of what the refinement beside it has filed leaves
// out the tree filed already: it counts what carries the spec's metadata past what did as the update
// began.
func TestRefineInUpdateModeCountsOnlyWhatItFiles(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	line := &tui.InterviewLine{Out: &out, Width: func() int { return 120 }}
	count := make(chan filedCount, 1)
	count <- filedCount{epics: 2, tickets: 3, ok: true}
	s := refineSubject{spec: testSpec, update: &specUpdate{Before: countOf([]dispatch.Ticket{
		issue("s-1", "epic", "", "{}", ""), issue("s-1.1", "task", "s-1", "{}", ""),
		issue("s-1.2", "task", "s-1", "{}", "")})}, every: time.Hour}.shown(line, time.Now())
	shown := s.(*refineShown)
	shown.counted, shown.count = time.Now(), count
	shown.show(t.Context(), "", dispatch.StateWorking)
	if got := ansi.Strip(out.String()); !strings.Contains(got, " · 1 epic, 1 ticket filed") {
		t.Errorf("the line: %q", got)
	}
}
