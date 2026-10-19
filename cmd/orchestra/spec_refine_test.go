//go:build unix

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// refineSpecPath is where the refinement tests' spec, from outside the repository, is kept in it.
const refineSpecPath = ".orchestra/specs/app.md"

// refineSpecText is the refinement tests' spec.
const refineSpecText = "# An app\n\nIt lists things, sorted.\n"

// refineCommitted is what orchestra says first, having committed the spec.
func refineCommitted() string {
	sum := sha256.Sum256([]byte(refineSpecText))
	return "committed the spec " + refineSpecPath + " (sha256 " + hex.EncodeToString(sum[:])[:12] + ")\n"
}

// refinePaneLine is what orchestra's pane says while the spec is refined in the pane beside it.
const refinePaneLine = "Breaking the spec down with claude in the pane on the right, which asks you only what the " +
	"spec leaves open and closes once the tickets are filed. Type /exit there to leave without filing more; " +
	"Ctrl+C here stops.\n"

// filedTree is bd as a refinement files into it: a specTree that grows while orchestra's pane counts
// what is in it, from another goroutine.
type filedTree struct {
	mu   sync.Mutex
	tree specTree
}

// file files issues, as claude does with bd create.
func (f *filedTree) file(issues ...dispatch.Ticket) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tree.issues = append(f.tree.issues, issues...)
}

// set is what orchestra set of the issues' metadata, as specTree keeps it.
func (f *filedTree) set() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tree.set)
}

func (f *filedTree) Show(ctx context.Context, id string) (dispatch.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tree.Show(ctx, id)
}

func (f *filedTree) Descendants(ctx context.Context, id string) ([]dispatch.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tree.Descendants(ctx, id)
}

func (f *filedTree) WithMetadata(ctx context.Context, key, value string) ([]dispatch.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tree.WithMetadata(ctx, key, value)
}

func (f *filedTree) Cycles(ctx context.Context) ([][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tree.Cycles(ctx)
}

func (f *filedTree) SetMetadata(ctx context.Context, id, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tree.SetMetadata(ctx, id, key, value)
}

// The refinement's tree: the root epic s-1, which carries the spec's path but not its SHA-256 or
// commit, and its tickets s-1.1 and s-1.2.
var (
	refineRoot    = issue("s-1", "epic", "", `{"spec":"`+refineSpecPath+`"}`, "")
	refineTickets = []dispatch.Ticket{
		issue("s-1.1", "task", "s-1", `{"spec":"`+refineSpecPath+`","files":["list.go"]}`, "Lists them"),
		issue("s-1.2", "task", "s-1", `{"spec":"`+refineSpecPath+`","files":["sort.go"]}`, "Sorts them"),
	}
)

// handBack names root in the repository's spec.json, as the refinement does once it has filed the tree.
func handBack(t *testing.T, repo, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, project.RunPath(project.SpecName)), []byte(`{"epic":"`+root+`"}`+"\n"),
		0o644); err != nil {
		t.Error(err)
	}
}

// refineOutcome is how a spec run with --refine ended before its loop: the root epic it is scoped to,
// the exit code, what orchestra showed on its output and its error output, and the first message the
// session on orchestra's terminal was given if it ran there ("" if it didn't).
type refineOutcome struct {
	root               string
	code               int
	out, err, terminal string
}

// refineSpec runs the step of a spec run with --refine before its loop, in repo, a git repository with
// nothing to commit, for a spec outside it: the spec is committed, then refined in a pane on panes
// (orchestra's being w1:p1), or on the terminal if pane is false, with bd as tree has it. The session
// on the terminal runs onTerminal, if given. width is the terminal orchestra's output is, in columns,
// on which its pane shows the refinement's progress; 0 is output that isn't a terminal.
func refineSpec(ctx context.Context, t *testing.T, repo string, panes *fakePanes, pane bool, tree *filedTree,
	onTerminal func(), width int) refineOutcome {
	t.Helper()
	var o refineOutcome
	var out, errOut strings.Builder
	spec := writeSpec(t, filepath.Join(t.TempDir(), "app.md"), refineSpecText)
	b := refineBreakdown{repo: repo, panes: panes, tracker: tree, out: &out, err: &errOut,
		terminal: func(_ context.Context, message string, _ project.RefineMode) error {
			o.terminal = message
			if onTerminal != nil {
				onTerminal()
			}
			return nil
		}}
	if pane {
		b.pane = "w1:p1"
	}
	if width > 0 {
		b.line = &tui.InterviewLine{Out: &out, Width: func() int { return width }}
	}
	root, code := specRun{file: spec, repo: repo, refine: true, breakdown: b, tracker: tree, out: &out,
		err: &errOut}.run(testCtx(ctx, t))
	o.root, o.code, o.out, o.err = root, code, out.String(), errOut.String()
	return o
}

