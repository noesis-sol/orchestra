package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A setup Ctrl+C cut short in a worktree runs again when the next run takes that worktree up, though
// no file it reads changed (orchestra-pwnf): its dependencies may be half installed, or missing, as npm
// ci removes node_modules before it installs them.

// Ctrl+C as the setup runs in A's new worktree leaves its mark there. The next run, reusing the
// worktree with nothing the setup reads changed, runs it again before A's worker starts, and the
// worker finds the mark gone.
func TestASetupCutShortRunsAgainInTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, log := recordsSetups(t, "")
		h.cfg.Setup, h.cfg.SetupFiles = cmd, project.DefaultSetupFiles
		h.beads.add("A", "first", 1)
		wt := h.worktree("A")
		mark := filepath.Join(wt, ".orchestra", "run", setupMarkName)
		var marked bool
		h.worker("A", func(w *fakeWorker) AgentState {
			marked = exists(mark)
			return finishes("a.txt")(w)
		})

		o := h.loop()
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.SetSink(cutSink{runSink: h.sink, on: func(ev Event) {
			if strings.Contains(ev.Text, "A: running '"+cmd+"' in its new worktree") {
				cancel(InterruptedError("with Ctrl+C"))
			}
		}})
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !exists(mark) {
			t.Fatalf("run 1 left no %s in A's worktree", setupMarkName)
		}
		if n := len(h.herdr.argsFor("A")); n != 0 {
			t.Fatalf("A's worker started %d times in run 1", n)
		}

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := setUpIn(t, log); !equal(got, []string{"A"}) {
			t.Errorf("the setup ran in %v, want A's worktree in run 2\n%s", got, h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"reusing worktree " + wt,
			"  A: the last setup in its worktree didn't finish; running '" + cmd + "' there again before its " +
				"worker starts\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if marked {
			t.Errorf("A's worker found %s, after the setup worked", mark)
		}
	})
}

// A worktree an earlier run left is set up again, with nothing the setup reads changed, only when the
// last setup there didn't finish: one that worked removed its mark.
func TestAReusedWorktreeIsSetUpWhenItsLastSetupDidntFinish(t *testing.T) {
	t.Parallel()
	for _, cut := range []bool{true, false} {
		t.Run(map[bool]string{true: "cut short", false: "finished"}[cut], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				cmd, log := recordsSetups(t, "")
				h.cfg.Setup, h.cfg.SetupFiles = cmd, project.DefaultSetupFiles
				wt := h.worktree("A")
				if _, err := h.mem.NewWorktree(t.Context(), h.repo, wt, "wt/A", "main"); err != nil {
					t.Fatal(err)
				}
				if err := h.mem.commitIn(wt, "A: an earlier attempt", "left.txt"); err != nil {
					t.Fatal(err)
				}
				if cut {
					mark := filepath.Join(wt, ".orchestra", "run", setupMarkName)
					if err := os.MkdirAll(filepath.Dir(mark), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(mark, nil, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				h.mem.commit("main", "meanwhile", "README.md")
				h.beads.add("A", "first", 1)
				h.worker("A", finishes("a.txt"))
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				var want []string
				if cut {
					want = []string{"A"}
				}
				if got := setUpIn(t, log); !equal(got, want) {
					t.Errorf("the setup ran in %v, want %v\n%s", got, want, h.sink.text())
				}
				said := "  A: the last setup in its worktree didn't finish; running '" + cmd + "' there again"
				if ev := h.sink.text(); strings.Contains(ev, said) != cut {
					t.Errorf("events should say %q only when the last setup was cut short:\n%s", said, ev)
				}
			})
		})
	}
}
