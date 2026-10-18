//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// featureTree is bd as a feature's session filed into it: each issue by its ID, the issues under
// each epic, and the blocks cycles. A read of an ID in fail fails, and a list by a metadata key
// there.
type featureTree struct {
	issues   map[string]dispatch.Ticket
	children map[string][]string
	cycles   [][]string
	fail     map[string]bool
	reads    []string // the epics whose children were listed
}

func (f *featureTree) Show(_ context.Context, id string) (dispatch.Ticket, error) {
	t, ok := f.issues[id]
	if !ok || f.fail[id] {
		return dispatch.Ticket{}, fmt.Errorf("bd show %s --json: exit status 1", id)
	}
	return t, nil
}

func (f *featureTree) Children(_ context.Context, id string) ([]dispatch.Ticket, []dispatch.Link, error) {
	f.reads = append(f.reads, id)
	if f.fail["children "+id] {
		return nil, nil, errors.New("bd list --json: exit status 1")
	}
	var under []dispatch.Ticket
	for _, c := range f.children[id] {
		under = append(under, f.issues[c])
	}
	return under, nil, nil
}

func (f *featureTree) WithMetadata(_ context.Context, key, value string) ([]dispatch.Ticket, error) {
	if f.fail[key] {
		return nil, errors.New("bd list --json: exit status 1")
	}
	var with []dispatch.Ticket
	for _, t := range f.issues {
		if t.MetadataText(key) == value {
			with = append(with, t)
		}
	}
	slices.SortFunc(with, func(a, b dispatch.Ticket) int { return strings.Compare(a.ID, b.ID) })
	return with, nil
}

func (f *featureTree) Cycles(context.Context) ([][]string, error) {
	if f.fail["cycles"] {
		return nil, errors.New("bd dep cycles --json: exit status 1")
	}
	return f.cycles, nil
}

// ticketOf is an open issue of bd's: its files in its metadata (none for no metadata), and its
// acceptance criteria.
func ticketOf(id, kind, title, acceptance string, files ...string) dispatch.Ticket {
	t := dispatch.Ticket{ID: id, IssueType: kind, Title: title, Status: "open", AcceptanceCriteria: acceptance}
	if len(files) > 0 {
		b, _ := json.Marshal(map[string][]string{"files": files})
		t.Metadata = b
	}
	return t
}

// newFeatureTree is the epic f-1, "Export", with a child epic f-1.3 for a separable part: f-1.1 has
// its files and acceptance criteria, f-1.2 neither; under f-1.3, f-1.3.1 has files but no
// acceptance criteria.
func newFeatureTree() *featureTree {
	f := &featureTree{issues: map[string]dispatch.Ticket{}, children: map[string][]string{
		"f-1": {"f-1.1", "f-1.2", "f-1.3"}, "f-1.3": {"f-1.3.1"},
	}, fail: map[string]bool{}}
	for _, t := range []dispatch.Ticket{
		ticketOf("f-1", "epic", "Export", ""),
		ticketOf("f-1.1", "task", "Add the encoder", "It encodes.", "enc.go"),
		ticketOf("f-1.2", "task", "Add the flag", ""),
		ticketOf("f-1.3", "epic", "The CSV part", ""),
		ticketOf("f-1.3.1", "feature", "Add CSV", "", "csv.go"),
		ticketOf("x-1", "task", "Elsewhere", "Done.", "x.go"),
	} {
		f.issues[t.ID] = t
	}
	return f
}

