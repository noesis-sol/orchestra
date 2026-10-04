package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// eraseLine is what orchestra writes before each progress line it draws over the last, and to clear
// the last one.
const eraseLine = "\r" + ansi.EraseLineRight

// progressLines splits what orchestra's pane showed into what came before the progress lines, the
// lines drawn in turn, without their styles, and what came after the last was cleared.
func progressLines(t *testing.T, out string) (before string, lines []string, after string) {
	t.Helper()
	parts := strings.Split(out, eraseLine)
	if len(parts) < 2 {
		t.Fatalf("orchestra's pane showed no progress line:\n%q", out)
	}
	for _, l := range parts[1 : len(parts)-1] {
		lines = append(lines, ansi.Strip(l))
	}
	return parts[0], lines, parts[len(parts)-1]
}

// Under its fixed line, orchestra's pane says what claude is doing and how long the interview has
// run, updated in place on each reading of Herdr's that the wait takes anyway: a reading that fails
// keeps the state shown. Once claude has filed the feature the line names the epic, then, once bd
// has given them, its title and tickets, until the interview's end. The line is gone before the
// plan and the question, which look as they do without it.
func TestPaneInterviewShowsItsProgress(t *testing.T) {
	agent := []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking, dispatch.StateBlocked, "",
		dispatch.StateIdle, dispatch.StateDone, dispatch.StateWorking, dispatch.StateWorking, dispatch.StateDone}
	interview := func(width int) (paneOutcome, int) {
		repo, _ := gitRepo(t)
		var o paneOutcome
		var panes *fakePanes
		synctest.Test(t, func(t *testing.T) {
			panes = &fakePanes{agent: agent, step: func(read int) {
				if read == 6 { // seen at the next reading: the file is looked at before claude's state
					fileFeature(t, repo)
				}
			}}
			o = interviewInPaneOn(t.Context(), t, repo, panes, "y\n", width)
		})
		return o, panes.reads
	}
	o, reads := interview(80)
	if o.epic != "f-1" || o.code != dispatch.ExitOK || o.err != "" {
		t.Fatalf("got %q, exit %d; stderr:\n%s", o.epic, o.code, o.err)
	}
	before, lines, after := progressLines(t, o.out)
	if before != paneLine {
		t.Errorf("before the progress line, orchestra's pane showed:\n%q", before)
	}
	if want := []string{
		"claude is working  1s",
		"claude is waiting for a permission answer  2s",
		"claude is waiting for a permission answer  3s",
		"claude is waiting for you  4s",
		"claude is waiting for you  5s",
		"Filed f-1",
		"Filed f-1: JSON output (2 tickets)",
	}; !slices.Equal(lines, want) {
		t.Errorf("orchestra's pane showed\n%q\nwant\n%q", lines, want)
	}
	plain, plainReads := interview(0)
	if paneLine+after != plain.out {
		t.Errorf("after the progress line, orchestra's pane showed\n%q\nwant, as without it,\n%q", after,
			strings.TrimPrefix(plain.out, paneLine))
	}
	if reads != plainReads || reads != len(agent) {
		t.Errorf("Herdr was read %d times, %d without the progress line, want %d", reads, plainReads, len(agent))
	}
}

// The line is cleared however the interview ends: claude leaving with nothing filed, a stop, or an
// epic bd can't read, which the line names alone.
func TestPaneInterviewProgressEnds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent []dispatch.AgentState
		step  func(t *testing.T, repo string, read int, stop func())
		lines []string
		code  int
		after string
	}{
		{"claude leaves", []dispatch.AgentState{dispatch.StateIdle, dispatch.StateWorking, dispatch.StateIdle,
			dispatch.StateGone}, nil, []string{"claude is working  1s", "claude is waiting for you  2s"},
			dispatch.ExitOK, noFeature + "\n"},
		{"stopped", []dispatch.AgentState{dispatch.StateWorking},
			func(t *testing.T, repo string, read int, stop func()) {
				if read == 3 {
					fileFeature(t, repo)
					stop()
				}
			}, []string{"claude is working  1s", "claude is working  2s"}, dispatch.ExitInterrupted, ""},
		{"bd doesn't know the epic", []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking,
			dispatch.StateWorking, dispatch.StateWorking, dispatch.StateWorking, dispatch.StateIdle},
			func(t *testing.T, repo string, read int, _ func()) {
				if read == 3 {
					if err := os.WriteFile(filepath.Join(repo, project.RunPath(project.FeatureName)),
						[]byte(`{"epic":"f-2"}`+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}, []string{"claude is working  1s", "claude is working  2s", "Filed f-2", "Filed f-2"}, dispatch.ExitOK,
			noFeature + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := gitRepo(t)
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				panes := &fakePanes{agent: tc.agent, step: func(read int) {
					if tc.step != nil {
						tc.step(t, repo, read, stop)
					}
				}}
				o := interviewInPaneOn(ctx, t, repo, panes, "", 60)
				if o.code != tc.code {
					t.Fatalf("exit %d; stderr:\n%s", o.code, o.err)
				}
				before, lines, after := progressLines(t, o.out)
				if before != paneLine || !slices.Equal(lines, tc.lines) || after != tc.after {
					t.Errorf("orchestra's pane showed %q, then\n%q\nthen %q; want the lines\n%q\nthen %q", before,
						lines, after, tc.lines, tc.after)
				}
			})
		})
	}
}
