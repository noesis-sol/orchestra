package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The worker prompt a start's fast-forward of Base brings in is the run's (orchestra-eur4): no ticket
// has started, and every worktree is made from the new commit, so its workers follow the prompt
// committed there rather than the one the run read as it opened.

// pushPrompt commits text as the worker prompt in the clone other and pushes it to the remote main
// follows.
func pushPrompt(h *harness, other, text string) {
	h.t.Helper()
	file := filepath.Join(other, ".orchestra", "worker-prompt.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.git(other, "add", "-f", ".orchestra/worker-prompt.md") // the harness's repository ignores .orchestra/
	h.git(other, "commit", "-q", "-m", "Change the worker prompt")
	h.git(other, "push", "-q", "origin", "main")
}

// A fast-forward to a commit that changes the worker prompt has the run's first worker get the new
// one, and says so as the run starts.
func TestTheStartsFastForwardBringsInTheWorkerPrompt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.PromptFile = filepath.Join(h.repo, ".orchestra", "worker-prompt.md")
	other := withRemote(h)
	pushPrompt(h, other, "Work on TICKET_ID, as upstream now says.")

	h.beads.add("A", "told by the new prompt", 1)
	f := &runFiles{rules: map[string]string{}, first: map[string]string{}}
	h.worker("A", f.then(finishes("a.txt")))
	runFetching(h)
	if rules := f.rules["A"]; !strings.HasPrefix(rules, "Work on A, as upstream now says.") {
		t.Errorf("A's rules don't start with the prompt the fast-forward brought in:\n%s", rules)
	}
	want := "  PROMPT: the fast-forward to origin/main changed .orchestra/worker-prompt.md on main: the run's " +
		"workers get it as committed\n"
	if ev := h.sink.text(); !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
}

// A worker prompt the fast-forward leaves without its TICKET_ID placeholder is warned of, and the run
// keeps the one it was given.
func TestTheStartsFastForwardKeepsAPromptWithoutItsPlaceholder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.PromptFile = filepath.Join(h.repo, ".orchestra", "worker-prompt.md")
	other := withRemote(h)
	pushPrompt(h, other, "Work on the ticket.")

	o := runFetching(h)
	if o.prompt != "Work on TICKET_ID." {
		t.Errorf("the prompt is %q, want the one the run was given", o.prompt)
	}
	want := "  PROMPT_UNREAD: the fast-forward to origin/main changed .orchestra/worker-prompt.md on main, but it " +
		"could not be read: it has no TICKET_ID placeholder; the run's workers get the worker prompt it read before"
	if ev := h.sink.text(); !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
}

// A worker prompt outside the repository, as --prompt may give, is the run's whatever the fast-forward
// changes.
func TestTheStartsFastForwardLeavesAPromptOutsideTheRepository(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.PromptFile = filepath.Join(t.TempDir(), "worker-prompt.md")
	other := withRemote(h)
	pushPrompt(h, other, "Work on TICKET_ID, as upstream now says.")

	if o := runFetching(h); o.prompt != "Work on TICKET_ID." {
		t.Errorf("the prompt is %q, want the one the run was given", o.prompt)
	}
	if ev := h.sink.text(); strings.Contains(ev, "PROMPT") {
		t.Errorf("events say of the prompt:\n%s", ev)
	}
}
