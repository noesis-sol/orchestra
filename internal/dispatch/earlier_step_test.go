package dispatch

import "testing"

// A returning ticket's earlier worker is adopted, told to carry on, renamed or left, or stops the
// run, by its state and whether it was left by a question asked in this run.
func TestEarlierStep(t *testing.T) {
	cases := []struct {
		st    AgentState
		asked bool
		want  earlierAction
	}{
		{StateGone, false, leaveEarlier},
		{StateGone, true, leaveEarlier},
		{StateWorking, false, stopForEarlier},
		{StateBlocked, false, stopForEarlier},
		{StateUnknown, false, stopForEarlier},
		{StateWorking, true, adoptEarlier},
		{StateBlocked, true, adoptEarlier},
		{StateUnknown, true, adoptEarlier},
		{StateIdle, true, tellEarlier},
		{StateDone, true, tellEarlier},
		{StateIdle, false, closeEarlier},
		{StateDone, false, closeEarlier},
		{StateLimited, false, adoptEarlier},
		{StateLimited, true, adoptEarlier},
		{AgentState("other"), true, renameEarlier},
	}
	for _, c := range cases {
		if got := earlierStep(c.st, c.asked); got != c.want {
			t.Errorf("earlierStep(%s, %v) = %d, want %d", c.st, c.asked, got, c.want)
		}
	}
}

func TestStillIn(t *testing.T) {
	cases := []struct {
		st   AgentState
		want string
	}{
		{StateWorking, "working in tab 3"},
		{StateBlocked, "blocked in tab 3"},
		{StateUnknown, "in tab 3, though the terminal can't tell what it is doing"},
	}
	for _, c := range cases {
		if got := stillIn(c.st, "tab 3"); got != c.want {
			t.Errorf("stillIn(%s) = %q, want %q", c.st, got, c.want)
		}
	}
}
