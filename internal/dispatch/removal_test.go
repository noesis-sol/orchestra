package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// The removals Claude Code's safety check holds for a person are those whose target it can't resolve
// (a variable, a glob) or a directory it guards, wherever the rm is in the command; one with literal
// paths, and rm as another program's argument, are not.
func TestGuardedRemoval(t *testing.T) {
	scratch := "/private/tmp/claude-501/-Users-me-Projects-fideo-worktrees-fideo-lgl/scratchpad"
	for _, c := range []struct{ cmd, want string }{
		// The three removals that stalled workers in a run on fideo.
		{"S=" + scratch + "; cd $S && for f in a b; do ffmpeg -i $f.mov -y $f.mp4; done; rm -f $S/*.mp4",
			"rm -f $S/*.mp4"},
		{"S=" + scratch + "; cd $S && bash -c 'for ext in mp4 mov; do f=$S/scr.$ext; rm -f $f; ffmpeg -y $f; done'",
			"rm -f $f"},
		{"cd $TMPDIR && mkdir -p fq && cd fq && rm -f * && ffmpeg -hide_banner -loglevel error -i in.mov out.mp4",
			"rm -f *"},
		{`rm -rf "$BUILD_DIR"`, `rm -rf "$BUILD_DIR"`},
		{"rm -rf $(git rev-parse --show-toplevel)/out", "rm -rf $(git rev-parse --show-toplevel"},
		{"find . -name '*.o' | xargs rm -f", ""},
		{"ls out/*.log | xargs rm -f out/*.log", "rm -f out/*.log"},
		{"if true; then rm -rf ~; fi", "rm -rf ~"},
		{"rm -rf /", "rm -rf /"},
		{"rm -r .", "rm -r ."},

		{"rm -rf build", ""},
		{"rm -f /tmp/x/out.mp4 && ffmpeg -y -i a.mov /tmp/x/out.mp4", ""},
		{"git rm --cached $f", ""},
		{"echo rm $f", ""},
		{"bash -c 'rm -f /tmp/x/a.txt '", ""},
		{"rmdir $d", ""},
		{"go test ./...", ""},
		{"", ""},
	} {
		if got := GuardedRemoval(c.cmd); got != c.want {
			t.Errorf("GuardedRemoval(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
	if got := GuardedRemoval("rm -f $S/" + strings.Repeat("x", 100)); len([]rune(got)) != 81 ||
		!strings.HasSuffix(got, "…") {
		t.Errorf("a long removal should be cut to 80 characters and marked: %q", got)
	}
}

// The evidence tells a removal the safety check held from a prompt an allow rule would spare, and
// says that no allow rule skips the check.
func TestHeldRemovalEvidence(t *testing.T) {
	rec := HookRecord{Permissions: []Permission{
		{Tool: "Bash", Command: "ffprobe -v error in.mp4"},
		{Tool: "Bash", Command: "S=/tmp/s; cd $S && rm -f $S/*.mp4", Removal: "rm -f $S/*.mp4"},
	}}
	want := "It waited on a permission prompt twice, Claude Code asking to allow: Bash (ffprobe -v error in.mp4); " +
		"1 of them a removal held by Claude Code's built-in safety check, its target a variable or a glob " +
		"(Bash (rm -f $S/*.mp4)), which only a person can approve and no allow rule skips: the worker prompt " +
		"should keep workers from writing such removals."
	if got := rec.Evidence(); got != want {
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
		w.notePermission(ToolUse{Event: EventPermission, Tool: "Bash", Command: cmd})
		want := "PERMISSION_PROMPT: A's worker waits on a permission prompt for Bash (rm -f $S/*.mp4), a removal " +
			"Claude Code's built-in safety check holds for a person, which no allow rule skips, in tab tab1"
		if got := strings.Join(h.sink.of(EvWarn), "\n"); !strings.Contains(got, want) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
}
