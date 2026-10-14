package dispatch

import "testing"

// A merge passes over the ticket's branch until a pass is done with it, giving up once maxMergePasses
// passes have rebased onto a Base that kept moving; a pass after its worker worked on the branch, the
// queue let go of meanwhile, doesn't count.
func TestMergePasses(t *testing.T) {
	cases := []struct {
		name   string
		steps  []mergeStep // what each pass says, in turn
		stop   bool        // the last pass stops the run
		passes int         // how many passes run
		done   bool        // false: Base kept changing
	}{
		{name: "merged at once", steps: []mergeStep{mergeDone}, passes: 1, done: true},
		{name: "stopped at once", steps: []mergeStep{mergeDone}, stop: true, passes: 1, done: true},
		{name: "rebased, then merged", steps: []mergeStep{mergeAgain, mergeDone}, passes: 2, done: true},
		{name: "merged on the last pass", steps: []mergeStep{mergeAgain, mergeAgain, mergeDone}, passes: 3, done: true},
		{name: "Base kept changing", steps: []mergeStep{mergeAgain, mergeAgain, mergeAgain, mergeDone}, passes: 3},
		{name: "after its worker, not counted",
			steps:  []mergeStep{mergeAgainAfterWorker, mergeAgain, mergeAgainAfterWorker, mergeAgain, mergeDone},
			passes: 5, done: true},
		{name: "after its worker, then Base kept changing",
			steps:  []mergeStep{mergeAgainAfterWorker, mergeAgain, mergeAgain, mergeAgain, mergeDone},
			passes: 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			passes := 0
			stop, done := mergePasses(func() (mergeStep, *stopReason) {
				step := c.steps[passes]
				passes++
				if c.stop && passes == len(c.steps) {
					return step, errInterrupted
				}
				return step, nil
			})
			if passes != c.passes || done != c.done {
				t.Errorf("%d passes, done %t; want %d, %t", passes, done, c.passes, c.done)
			}
			if want := c.stop && c.done; (stop != nil) != want {
				t.Errorf("stop %v; want one: %t", stop, want)
			}
		})
	}
}