// refineFixture is a repository with nothing to commit, and a claude on PATH, which the refinement
// needs there but these tests never run.
func refineFixture(t *testing.T) string {
	t.Helper()
	repo, _ := gitRepo(t)
	tools := t.TempDir()
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	return repo
}

// With --refine, the spec is broken down in a pane split off orchestra's, to its right, in the main
// checkout: claude starts there with the refine instructions and a first message, one line, naming
// the committed spec. Once it has filed the tree, handed the root epic back in spec.json and ended its
// turn, the pane is closed, the tree is checked as after any breakdown (the root epic gets the spec's
// SHA-256 and commit it lacks), and the run is scoped to the root epic, without asking.
func TestRefineInAPaneRunsTheRootEpicItHandsBack(t *testing.T) {
	t.Parallel()
	repo := refineFixture(t)
	synctest.Test(t, func(t *testing.T) {
		tree := &filedTree{}
		panes := &fakePanes{
			agent: []dispatch.AgentState{dispatch.StateGone, dispatch.StateWorking, dispatch.StateIdle,
				dispatch.StateWorking, dispatch.StateWorking, dispatch.StateIdle},
			step: func(read int) {
				switch read {
				case 4: // the user agreed: it files the tree
					tree.file(refineRoot)
					tree.file(refineTickets...)
				case 5:
					handBack(t, repo, "s-1")
				}
			},
		}
		o := refineSpec(t.Context(), t, repo, panes, true, tree, nil, 0)
		if o.root != "s-1" || o.code != dispatch.ExitOK || o.terminal != "" {
			t.Fatalf("got %v, exit %v, on the terminal %q; stderr:\n%s", o.root, o.code, o.terminal, o.err)
		}
		if want := []string{"split w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
			t.Errorf("Herdr was asked to %q, want %q", panes.calls, want)
		}
		head := gitIn(t, repo, "rev-parse", "HEAD")
		sum := sha256.Sum256([]byte(refineSpecText))
		prompt := filepath.Join(repo, project.RunPath("spec-prompt.md"))
		if want := []string{"--append-system-prompt-file", prompt, "--", "Break the spec at " + refineSpecPath +
			" down into Beads tickets with me (sha256 " + hex.EncodeToString(sum[:]) + ", commit " + head + ")."}; !slices.Equal(panes.launched, want) {
			t.Errorf("claude started with %q, want %q", panes.launched, want)
		}
		if b, err := os.ReadFile(prompt); err != nil || !strings.Contains(string(b), "## Questions") ||
			!strings.Contains(string(b), ".orchestra/run/spec.json") || strings.Contains(string(b), "## Answering") {
			t.Errorf("the instructions at %s aren't the refinement's (%v):\n%s", prompt, err, b)
		}
		if o.out != refineCommitted()+refinePaneLine+"broke the spec down into s-1 (1 epic, 2 tickets)\n" {
			t.Errorf("orchestra's pane:\n%s", o.out)
		}
		set := tree.set()
		if len(set) != 2 || !strings.HasPrefix(set[0], "s-1 spec_sha256=") || set[1] != "s-1 spec_commit="+head {
			t.Errorf("the checks set %q", set)
		}
		if want := "warning: the breakdown left s-1's spec_sha256, spec_commit metadata out or wrong; orchestra " +
			"set it\n"; o.err != want {
			t.Errorf("stderr:\n%s\nwant:\n%s", o.err, want)
		}
	})
}

