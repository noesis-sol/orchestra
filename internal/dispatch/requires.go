package dispatch

import (
	"context"
	"fmt"
	"slices"

	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/requires"
)

// A project's requirements (requires in .orchestra/settings.json) are what its machine must have: a
// tool, a service running, a permission. The run checks them in the main checkout as it starts, before
// its first ticket, installing what it can (see requires.Check), and stops REQUIREMENTS_MISSING, exit
// 2, for what is still missing, rather than leave every ticket to fail on it one by one. A merge that
// changes them on Base has the run check them again before the next ticket starts (see takeRequires),
// as a merge that changes the setup has it set up from then on: the environment ticket a spec's
// breakdown files first writes them.

// checkRequires checks the requirements list in the main checkout, each install within the check's
// time limit, says how they were found, and returns the reason to stop for what is still missing; nil
// for nothing missing, none to check, or a check that Ctrl+C cut short, which the caller reads.
func (o *Loop) checkRequires(ctx context.Context, list []project.Requirement) *stopReason {
	if len(list) == 0 {
		return nil
	}
	report := requires.Check(ctx, list, requires.Options{
		Dir: o.cfg.Repo, InstallLimit: o.checkTimeout(),
		Working: func(what string) { o.info("  REQUIRES: %s", what) },
	})
	if ctx.Err() != nil {
		return nil
	}
	for _, res := range report {
		if res.KeptErr != nil {
			o.log.Raw("", fmt.Errorf("cannot keep the output of %s's install: %w", res.Name, res.KeptErr))
		}
	}
	if sum := report.Summary(); sum != "" {
		o.info("  REQUIRES: %s", sum)
	}
	if d := report.MissingDetail(); d != "" {
		return halt(ExitSetup, stopRequirements, "%s", d)
	}
	return nil
}

// requiresNow is the requirements as the run has them now: Config's, unless a merge in this run
// committed others to Base's settings.json (see takeRequires).
func (o *Loop) requiresNow() []project.Requirement {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.requiresSet != nil {
		return *o.requiresSet
	}
	return o.cfg.Requires
}

// takeRequires has the run take the requirements in s, the settings.json ticket id's merge committed
// to Base, when they differ from the run's, and check them before the next ticket starts (see
// recheckRequires). Ones a run would refuse (see project.ResolveRequires) are warned of, and the
// run's stay as they were.
func (o *Loop) takeRequires(id string, s project.Settings) {
	c := o.cfg
	list, err := project.ResolveRequires(s)
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  SETTINGS_UNREAD: %s's merge changed the requirements in %s on %s, but %v; they stay as they were",
			id, project.SettingsFile, c.Base, err)})
		return
	}
	if slices.EqualFunc(list, o.requiresNow(), project.Requirement.Equal) {
		return
	}
	o.mu.Lock()
	o.requiresSet, o.requiresDue = &list, true
	o.mu.Unlock()
	o.info("  REQUIRES: %s's merge changed the requirements in %s on %s: they are checked before the next "+
		"ticket starts", id, project.SettingsFile, c.Base)
}

// recheckRequires checks the requirements again before the next ticket starts, once a merge changed
// them (see takeRequires): what is still missing stops the run as it would at its start, the running
// tickets finishing first.
func (r *runState) recheckRequires() {
	o := r.o
	o.mu.Lock()
	due := o.requiresDue
	o.requiresDue = false
	o.mu.Unlock()
	if !due {
		return
	}
	s := o.checkRequires(r.ctx, o.requiresNow())
	if s == nil {
		return
	}
	if first := r.addStop(s); len(r.inflight) > 0 || !first {
		o.emit(Event{Kind: EvHold, Text: holdLine(s, len(r.inflight))})
	}
}