// The epic a feature's session hands back is checked as a spec's tree is, without what depends on
// a spec's metadata: it must be an epic; open tickets under it, at every level, without files
// metadata or acceptance criteria, and blocks cycles that hold one of its issues, are warnings; what
// can't be read is a warning too, and leaves the tickets uncounted.
func TestCheckFeatureWarnsOfWhatIsAmiss(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		epic     string
		change   func(*featureTree)
		counts   string
		warnings []string
		err      string
	}{
		{"its tree", "f-1", nil, " (1 epic, 3 tickets)", []string{
			"tickets under f-1 without files metadata, whose files orchestra guesses (1): f-1.2",
			"tickets under f-1 without acceptance criteria (2): f-1.2, f-1.3.1",
		}, ""},
		{"a cycle among its tickets", "f-1", func(f *featureTree) {
			f.cycles = [][]string{{"x-1", "x-2"}, {"f-1.1", "f-1.3.1"}}
			f.children["f-1"] = []string{"f-1.1", "f-1.3"}
			f.issues["f-1.3.1"] = ticketOf("f-1.3.1", "feature", "Add CSV", "Writes CSV.", "csv.go")
		}, " (1 epic, 2 tickets)", []string{
			"a blocks cycle: f-1.1 → f-1.3.1 → f-1.1; none of them can start until it is broken (bd dep remove)",
		}, ""},
		{"tickets bd can't list", "f-1", func(f *featureTree) { f.fail["children f-1.3"] = true }, "",
			[]string{"can't list the tickets under f-1: bd list --json: exit status 1"}, ""},
		{"cycles bd can't list", "f-1", func(f *featureTree) {
			f.children["f-1"] = []string{"f-1.1"}
			f.fail["cycles"] = true
		}, " (1 ticket)", []string{"can't look for blocks cycles: bd dep cycles --json: exit status 1"}, ""},
		{"not an epic", "f-1.1", nil, "", nil,
			"f-1.1, which it named as the feature's epic, is a task, not an epic"},
		{"unknown", "f-9", nil, "", nil, "can't read f-9, the epic it named: bd show f-9 --json: exit status 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := newFeatureTree()
			if tc.change != nil {
				tc.change(tree)
			}
			filed, warnings, err := checkFeature(t.Context(), tree, tc.epic)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("got %v, want the error %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if filed.title != "Export" || filed.counts() != tc.counts || !slices.Equal(warnings, tc.warnings) {
				t.Errorf("got %q%q, warnings\n%q\nwant Export%q, warnings\n%q", filed.title, filed.counts(),
					warnings, tc.counts, tc.warnings)
			}
		})
	}
}

// talkedThrough is how a feature talked through on orchestra's terminal (as the session plays it,
// handing back epic unless it is "") ended: the epic, the exit code, and what orchestra wrote.
type talkedThrough struct {
	epic, out, err string
	code           int
}

// talkFeatureThrough talks paneRequest through on the terminal in repo (featureRepo), with bd as tree
// has it; the session hands epic back.
func talkFeatureThrough(t *testing.T, repo string, tree featureEpicTracker, epic string) talkedThrough {
	t.Helper()
	var out, errOut strings.Builder
	b := refineBreakdown{repo: repo, out: &out, err: &errOut,
		terminal: func(context.Context, string, project.RefineMode) error {
			if epic != "" {
				if err := os.WriteFile(filepath.Join(repo, project.RunPath(project.FeatureName)),
					[]byte(`{"epic":"`+epic+`"}`), 0o644); err != nil {
					t.Error(err)
				}
			}
			return nil
		}}
	var r talkedThrough
	r.epic, r.code = featureTalk{request: paneRequest, runID: paneRun, repo: repo, breakdown: b, tracker: tree,
		out: &out, err: &errOut}.run(t.Context())
	r.out, r.err = out.String(), errOut.String()
	return r
}

// What is amiss in the epic handed back is said, and the run starts on it all the same; an ID that
// isn't an epic's ends the run, exit 2, without running it.
func TestFeatureRunsWhatIsHandedBackDespiteWarnings(t *testing.T) {
	t.Parallel()
	repo := featureRepo(t)
	r := talkFeatureThrough(t, repo, newFeatureTree(), "f-1")
	if r.epic != "f-1" || r.code != dispatch.ExitOK {
		t.Fatalf("got %q, exit %d; stderr:\n%s", r.epic, r.code, r.err)
	}
	if want := "warning: tickets under f-1 without files metadata, whose files orchestra guesses (1): f-1.2\n" +
		"warning: tickets under f-1 without acceptance criteria (2): f-1.2, f-1.3.1\n"; r.err != want {
		t.Errorf("stderr:\n%s\nwant:\n%s", r.err, want)
	}
	if want := "filed the feature as f-1, Export (1 epic, 3 tickets)\n"; r.out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.out, want)
	}

	r = talkFeatureThrough(t, repo, newFeatureTree(), "f-1.1")
	if want := "orchestra won't run what the interview handed back: f-1.1, which it named as the feature's " +
		"epic, is a task, not an epic\n"; r.epic != "" || r.code != dispatch.ExitSetup || r.err != want || r.out != "" {
		t.Errorf("got %q, exit %d; stdout:\n%s\nstderr:\n%s", r.epic, r.code, r.out, r.err)
	}
}

