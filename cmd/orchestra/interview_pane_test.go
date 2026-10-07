package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// paneRequest is the feature typed at the question: a list, on two lines.
const paneRequest = "- Add a --json flag\n- to the list command"

// fakePanes is Herdr for the interview in a pane, orchestra's being w1:p1. The interview's pane,
// w1:p2 once split off it, holds what each read of its agent finds in turn, the last one for every
// read after; "" is a read Herdr fails. step, if set, is called at the start of each read, numbered
// from 1: it plays what claude and the user do meanwhile, such as filing the feature, closing the
// pane or pressing Ctrl+C in orchestra's.
type fakePanes struct {
	splitErr, launchErr error
	agent               []dispatch.AgentState
	step                func(read int)

	calls    []string // split, launch and close, with the pane each was given
	launched []string // the arguments claude was started with
	reads    int
	closed   bool // the interview's pane is gone
}

func (f *fakePanes) SplitPane(_ context.Context, pane, cwd string) (string, error) {
	f.calls = append(f.calls, "split "+pane+" "+cwd)
	if f.splitErr != nil {
		return "", f.splitErr
	}
	return "w1:p2", nil
}

func (f *fakePanes) LaunchInPane(_ context.Context, pane, kind string, args []string) error {
	f.calls = append(f.calls, "launch "+pane+" "+kind)
	f.launched = args
	return f.launchErr
}

func (f *fakePanes) PaneAgent(_ context.Context, pane string) (string, string, dispatch.AgentState, error) {
	f.reads++
	if f.step != nil {
		f.step(f.reads)
	}
	if f.closed {
		return "", "", dispatch.StateGone, nil
	}
	switch st := f.agent[min(f.reads, len(f.agent))-1]; st {
	case "":
		return "", "", "", errors.New("herdr: server busy")
	case dispatch.StateGone:
		return "", "", st, nil
	default:
		return "", "claude", st, nil
	}
}

func (f *fakePanes) PaneOpen(context.Context, string) (bool, error) { return !f.closed, nil }

// ClosePane closes the pane, which is no error when it is gone, as Herdr's adapter has it. It needs
// a context that hasn't ended: orchestra closes the pane after a stop too.
func (f *fakePanes) ClosePane(ctx context.Context, pane string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.calls = append(f.calls, "close "+pane)
	f.closed = true
	return nil
}

// filedEpic is bd knowing the epic f-1 and its two tickets, the second after the first.
type filedEpic struct{}

func (filedEpic) Show(_ context.Context, id string) (dispatch.Ticket, error) {
	if id != "f-1" {
		return dispatch.Ticket{}, fmt.Errorf("no issue found matching %s", id)
	}
	return dispatch.Ticket{ID: "f-1", Title: "JSON output", IssueType: "epic"}, nil
}

func (filedEpic) Children(context.Context, string) ([]dispatch.Ticket, []dispatch.Link, error) {
	return []dispatch.Ticket{
		{ID: "f-1.1", Title: "Add the JSON encoder", IssueType: "feature"},
		{ID: "f-1.2", Title: "Add the --json flag", IssueType: "task"},
	}, []dispatch.Link{{Blocker: "f-1.1", Blocked: "f-1.2"}}, nil
}

