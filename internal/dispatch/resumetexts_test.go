package dispatch

import "testing"

// What is noted, logged and told as a worker has its session resumed depends on why, and a question
// it was left waiting on is said answered either way.
func TestResumeTexts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		why      resumeWhy
		question string
		want     resumeTexts
	}{
		{"gone mid-ticket", goneMidTicket, "", resumeTexts{
			note: "Orchestra: the worker in tab w1 is gone, with the ticket still in_progress (worktree /wt/A); " +
				"its session s1 is resumed in a new tab.",
			text: "  RESUMED: A's worker is gone from tab w1 with the ticket in progress; " +
				"resuming its session s1 in a new tab (worktree /wt/A)",
			msg: "Orchestra: your worker stopped, its tab gone, with A still in progress, and this is your session " +
				"resumed. Carry on with A where you left off, as your instructions say.",
		}},
		{"gone mid-ticket, its question answered", goneMidTicket, "Q", resumeTexts{
			note: "Orchestra: the worker in tab w1 is gone, with the ticket still in_progress (worktree /wt/A); " +
				"its session s1 is resumed in a new tab.",
			text: "  RESUMED: A's worker is gone from tab w1 with the ticket in progress; " +
				"resuming its session s1 in a new tab (worktree /wt/A)",
			msg: "Orchestra: your worker stopped, its tab gone, with A still in progress, and this is your session " +
				"resumed. Carry on with A where you left off, as your instructions say. " +
				"Your question Q is answered: read the answer with bd show Q.",
		}},
		{"back from a question", backFromQuestion, "Q", resumeTexts{
			note: "Orchestra: the ticket is back, and its worker in tab w1 is gone (worktree /wt/A); " +
				"its session s1 is resumed in a new tab.",
			text: "  RESUMED: A is back, and its worker is gone from tab w1; " +
				"resuming its session s1 in a new tab (worktree /wt/A)",
			msg: "Orchestra: your worker stopped, its tab gone, and this is your session resumed: A is yours again. " +
				"Your question Q is answered: read the answer with bd show Q. " +
				"Claim A again with bd update A --claim, and carry on with it where you left off, as your instructions say.",
		}},
		{"back, no question", backFromQuestion, "", resumeTexts{
			note: "Orchestra: the ticket is back, and its worker in tab w1 is gone (worktree /wt/A); " +
				"its session s1 is resumed in a new tab.",
			text: "  RESUMED: A is back, and its worker is gone from tab w1; " +
				"resuming its session s1 in a new tab (worktree /wt/A)",
			msg: "Orchestra: your worker stopped, its tab gone, and this is your session resumed: A is yours again. " +
				"Claim A again with bd update A --claim, and carry on with it where you left off, as your instructions say.",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.why.texts("A", "w1", "/wt/A", tc.question, "s1")
			if got.note != tc.want.note {
				t.Errorf("note\n got %q\nwant %q", got.note, tc.want.note)
			}
			if got.text != tc.want.text {
				t.Errorf("text\n got %q\nwant %q", got.text, tc.want.text)
			}
			if got.msg != tc.want.msg {
				t.Errorf("msg\n got %q\nwant %q", got.msg, tc.want.msg)
			}
		})
	}
}
