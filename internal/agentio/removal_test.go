package agentio

import (
	"strings"
	"testing"
)

// The removals Claude Code's safety check holds for a person are those whose target it can't resolve
// (a variable, a glob) or a directory it guards, wherever the rm is in the command; one with literal
// paths, and rm as another program's argument, are not.
func TestGuardedRemoval(t *testing.T) {
	t.Parallel()
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