// fileFeature names the epic f-1 in the repository's feature.json, as the interview does once it
// has filed the feature.
func fileFeature(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, project.RunPath(project.FeatureName)), []byte(`{"epic":"f-1"}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
}

// paneOutcome is how an interview in a pane ended: what run returned, what orchestra showed on its
// output and its error output, and the instructions the terminal session was given if orchestra
// fell back to it ("" if it didn't).
type paneOutcome struct {
	epic        string
	code        int
	out, err    string
	fellBack    string
	closedFirst bool // the interview's pane was closed before the terminal session started
}

// interviewInPane runs the feature interview for paneRequest in repo, a git repository with nothing
// to commit, its session in a pane on panes, and answers the question with answer. The terminal
// session it may fall back to files the feature. ctx is the interview's, which a stop signal ends.
func interviewInPane(ctx context.Context, t *testing.T, repo string, panes *fakePanes, answer string) paneOutcome {
	t.Helper()
	return interviewInPaneOn(ctx, t, repo, panes, answer, 0)
}

// interviewInPaneOn is interviewInPane with orchestra's output a terminal width columns wide, on
// which orchestra's pane shows the interview's progress; 0 is output that isn't a terminal.
func interviewInPaneOn(
	ctx context.Context, t *testing.T, repo string, panes *fakePanes, answer string, width int,
) paneOutcome {
	t.Helper()
	var o paneOutcome
	var out, errOut strings.Builder
	fallback := func(_ context.Context, prompt string) error {
		o.fellBack, o.closedFirst = prompt, slices.Contains(panes.calls, "close w1:p2")
		fileFeature(t, repo)
		return nil
	}
	session := paneSession{herdr: panes, pane: "w1:p1", repo: repo, request: paneRequest, fallback: fallback,
		out: &out, err: &errOut}
	if width > 0 {
		session.progress = &tui.InterviewLine{Out: &out, Width: func() int { return width }}
		session.tracker = filedEpic{}
	}
	o.epic, o.code = featureInterview{
		request: paneRequest,
		repo:    repo,
		session: session.talk,
		tracker: filedEpic{},
		in:      strings.NewReader(answer),
		out:     &out,
		err:     &errOut,
	}.run(ctx)
	o.out, o.err = out.String(), errOut.String()
	return o
}

// paneLine is what orchestra's pane says while the interview runs in the pane beside it.
const paneLine = "Talking the feature through with claude in the pane on the right, which closes once the " +
	"feature is filed. Type /exit there to leave without filing; Ctrl+C here stops.\n"

// In a Herdr pane, the interview opens in a pane split off orchestra's, to its right, in the main
// checkout: claude starts there with the instructions and a first message, one line, that brings in
// the description, every line of which is in the file it names. orchestra's pane says where the
// interview is and waits through claude's turns. Once claude has filed the feature and left, the
// pane is closed and the plan and the question follow in orchestra's pane, as on the terminal.
func TestPaneInterviewFilesTheFeature(t *testing.T) {
	repo, _ := gitRepo(t)
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{
			agent: []dispatch.AgentState{dispatch.StateGone, dispatch.StateWorking, "", dispatch.StateBlocked,
				dispatch.StateIdle, dispatch.StateWorking, dispatch.StateIdle, dispatch.StateGone},
			step: func(read int) {
				if read == 7 { // its last turn
					fileFeature(t, repo)
				}
			},
		}
		o := interviewInPane(t.Context(), t, repo, panes, "y\n")
		if o.epic != "f-1" || o.code != dispatch.ExitOK || o.err != "" || o.fellBack != "" {
			t.Fatalf("got %q, exit %d, fell back %v; stderr:\n%s", o.epic, o.code, o.fellBack != "", o.err)
		}
		if want := []string{"split w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
			t.Errorf("Herdr was asked to %q, want %q", panes.calls, want)
		}
		prompt := filepath.Join(repo, project.RunPath("interview-prompt.md"))
		request := project.RunPath("feature-request.md")
		if want := []string{"--append-system-prompt-file", prompt, "--",
			"Here is the feature I'd like to talk through: @" + request}; !slices.Equal(panes.launched, want) {
			t.Errorf("claude started with %q, want %q", panes.launched, want)
		}
		if b, err := os.ReadFile(filepath.Join(repo, request)); err != nil {
			t.Error(err)
		} else if text, _ := unpasted(t, strings.TrimSuffix(string(b), "\n")); text != paneRequest {
			t.Errorf("%s holds %q", request, b)
		}
		if b, err := os.ReadFile(prompt); err != nil || !strings.Contains(string(b), "# Feature interview") {
			t.Errorf("the instructions at %s: %v", prompt, err)
		}
		if !strings.HasPrefix(o.out, paneLine) || strings.Contains(o.out, "Type /exit to come back.") {
			t.Errorf("orchestra's pane doesn't start with the pane's line:\n%s", o.out)
		}
		for _, want := range []string{"Epic: f-1 JSON output\n", "f-1.1  feature", "f-1.2  task", "after: f-1.1\n",
			"Start the run on f-1 (2 tickets)? [y/N] "} {
			if !strings.Contains(o.out, want) {
				t.Errorf("orchestra's pane lacks %q:\n%s", want, o.out)
			}
		}
	})
}

// An interview that files nothing ends as on the terminal, its pane closed once claude has left.
func TestPaneInterviewThatFilesNothing(t *testing.T) {
	repo, _ := gitRepo(t)
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateIdle, dispatch.StateWorking, dispatch.StateGone}}
		o := interviewInPane(t.Context(), t, repo, panes, "")
		if o.epic != "" || o.code != dispatch.ExitOK || o.err != "" || o.fellBack != "" {
			t.Fatalf("got %q, exit %d, fell back %v; stderr:\n%s", o.epic, o.code, o.fellBack != "", o.err)
		}
		if o.out != paneLine+noFeature+"\n" {
			t.Errorf("orchestra's pane:\n%s", o.out)
		}
		if !slices.Contains(panes.calls, "close w1:p2") {
			t.Errorf("the interview's pane wasn't closed: %q", panes.calls)
		}
	})
}

// The user can close the interview's pane rather than type /exit, even while claude starts: that
// ends the interview with what it filed, at once, without falling back to the terminal.
func TestPaneInterviewClosedByTheUser(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent dispatch.AgentState // in the pane until the user closes it
		filed bool
	}{
		{"while claude runs", dispatch.StateIdle, true},
		{"before claude appeared", dispatch.StateGone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := gitRepo(t)
			synctest.Test(t, func(t *testing.T) {
				var panes *fakePanes
				panes = &fakePanes{agent: []dispatch.AgentState{tc.agent}, step: func(read int) {
					if read == 3 {
						if tc.filed {
							fileFeature(t, repo)
						}
						panes.closed = true
					}
				}}
				started := time.Now()
				o := interviewInPane(t.Context(), t, repo, panes, "n\n")
				if o.code != dispatch.ExitOK || o.err != "" || o.fellBack != "" {
					t.Fatalf("exit %d, fell back %v; stderr:\n%s", o.code, o.fellBack != "", o.err)
				}
				if waited := time.Since(started); waited > 5*time.Second {
					t.Errorf("orchestra took %v to see the pane closed", waited)
				}
				want := noFeature
				if tc.filed {
					want = "Start it later with: orchestra --ticket f-1"
				}
				if !strings.Contains(o.out, want) {
					t.Errorf("orchestra's pane lacks %q:\n%s", want, o.out)
				}
			})
		})
	}
}

// Ctrl+C in orchestra's pane, or SIGTERM or SIGHUP, ends the interview's context while it waits,
// whether claude has appeared yet or not: the interview's pane is closed, which ends claude, and
// orchestra stops as on the terminal, naming the epic if one was filed.
func TestPaneInterviewStoppedWhileWaiting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent dispatch.AgentState
		filed bool
	}{
		{"while claude runs", dispatch.StateWorking, true},
		{"while claude starts", dispatch.StateGone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := gitRepo(t)
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				panes := &fakePanes{agent: []dispatch.AgentState{tc.agent}, step: func(read int) {
					if read == 3 {
						if tc.filed {
							fileFeature(t, repo)
						}
						stop()
					}
				}}
				o := interviewInPane(ctx, t, repo, panes, "y\n")
				if o.epic != "" || o.code != dispatch.ExitInterrupted || o.fellBack != "" {
					t.Fatalf("got %q, exit %d, fell back %v; stderr:\n%s", o.epic, o.code, o.fellBack != "", o.err)
				}
				want := "orchestra: stopped before the run started.\n"
				if tc.filed {
					want += "The interview filed f-1; start the run on it with: orchestra --ticket f-1\n"
				}
				if o.err != want {
					t.Errorf("stderr:\n%s\nwant:\n%s", o.err, want)
				}
				if last := panes.calls[len(panes.calls)-1]; last != "close w1:p2" {
					t.Errorf("the interview's pane wasn't closed: %q", panes.calls)
				}
				if strings.Contains(o.out, "Start the run") {
					t.Errorf("orchestra asked after the stop:\n%s", o.out)
				}
			})
		})
	}
}

// When the split fails, Herdr won't start claude in the new pane, or claude doesn't appear in it
// within a minute, orchestra closes any pane it made, says why in one line, and hands claude its
// terminal instead, with the same instructions; what that session files runs as before.
func TestPaneInterviewFallsBackToTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		panes     fakePanes
		why       string
		paneMade  bool
		minWaited time.Duration
	}{
		{"split refused", fakePanes{splitErr: errors.New("herdr pane split w1:p1: exit status 1: no such pane"),
			agent: []dispatch.AgentState{dispatch.StateGone}}, "herdr pane split w1:p1: exit status 1: no such pane",
			false, 0},
		{"launch refused", fakePanes{launchErr: errors.New("herdr pane run w1:p2: exit status 1"),
			agent: []dispatch.AgentState{dispatch.StateGone}}, "herdr pane run w1:p2: exit status 1", true, 0},
		{"claude never appears", fakePanes{agent: []dispatch.AgentState{dispatch.StateGone}},
			"claude didn't appear in pane w1:p2 within 1m0s", true, interviewStart},
		{"Herdr can't say", fakePanes{agent: []dispatch.AgentState{""}},
			"claude didn't appear in pane w1:p2 within 1m0s; Herdr could not say what it holds: herdr: server busy",
			true, interviewStart},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := gitRepo(t)
			synctest.Test(t, func(t *testing.T) {
				panes := tc.panes
				started := time.Now()
				o := interviewInPane(t.Context(), t, repo, &panes, "y\n")
				if o.epic != "f-1" || o.code != dispatch.ExitOK {
					t.Fatalf("got %q, exit %d; stderr:\n%s", o.epic, o.code, o.err)
				}
				if want := "orchestra couldn't open the interview in a pane beside its own, so claude takes this " +
					"terminal: " + tc.why + "\n"; o.err != want {
					t.Errorf("stderr:\n%s\nwant:\n%s", o.err, want)
				}
				if want := filepath.Join(repo, project.RunPath("interview-prompt.md")); o.fellBack != want {
					t.Errorf("the terminal session got the instructions at %q, want %q", o.fellBack, want)
				}
				if o.closedFirst != tc.paneMade || slices.Contains(panes.calls, "close w1:p2") != tc.paneMade {
					t.Errorf("Herdr was asked to %q; closed before the terminal session: %v", panes.calls, o.closedFirst)
				}
				if waited := time.Since(started); waited < tc.minWaited || waited > tc.minWaited+5*time.Second {
					t.Errorf("orchestra fell back after %v", waited)
				}
				if strings.Contains(o.out, paneLine) || !strings.Contains(o.out, "Start the run on f-1 (2 tickets)?") {
					t.Errorf("orchestra's pane:\n%s", o.out)
				}
			})
		})
	}
}
