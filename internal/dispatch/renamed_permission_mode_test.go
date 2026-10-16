package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// bothNamesHelp is a claude's --help that lists the mode by its old name and its new one.
const bothNamesHelp = `  --permission-mode <mode>  (choices: "acceptEdits", "auto", "default", "manual", "plan")
`

func TestListedPermissionMode(t *testing.T) {
	for _, tc := range []struct {
		help, mode, want string
	}{
		{claudeHelp, "default", "manual"}, // renamed since
		{claudeHelp, "manual", "manual"},
		{olderHelp, "manual", "default"}, // renamed from
		{olderHelp, "default", "default"},
		{bothNamesHelp, "default", "default"},
		{bothNamesHelp, "manual", "manual"},
		{claudeHelp, "acceptEdits", "acceptEdits"},
		{olderHelp, "auto", ""}, // not renamed
		{`  --permission-mode <mode>  (choices: "auto", "plan")`, "default", ""},
		{`  --permission-mode <mode>  (choices: "auto", "plan")`, "manual", ""},
		{"", "default", ""},
	} {
		if got := listedPermissionMode(tc.help, tc.mode); got != tc.want {
			t.Errorf("%s in %q: %q, want %q", tc.mode, tc.help, got, tc.want)
		}
	}
}

// default and manual are one mode, which Claude Code renamed: a run given either starts its workers
// in it by the name their claude lists, without a warning, the START line naming the mode as given.
func TestWorkersStartInARenamedPermissionMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		help, mode, passed string
	}{
		{claudeHelp, "default", "manual"},
		{olderHelp, "manual", "default"},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			help := &fakeHelp{help: tc.help}
			h.help, h.cfg.WorkerPermissionMode = help, tc.mode
			h.beads.add("A", "first", 1)
			h.beads.add("B", "second", 2)
			h.worker("A", finishes("a.txt"))
			h.worker("B", finishes("b.txt"))
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
				t.Fatalf("%s: exit %d, final %q\n%s", tc.mode, code, o.Final(), h.sink.text())
			}
			for _, args := range append(h.herdr.argsFor("A"), h.herdr.argsFor("B")...) {
				if !startsIn(args, tc.passed) {
					t.Errorf("%s: a worker started with %q, want --permission-mode %s", tc.mode, args, tc.passed)
				}
			}
			if ev := h.sink.text(); !strings.Contains(ev, ", permission mode: "+tc.mode+", worktrees: ") {
				t.Errorf("%s: the START line should name the mode as given:\n%s", tc.mode, ev)
			}
			if w := permissionWarnings(h); len(w) > 0 || help.asked != 1 {
				t.Errorf("%s: claude asked %d times, warnings %q; want once, none", tc.mode, help.asked, w)
			}
		})
	}
}
