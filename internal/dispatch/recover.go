package dispatch

import (
	"fmt"
	"runtime/debug"
	"sync"
)

// Panics. Each ticket runs in its own goroutine, and a panic in one (a nil map, an index out of
// range) would end the process: the other workers would go on in their tabs unsupervised, with no
// INTERRUPTED line, no report, and perhaps the terminal left in raw mode. So the long-lived
// goroutines recover. A worker's panic stops the run like any other reason: it holds, the running
// tickets finish, and the run ends with it. A panic in triage, the predictor or a status watcher
// loses only that. Run's own goroutine doesn't recover: a panic there still ends orchestra.

// logPanic writes panic p in what, with its stack, to the log, and returns p on one line. Call it
// from the deferred function that recovered p, so the stack still shows where it panicked.
func (o *Loop) logPanic(what string, p any) string {
	o.log.Raw(fmt.Sprintf("panic in %s: %v\n\n%s", what, p, debug.Stack()), nil)
	return FirstLine(fmt.Sprint(p))
}

// panicStop is the reason to stop the run when the worker on ticket id panicked with p. The ticket
// is left as it was: still active, its worktree and tab as they are.
func (o *Loop) panicStop(id string, p any) *stopReason {
	text := o.logPanic(id, p)
	left := "its worktree is left for review"
	o.mu.Lock()
	st, ok := o.active[id]
	o.mu.Unlock()
	if ok {
		left = fmt.Sprintf("its worktree and tab %s are left for review", st.Tab)
	}
	s := halt(ExitTool, stopPanic, " in %s: %s; %s (the stack is in %s)", id, text, left, o.cfg.LogPath)
	if err, ok := p.(error); ok {
		s = s.causedBy(err) // a runtime.Error, say
	}
	return s
}

// held is a mutex taken and let go of along the way, as merging does, released by a deferred
// release however the function ends: a worker that panics while holding it must not leave the
// other workers waiting for it forever, nor unlock it twice, which Go can't recover from.
type held struct {
	mu *sync.Mutex
	on bool
}

func (h *held) lock()   { h.mu.Lock(); h.on = true }
func (h *held) unlock() { h.on = false; h.mu.Unlock() }

// release lets go of the mutex if it is held.
func (h *held) release() {
	if h.on {
		h.unlock()
	}
}
