package dispatch

import "testing"

// A merge passes over the ticket's branch until a pass is done with it; after maxMergePasses passes
// have rebased onto a Base that kept moving, one more may merge but not rebase, and gives up when Base
// moved again. A pass after its worker worked on the branch, the queue let go of meanwhile, doesn't
// count.
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
		{name: "merged after the last rebase", steps: []mergeStep{mergeAgain, mergeAgain, mergeAgain, mergeDone},
			passes: 4, done: true},
		{name: "Base kept changing", steps: []mergeStep{mergeAgain, mergeAgain, mergeAgain, mergeBaseMoved}, passes: 4},
		{name: "after its worker, not counted",
			steps:  []mergeStep{mergeAgainAfterWorker, mergeAgain, mergeAgainAfterWorker, mergeAgain, mergeDone},
			passes: 5, done: true},
		{name: "after its worker, then Base kept changing",
			steps:  []mergeStep{mergeAgainAfterWorker, mergeAgain, mergeAgain, mergeAgain, mergeBaseMoved},
			passes: 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			passes := 0
			stop, done := mergePasses(func(rebase bool) (mergeStep, *stopReason) {
				step := c.steps[passes]
				if !rebase && step != mergeDone && step != mergeBaseMoved {
					t.Fatalf("pass %d may not rebase, but says %d", passes+1, step)
				}
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
