package dispatch

// Once nothing runs, Run ends, unless something would give it work again: the rest of a scope being
// filed, the usage limit's reset, the probe of the machine once the run holds for the environment, the
// maintainer's answers. Its idle step (runState.idle) takes the first of these that applies
// (idleWaits) and waits for it alone, on its own channels, under the exit rule all of them share
// (idleWake): Ctrl+C, a request to wind down or to take tickets again, a new reason to stop or a new
// hold, or triage holding the run for the environment ends the wait, and Run goes round to take that
// up, starting what it may, or taking the next wait that applies, or ending.

// idleWaits are what Run waits for once nothing runs, in the order it looks at them (see
// runState.idle). Each reports whether it applies and, if it does, waits as long as it should, the
// gate standing as from when the step began (see idleWake), and reports whether Run goes round.
var idleWaits = [...]func(r *runState, from gateMark) (applies, goOn bool){
	(*runState).awaitFiling,
	(*runState).waitOutLimit,
	(*runState).awaitProbe,
	(*runState).awaitAnswers,
}

// idle is Run's step once nothing runs, and reports whether Run goes round rather than end: asked
// tickets were adopted (see follow), or the first of idleWaits that applies says so. When none
// applies, the run ends.
func (r *runState) idle() bool {
	if r.ctx.Err() != nil {
		return false
	}
	// Before the run waits or ends, the asked tickets are read once more.
	if r.follow() {
		return true
	}
	from := r.o.gate.mark()
	for _, wait := range idleWaits {
		if applies, goOn := wait(r, from); applies {
			return goOn && r.ctx.Err() == nil
		}
	}
	return false
}

// idleWake waits once as wait does, for one of idleWaits on w's channels, and says what woke it, and
// whether the wait is over by the rule they all keep to: the run's context ended, the gate no longer
// stands as from (a request to wind down or to take tickets again, a new reason to stop, a hold begun
// or lifted), or triage holds the run for the environment, which Run has yet to take up (see
// heedEnvironment).
func (r *runState) idleWake(w waits, from gateMark) (why woke, over bool) {
	o := r.o
	why, _ = r.wait(w)
	return why, r.ctx.Err() != nil || o.gate.mark() != from || o.envStop != nil && !o.gate.held(gateEnv)
}
