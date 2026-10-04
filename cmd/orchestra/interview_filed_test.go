package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Once claude has filed the feature, its feature.json naming the epic, and its turn is over, idle
// or done as Herdr reads it after a turn, orchestra closes the interview's pane by itself, which
// ends claude, and shows the plan and the question: nobody types /exit. claude writes the file mid-
// turn, while it still works, waits on a permission answer and goes on to tell the user the feature
// is filed: the pane stays open through all that. A turn's end Herdr reports as the file appears
// may be the turn before's, read late: the pane is closed only on a reading taken after the file
// was there.
func TestPaneInterviewEndsOnceTheFeatureIsFiled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agent  []dispatch.AgentState // in the pane until orchestra closes it, the last one for every read after
		filed  int                   // the read at whose start claude writes feature.json
		closed int                   // the read after which orchestra closes the pane
	}{
		{"then claude is done", []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle, dispatch.StateWorking,
			dispatch.StateWorking, dispatch.StateWorking, dispatch.StateBlocked, dispatch.StateWorking, dispatch.StateDone},
			4, 8},
		{"then claude is idle", []dispatch.AgentState{dispatch.StateWorking, dispatch.StateDone, dispatch.StateWorking,
			dispatch.StateWorking, "", dispatch.StateWorking, dispatch.StateIdle}, 3, 7},
		{"as Herdr reads the turn before's end", []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle,
			dispatch.StateWorking, dispatch.StateIdle}, 4, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := gitRepo(t)
			synctest.Test(t, func(t *testing.T) {
				var panes *fakePanes
				panes = &fakePanes{agent: tc.agent, step: func(read int) {
					if read == tc.filed {
						fileFeature(t, repo)
					}
					if read > tc.closed { // claude never leaves: only orchestra ends this interview
						t.Fatalf("orchestra still waits for the interview at read %d; Herdr was asked to %q", read, panes.calls)
					}
				}}
				o := interviewInPane(t.Context(), t, repo, panes, "y\n")
				if o.epic != "f-1" || o.code != dispatch.ExitOK || o.err != "" || o.fellBack != "" {
					t.Fatalf("got %q, exit %d, fell back %v; stderr:\n%s", o.epic, o.code, o.fellBack != "", o.err)
				}
				if panes.reads != tc.closed {
					t.Errorf("orchestra closed the pane after read %d, want after read %d", panes.reads, tc.closed)
				}
				if want := []string{"split w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
					t.Errorf("Herdr was asked to %q, want %q", panes.calls, want)
				}
				if !strings.HasPrefix(o.out, paneLine) {
					t.Errorf("orchestra's pane doesn't start with the pane's line:\n%s", o.out)
				}
				for _, want := range []string{"Epic: f-1 JSON output\n", "Start the run on f-1 (2 tickets)? [y/N] "} {
					if !strings.Contains(o.out, want) {
						t.Errorf("orchestra's pane lacks %q:\n%s", want, o.out)
					}
				}
			})
		})
	}
}

// An interview that files nothing ends only when claude leaves the pane (/exit), however many turns
// end before: a feature.json that names no epic files nothing either. The pane is closed then.
func TestPaneInterviewWithoutAFeatureWaitsForClaudeToLeave(t *testing.T) {
	for _, tc := range []struct {
		name, file, err string
	}{
		{"no feature.json", "", ""},
		{"one that names no epic", "{}\n", "orchestra can't read the feature the interview filed: " +
			project.RunPath(project.FeatureName) + " names no epic\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := gitRepo(t)
			synctest.Test(t, func(t *testing.T) {
				var panes *fakePanes
				panes = &fakePanes{
					agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle, dispatch.StateWorking,
						dispatch.StateDone, dispatch.StateIdle, dispatch.StateWorking, dispatch.StateDone, dispatch.StateGone},
					step: func(read int) {
						if read == 3 && tc.file != "" {
							if err := os.WriteFile(filepath.Join(repo, project.RunPath(project.FeatureName)),
								[]byte(tc.file), 0o644); err != nil {
								t.Fatal(err)
							}
						}
						if slices.Contains(panes.calls, "close w1:p2") {
							t.Errorf("orchestra closed the pane before claude left it (read %d)", read)
						}
					},
				}
				o := interviewInPane(t.Context(), t, repo, panes, "")
				if o.epic != "" || o.code != dispatch.ExitOK || o.err != tc.err || o.fellBack != "" {
					t.Fatalf("got %q, exit %d, fell back %v; stderr:\n%s", o.epic, o.code, o.fellBack != "", o.err)
				}
				if panes.reads != 8 {
					t.Errorf("orchestra stopped reading Herdr after read %d, want 8, where claude has left", panes.reads)
				}
				if want := []string{"split w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
					t.Errorf("Herdr was asked to %q, want %q", panes.calls, want)
				}
				if o.out != paneLine+noFeature+"\n" {
					t.Errorf("orchestra's pane:\n%s", o.out)
				}
			})
		})
	}
}