// /exit in the refinement's pane, or closing the pane, before it hands a root epic back files nothing
// more and ends the run: exit 0 when nothing was filed for the spec, else exit 2, listing what was
// filed and how to carry on.
func TestRefineLeftWithoutAHandBack(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: refines a spec on real git with fake tools")
	}
	for _, tc := range []struct {
		name   string
		filed  bool
		closed bool // the user closed the pane rather than type /exit
		code   int
		out    string
		err    string
	}{
		{"nothing filed", false, false, dispatch.ExitOK, "The refinement filed nothing; nothing to run.\n", ""},
		{"pane closed, nothing filed", false, true, dispatch.ExitOK,
			"The refinement filed nothing; nothing to run.\n", ""},
		{"something filed", true, false, dispatch.ExitSetup, "",
			"orchestra: the refinement ended without handing back a root epic, so there is nothing to run.\n" +
				"It filed 1 epic and 2 tickets for " + refineSpecPath + ", under s-1:\n" +
				"  s-1  epic  \n  s-1.1  task  \n  s-1.2  task  \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := refineFixture(t)
			synctest.Test(t, func(t *testing.T) {
				tree := &filedTree{}
				var panes *fakePanes
				panes = &fakePanes{agent: []dispatch.AgentState{dispatch.StateIdle, dispatch.StateWorking,
					dispatch.StateIdle, dispatch.StateGone}, step: func(read int) {
					if read == 2 && tc.filed {
						tree.file(refineRoot)
						tree.file(refineTickets...)
					}
					if read == 3 && tc.closed {
						panes.closed = true
					}
				}}
				o := refineSpec(t.Context(), t, repo, panes, true, tree, nil, 0)
				if o.root != "" || o.code != tc.code {
					t.Fatalf("got %v, exit %v; stderr:\n%s", o.root, o.code, o.err)
				}
				if want := refineCommitted() + refinePaneLine + tc.out; o.out != want {
					t.Errorf("orchestra's pane:\n%s\nwant:\n%s", o.out, want)
				}
				if tc.err != "" {
					tc.err += "Run it again to carry on: orchestra --spec "
				}
				if !strings.HasPrefix(o.err, tc.err) || tc.err == "" && o.err != "" ||
					tc.err != "" && !strings.HasSuffix(o.err, " --refine\n") {
					t.Errorf("stderr:\n%s\nwant it to start with:\n%s", o.err, tc.err)
				}
				if !slices.Contains(panes.calls, "close w1:p2") && !tc.closed {
					t.Errorf("the refinement's pane wasn't closed: %q", panes.calls)
				}
			})
		})
	}
}

// Under its fixed line, orchestra's pane says what claude is doing and how long the refinement has
// run, as the interview's does, and how many epics and tickets carry the spec's metadata, counted
// from bd every few seconds: the count read shows at the next reading of the pane, until the next
// count. The line is gone before orchestra goes on, which looks as it does without it.
func TestRefineProgressCountsTheFiledIssues(t *testing.T) {
	t.Parallel()
	agent := []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking, dispatch.StateBlocked,
		dispatch.StateIdle, dispatch.StateWorking, "", dispatch.StateWorking, dispatch.StateWorking,
		dispatch.StateWorking, dispatch.StateIdle}
	refine := func(width int) (refineOutcome, int) {
		repo := refineFixture(t)
		var o refineOutcome
		var panes *fakePanes
		synctest.Test(t, func(t *testing.T) {
			tree := &filedTree{}
			panes = &fakePanes{agent: agent, step: func(read int) {
				switch read {
				case 3:
					tree.file(refineRoot)
				case 5:
					tree.file(refineTickets...)
				case 9: // seen at the next reading: the file is looked at before claude's state
					handBack(t, repo, "s-1")
				}
			}}
			o = refineSpec(t.Context(), t, repo, panes, true, tree, nil, width)
		})
		return o, panes.reads
	}
	o, reads := refine(80)
	if o.root != "s-1" || o.code != dispatch.ExitOK {
		t.Fatalf("got %v, exit %v; stderr:\n%s", o.root, o.code, o.err)
	}
	before, lines, after := progressLines(t, o.out)
	if before != refineCommitted()+refinePaneLine {
		t.Errorf("before the progress line, orchestra's pane showed:\n%q", before)
	}
	if want := []string{
		"claude is working  1s",
		"claude is waiting for a permission answer  2s · 0 epics, 0 tickets filed",
		"claude is waiting for you  3s · 0 epics, 0 tickets filed",
		"claude is working  4s · 0 epics, 0 tickets filed",
		"claude is working  5s · 0 epics, 0 tickets filed",
		"claude is working  6s · 0 epics, 0 tickets filed",
		"claude is working  7s · 1 epic, 2 tickets filed",
		"claude is working  8s · 1 epic, 2 tickets filed",
	}; !slices.Equal(lines, want) {
		t.Errorf("orchestra's pane showed\n%q\nwant\n%q", lines, want)
	}
	plain, plainReads := refine(0)
	if before+after != plain.out {
		t.Errorf("after the progress line, orchestra's pane showed\n%q\nwant, as without it,\n%q", after,
			strings.TrimPrefix(plain.out, before))
	}
	if reads != plainReads || reads != len(agent) {
		t.Errorf("Herdr was read %d times, %d without the progress line, want %d", reads, plainReads, len(agent))
	}
}

// Ctrl+C in orchestra's pane, or SIGTERM or SIGHUP, ends the refinement while it waits: its pane is
// closed, which ends claude, and the run stops, exit 130, naming the root epic filed so far, as bd has
// it, and how to carry on.
func TestRefineStopped(t *testing.T) {
	t.Parallel()
	repo := refineFixture(t)
	synctest.Test(t, func(t *testing.T) {
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		tree := &filedTree{}
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking}, step: func(read int) {
			if read == 3 {
				tree.file(refineRoot)
				stop()
			}
		}}
		o := refineSpec(ctx, t, repo, panes, true, tree, nil, 0)
		if o.root != "" || o.code != dispatch.ExitInterrupted {
			t.Fatalf("got %v, exit %v; stderr:\n%s", o.root, o.code, o.err)
		}
		if !strings.HasPrefix(o.err, "orchestra: stopped while the spec was broken down, with s-1 filed.\n"+
			"Run it again to carry on: orchestra --spec ") || !strings.HasSuffix(o.err, " --refine\n") {
			t.Errorf("stderr:\n%s", o.err)
		}
		if last := panes.calls[len(panes.calls)-1]; last != "close w1:p2" {
			t.Errorf("the refinement's pane wasn't closed: %q", panes.calls)
		}
	})
}

