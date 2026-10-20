package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// The evidence tells a removal the safety check held from a prompt an allow rule would spare, and
// says that no allow rule skips the check.
func TestHeldRemovalEvidence(t *testing.T) {
	t.Parallel()
	rec := HookRecord{Permissions: []Permission{
		{Tool: "Bash", Command: "ffprobe -v error in.mp4"},
		{Tool: "Bash", Command: "S=/tmp/s; cd $S && rm -f $S/*.mp4", Removal: "rm -f $S/*.mp4"},
	}}
	want := "It waited on a permission prompt twice, Claude Code asking to allow: Bash (ffprobe -v error in.mp4); " +
		"1 of them a removal held by Claude Code's built-in safety check, its target a variable or a glob " +
		"(Bash (rm -f $S/*.mp4)), which only a person can approve and no allow rule skips: the worker prompt " +
		"should keep workers from writing such removals."
	if got := recordEvidence(rec); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	line := hooksLine("A", rec.Permissions[1:], nil)
	if !strings.HasPrefix(line, "A: its workers waited on 1 permission prompt (a removal held by Claude "+
		"Code's built-in safety check") || strings.Contains(line, "asking to allow") {
		t.Errorf("a held removal alone is no prompt to allow: %q", line)
	}
}

// The log names the removal a worker's prompt holds, which the command's first 80 characters may not
// show, and says no allow rule skips it.
func TestPermissionPromptLogNamesTheHeldRemoval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		w := h.loop().newWatcher(h.worktree("A"), Status{Ticket: "A", Tab: "tab1"})
		cmd := "S=/private/tmp/claude-501/" + strings.Repeat("x", 80) + "/scratchpad; cd $S && rm -f $S/*.mp4"
		w.notePermission(ToolUse{Event: agentio.EventPermission, Tool: "Bash", Command: cmd})
		want := "PERMISSION_PROMPT: A's worker waits on a permission prompt for Bash (rm -f $S/*.mp4), a removal " +
			"Claude Code's built-in safety check holds for a person, which no allow rule skips, in tab tab1"
		if got := strings.Join(h.sink.of(EvWarn), "\n"); !strings.Contains(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
}
