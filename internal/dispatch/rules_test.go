package dispatch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// rulesFile returns the file args append to the worker's system prompt, or "" if they append none.
func rulesFile(args []string) string {
	i := slices.Index(args, "--append-system-prompt-file")
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

// runFiles records the rules and first message each worker found in its worktree when it started.
type runFiles struct{ rules, first map[string]string }

// then reads the worker's rules and first message, then does b.
func (f *runFiles) then(b behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		f.rules[w.id], f.first[w.id] = readRun(w.t, w.wt, "rules.md"), readRun(w.t, w.wt, "prompt.md")
		return b(w)
	}
}

// readRun returns the file name in .orchestra/run/ of worktree wt, or "" if there is none.
func readRun(t *testing.T, wt, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(wt, ".orchestra", "run", name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(raw)
}

// A Claude worker gets the worker prompt, and what the run adds for the whole ticket (its scope, its
// time limit), in its system prompt, from .orchestra/run/rules.md; its first message, from its
// prompt file, carries only its ticket and what an earlier attempt left, and points at the rules.
func TestClaudeWorkerGetsItsRulesInItsSystemPrompt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := scoped(newTimedHarness(t), "R")
		h.cfg.TicketLimit = 2 * time.Hour
		h.beads.add("R", "root", 1)
		h.beads.sub("R.1", "R", "child", 1)
		h.mem.commit("wt/R.1", "R.1: an earlier attempt", "left.txt")
		f := &runFiles{rules: map[string]string{}, first: map[string]string{}}
		h.worker("R.1", f.then(finishes("a.txt")))
		h.worker("R", finishes("r.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		wt := h.worktree("R.1")
		starts := h.herdr.argsFor("R.1")
		if len(starts) != 1 || starts[0][0] != "launch" {
			t.Fatalf("starts %q; want one launch", starts)
		}
		if got, want := rulesFile(starts[0]), filepath.Join(wt, ".orchestra", "run", "rules.md"); got != want {
			t.Errorf("rules file %q, want %q (args %q)", got, want, starts[0])
		}
		rules := f.rules["R.1"]
		for _, want := range []string{"Work on R.1.", "as a child of R", "You have about 2h for this ticket"} {
			if !strings.Contains(rules, want) {
				t.Errorf("the rules lack %q:\n%s", want, rules)
			}
		}
		if strings.Contains(rules, "An earlier attempt") {
			t.Errorf("the rules hold the earlier attempt's note, which is for the first message:\n%s", rules)
		}
		first := f.first["R.1"]
		if !strings.HasPrefix(first, "Your ticket is R.1. Your standing rules for it are in your system prompt "+
			"(a copy is in .orchestra/run/rules.md)") || !strings.Contains(first, "An earlier attempt at this ticket left work") {
			t.Errorf("first message:\n%s", first)
		}
		for _, rule := range []string{"Work on R.1.", "as a child of R", "You have about"} {
			if strings.Contains(first, rule) {
				t.Errorf("the first message repeats the rule %q:\n%s", rule, first)
			}
		}
		if got := h.herdr.pastedText("R.1"); len(got) != 0 {
			t.Errorf("pasted %q to a worker given its prompt at launch", got)
		}
	})
}

// Pasted rather than given at launch, a Claude worker's prompt is its first message alone: its
// rules are still in its system prompt.
func TestPastedClaudeWorkerPromptLeavesTheRulesToTheSystemPrompt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.LaunchPrompt = false
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 1 || starts[0][0] != "start" || rulesFile(starts[0]) == "" {
			t.Errorf("starts %q; want one start with its rules", starts)
		}
		pasted := h.herdr.pastedText("A")
		if len(pasted) != 1 || !strings.HasPrefix(pasted[0], "Your ticket is A.") || strings.Contains(pasted[0], "Work on A.") {
			t.Errorf("pasted %q; want the first message alone", pasted)
		}
	})
}

// A worker Herdr starts without its rules, refusing the arguments, gets them pasted with its prompt,
// as a worker of another kind always does: it then has the whole prompt as its first message.
func TestWorkerWithoutItsRulesArgumentsGetsTheWholePrompt(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"claude", "codex"} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.AgentKind = kind
			if kind == "claude" {
				h.herdr.launchFails["A"] = true
				h.herdr.refusePaths = true
			}
			h.beads.add("A", "first", 1)
			f := &runFiles{rules: map[string]string{}, first: map[string]string{}}
			h.worker("A", f.then(finishes("a.txt")))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("%s: exit %d, final %q\n%s", kind, code, o.Final(), h.sink.text())
			}
			starts := h.herdr.argsFor("A")
			if len(starts) == 0 || !equal(starts[len(starts)-1], []string{"start"}) {
				t.Errorf("%s: starts %q; want the last a plain start", kind, starts)
			}
			if kind != "claude" {
				for _, args := range starts {
					if rulesFile(args) != "" {
						t.Errorf("%s: a worker of another kind was given a system prompt file: %q", kind, args)
					}
				}
				if rules := f.rules["A"]; rules != "" {
					t.Errorf("%s: rules written for a worker of another kind:\n%s", kind, rules)
				}
			}
			if pasted := h.herdr.pastedText("A"); len(pasted) != 1 || !strings.HasPrefix(pasted[0], "Work on A.") {
				t.Errorf("%s: pasted %q; want the whole prompt", kind, pasted)
			}
		})
	}
}

// A worker whose session is resumed gets its rules again: Claude Code keeps no system prompt with
// the session.
func TestResumedWorkerGetsItsRulesAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes, finishes("a.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
		want := filepath.Join(h.worktree("A"), ".orchestra", "run", "rules.md")
		for _, args := range starts {
			if rulesFile(args) != want {
				t.Errorf("start %q; want its rules from %s", args, want)
			}
		}
	})
}