// Without a pane, the session takes orchestra's terminal; with neither, the run ends, exit 2, before
// claude starts or anything is filed, naming --spec as the way to break a request down unattended.
func TestFeatureNeedsATerminal(t *testing.T) {
	t.Parallel()
	repo := featureRepo(t)
	tree := newFeatureTree()
	var out, errOut strings.Builder
	b := refineBreakdown{repo: repo, out: &out, err: &errOut,
		terminal: func(ctx context.Context, message string, m project.RefineMode) error {
			return refineOnTerminal(ctx, nil, repo, "", message, m, strings.NewReader(""), &out, &errOut)
		}}
	epic, code := featureTalk{request: paneRequest, repo: repo, breakdown: b, tracker: tree, out: &out,
		err: &errOut}.run(t.Context())
	if want := "orchestra couldn't talk the feature through: talking the feature through needs you at a " +
		"terminal, and orchestra's input or output isn't one: orchestra --spec <file> breaks a request written " +
		"to a file down unattended\n"; epic != "" || code != dispatch.ExitSetup || errOut.String() != want ||
		out.Len() > 0 {
		t.Errorf("got %q, exit %d; stdout:\n%s\nstderr:\n%s", epic, code, out.String(), errOut.String())
	}
	if len(tree.reads) > 0 {
		t.Errorf("bd was read for an epic: %q", tree.reads)
	}
}

// A feature typed at the start is talked through with Claude Code whatever agent the workers are:
// with codex workers it goes to the session, not the organs.
func TestTypedFeatureWithOtherWorkersIsTalkedThrough(t *testing.T) {
	t.Parallel()
	dir := interviewTools(t, "f-1")
	term, repo, exit := runApartOnTerminal(t, "--agent", "codex")
	describeFeature(t, term)
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	if text := terminalMessage(t, repo, read(t, filepath.Join(dir, "claude-args"))); text != paneRequest {
		t.Errorf("claude's first message holds the description %q", text)
	}
	out := screenOf(term)
	for _, want := range []string{"Talking the feature through with claude",
		"filed the feature as f-1, JSON output (2 tickets)\n", "○ Nothing under f-1 is ready to run"} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "organs") || strings.Contains(out, "screened") {
		t.Errorf("the organs had the feature:\n%s", out)
	}
}

// Without claude, a typed feature isn't talked through, nor planned by the organs: the run ends,
// exit 2, saying so, with nothing filed.
func TestTypedFeatureWithoutClaudeEndsSayingSo(t *testing.T) {
	t.Parallel()
	dir := interviewTools(t, "f-1")
	if err := os.Remove(filepath.Join(dir, "claude")); err != nil {
		t.Fatal(err)
	}
	hideClaude(t, dir)
	term, _, exit := runOnTerminal(t)
	describeFeature(t, term)
	code, stderr := exit()
	if want := "orchestra can't talk the feature through: it needs Claude Code, and claude not found.\n"; code !=
		dispatch.ExitSetup || stderr != want {
		t.Errorf("exit %d, stderr:\n%s\nwant:\n%s", code, stderr, want)
	}
	if out := screenOf(term); strings.Contains(out, "screened") || strings.Contains(out, noFeature) {
		t.Errorf("the terminal:\n%s", out)
	}
	if calls := read(t, filepath.Join(dir, "bd-calls")); strings.Contains(calls, "f-1") {
		t.Errorf("bd was asked about the epic:\n%s", calls)
	}
}