// Where orchestra runs in no pane, or the pane can't be opened, the refinement takes orchestra's own
// terminal, with the same first message; what it hands back runs as from a pane.
func TestRefineOnTheTerminal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		pane  bool
		panes fakePanes
		err   string
	}{
		{"no pane", false, fakePanes{}, ""},
		{"split refused", true, fakePanes{splitErr: errors.New("herdr pane split w1:p1: exit status 1: no such pane")},
			"orchestra couldn't open the refinement in a pane beside its own, so claude takes this terminal: " +
				"herdr pane split w1:p1: exit status 1: no such pane\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := refineFixture(t)
			synctest.Test(t, func(t *testing.T) {
				tree := &filedTree{}
				panes := tc.panes
				o := refineSpec(t.Context(), t, repo, &panes, tc.pane, tree, func() {
					tree.file(refineRoot)
					tree.file(refineTickets...)
					handBack(t, repo, "s-1")
				}, 0)
				if o.root != "s-1" || o.code != dispatch.ExitOK {
					t.Fatalf("got %v, exit %v; stderr:\n%s", o.root, o.code, o.err)
				}
				if !strings.HasPrefix(o.terminal, "Break the spec at "+refineSpecPath+" down into Beads tickets with me") {
					t.Errorf("the session on the terminal was given %q", o.terminal)
				}
				if !strings.HasPrefix(o.err, tc.err) || strings.Contains(o.out, refinePaneLine) {
					t.Errorf("stderr:\n%s\nstdout:\n%s", o.err, o.out)
				}
				if len(panes.calls) > 1 {
					t.Errorf("Herdr was asked to %q", panes.calls)
				}
			})
		})
	}
}

// On orchestra's own terminal, the refinement needs one: run with input or output that isn't a
// terminal, it starts no claude and says so.
func TestRefineNeedsATerminal(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	var out strings.Builder
	err := refineOnTerminal(t.Context(), nil, repo, "", "Break the spec down", project.RefineSpec,
		strings.NewReader(""), &out, &out)
	if !errors.Is(err, errRefineNeedsTerminal) || out.Len() > 0 {
		t.Errorf("got %v; output:\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(repo, project.RunPath("spec-prompt.md"))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the instructions were written: %v", err)
	}
}

// A whole spec run with --refine in a Herdr pane: the refinement in the pane beside orchestra's hands
// back the root epic, and the run is scoped to it, its START line naming the spec; run without a
// terminal and outside any pane, the refinement can't take orchestra's terminal, and the run ends with
// exit 2 once the spec is committed.
func TestSpecRunWithRefine(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra twice on real git with fake tools")
	}
	repo, spec, tools := specRunFixture(t)
	setenv(t, "HERDR_PANE_ID", "w1:p1")
	panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle},
		step: func(read int) {
			if read == 2 {
				handBack(t, repo, "s-1")
			}
		}}
	breakDownWith(t, func(s *session) specBreakdown {
		b := newRefineBreakdown(s)
		b.panes = panes
		return b
	})
	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain", "--refine")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	for _, want := range []string{refinePaneLine, "broke the spec down into s-1 (2 epics, 2 tickets)\n",
		" · spec " + refineSpecPath + " ("} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if !slices.Contains(panes.calls, "close w1:p2") {
		t.Errorf("the refinement's pane wasn't closed: %q", panes.calls)
	}
	if calls := read(t, filepath.Join(tools, "bd-calls")); !strings.Contains(calls,
		"ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|--parent|s-1|\n") {
		t.Errorf("the run wasn't scoped to s-1:\n%s", calls)
	}
	if log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log"))); !strings.Contains(log,
		"SPEC: refining "+refineSpecPath+" with claude and you\n") {
		t.Errorf("the log:\n%s", log)
	}

	setenv(t, "HERDR_PANE_ID", "")
	stdout, stderr, code = runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain", "--refine")
	if code != 2 || !strings.Contains(stderr, "orchestra couldn't break the spec down: "+errRefineNeedsTerminal.Error()+
		"\n") || strings.Contains(stdout, "START") {
		t.Errorf("without a terminal: exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
}
