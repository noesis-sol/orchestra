package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// An environment-wide failure (a safety classifier that refuses every command, say) fails each
// worker the same way at once, whichever ticket it has. Setting each ticket aside in turn would
// burn through the queue, so the run holds instead: no new tickets, the running ones finish, and
// the tickets that failed this way are reopened, as they did nothing. Either signal holds it, with
// Config.EnvHoldCount tickets in a row:
//   - their workers settled within Config.EnvHoldWindow of dispatch without claiming the ticket,
//     committing or leaving changes (needs nothing but the loop, and works with triage off);
//   - triage blamed the environment with high confidence.
//
// Once no ticket runs, and after Config.EnvProbe, one worker without a ticket is asked to run a
// command in the main checkout. If it does, the machine works again: the hold ends and the run takes
// tickets again. If not, the run ends as it would have. The machine is probed once per run, so an
// environment that keeps failing ends the run the second time it holds. Triage's verdicts on the
// tickets deferred before the probe ended the hold count for nothing: triage takes them one at a
// time, a model call each, so they can come long after the probe has shown that the machine works.

// failedAtOnce reports whether a worker settled as one failed by its environment does: idle for good
// (from idleAt) soon after started, its ticket still open, its branch still at head and its
// worktree clean; not where git can't tell. Going idle counts, not settling: an idle worker with its
// ticket open is given startGrace before it settles, which can outlast the window.
func (o *Loop) failedAtOnce(ctx context.Context, status TicketStatus, started, idleAt time.Time,
	br, head, wt string) bool {
	c := o.cfg
	if c.EnvHoldCount == 0 || status != StatusOpen || idleAt.Sub(started) >= c.EnvHoldWindow {
		return false
	}
	now, err := o.checkout.Head(ctx, c.Repo, br)
	if err != nil || now != head {
		return false
	}
	dirty, err := o.checkout.DirtyWorktree(ctx, wt)
	return err == nil && dirty == ""
}

// settling is how a worker settled, as the hold counts it. One requeued doesn't count toward
// Config.Limit either.
type settling int

const (
	unsettled   settling = iota // it never settled with its ticket read: the row goes on
	settledFast                 // it failed at once, as one its environment fails does
	settledSlow                 // any other way, which ends the row
	requeued                    // back in the queue, to wait for a ticket to merge: as unsettled
	// no worker started, as the setup failed in its worktree: as settledFast, its ticket back in the
	// queue already (see leaveSetupFailed)
	setupFailed
)

// fastFail is a ticket in the row failing at once, and whether that was its worktree's setup failing.
type fastFail struct {
	id    string
	setup bool
}

// countSettling counts a settled worker toward the hold: one that failed at once, or a setup that
// failed before it could start, adds to the tickets failing in a row, and any other ends the row. A
// ticket whose worker fails this way once the run holds is reopened too. Run calls it with each
// result.
func (o *Loop) countSettling(ctx context.Context, id string, how settling) {
	n := o.cfg.EnvHoldCount
	if n == 0 || how == unsettled || how == requeued {
		return
	}
	if how != settledFast && how != setupFailed {
		o.fastFails = nil
		return
	}
	o.fastFails = append(o.fastFails, fastFail{id: id, setup: how == setupFailed})
	switch {
	case o.envStop != nil:
		if how == settledFast {
			o.reopenFailed(ctx, id)
		}
	case len(o.fastFails) >= n:
		o.holdForEnvironment(ctx, o.fastFailsWhy())
	}
}

// fastFailsWhy says why the row of tickets failing at once holds the run: their workers settled at
// once, the setup failed in their worktrees, or some of each.
func (o *Loop) fastFailsWhy() string {
	ids, setups := make([]string, len(o.fastFails)), 0
	for i, f := range o.fastFails {
		ids[i] = f.id
		if f.setup {
			setups++
		}
	}
	n, list, window := len(ids), strings.Join(ids, ", "), command.ShortDuration(o.cfg.EnvHoldWindow)
	switch setups {
	case 0:
		return fmt.Sprintf("the last %d tickets (%s) each settled within %s of starting without being claimed or changed",
			n, list, window)
	case n:
		return fmt.Sprintf("the setup %s failed in the worktrees of the last %d tickets (%s)",
			setupSaid(o.setupNow().cmd), n, list)
	}
	return fmt.Sprintf("the last %d tickets (%s) each failed at once: the setup %s failed in their worktree, "+
		"or their worker settled within %s of starting without being claimed or changed",
		n, list, setupSaid(o.setupNow().cmd), window)
}

// verdict is triage's verdict on a deferred ticket, as the hold counts it, with the hold's
// generation (Loop.envGen) as the ticket was queued for triage.
type verdict struct {
	id, cause, confidence, summary string
	gen                            uint64
}

// blamed hands a triage verdict to Run, waiting until Run takes it; once Run has returned, triage
// counts it itself.
func (o *Loop) blamed(ctx context.Context, v verdict) {
	if o.cfg.EnvHoldCount == 0 {
		return
	}
	select {
	case o.verdicts <- v:
	case <-o.runDone:
		o.triaged(ctx, v)
	}
}

// triaged counts a triage verdict toward the hold: one blaming the environment with high
// confidence adds to the row, any other ends it. One on a ticket queued before a probe ended the
// hold counts for nothing, as that row was forgotten.
func (o *Loop) triaged(ctx context.Context, v verdict) {
	n := o.cfg.EnvHoldCount
	if n == 0 || v.gen != o.envGen.Load() {
		return
	}
	if v.cause != "environment" || v.confidence != "high" {
		o.envVerdicts = nil
		return
	}
	o.envVerdicts = append(o.envVerdicts, v.id)
	if len(o.envVerdicts) >= n {
		o.holdForEnvironment(ctx, fmt.Sprintf(
			"triage blamed the environment for the last %d tickets (%s) with high confidence (%s)",
			len(o.envVerdicts), strings.Join(o.envVerdicts, ", "), strings.TrimSuffix(strings.TrimSpace(v.summary), ".")))
	}
}

// holdForEnvironment holds the run, once, and reopens the tickets in the current row of workers
// that failed at once; those whose setup failed are back in the queue already. Run picks the reason up
// before it starts another ticket.
func (o *Loop) holdForEnvironment(ctx context.Context, why string) {
	if o.envStop != nil {
		return
	}
	o.envWhy = why
	o.envStop = halt(ExitEnvironment, stopEnvironment, ": %s; check the machine, then restart", why)
	for _, f := range o.fastFails {
		if !f.setup {
			o.reopenFailed(ctx, f.id)
		}
	}
}

// reopenFailed puts a ticket whose worker failed at once back in the queue, keeping its notes: it
// did nothing, and its worktree is clean.
func (o *Loop) reopenFailed(ctx context.Context, id string) {
	if !o.reopen(ctx, id, "", "be back in the queue after failing at once like the tickets before it") {
		return
	}
	o.book.requeue(id)
	o.appendNotes(ctx, id, "Orchestra: reopened; its worker failed at once, like the tickets before it, "+
		"so the run held for the environment rather than for this ticket.")
	// No longer set aside: the dashboard's total and the run's closing count leave it out.
	o.emit(Event{Kind: EvReopened, Ticket: id, Detail: "the run held for the environment", Text: fmt.Sprintf(
		"  %s reopened: its worker failed at once, like the tickets before it", id)})
}

// probeID labels the probe worker's tab, and names it as a ticket's worker would be named.
const probeID = "orchestra-probe"

// probeLimit is how long the probe worker may take, from its tab opening, to run its command.
const probeLimit = 5 * time.Minute

// probeEnvironment is Run's next step when nothing runs and stop is the only reason to stop: with
// the run held for the environment, and not probed yet, it waits Config.EnvProbe and then probes
// the machine. It returns nil when the probe ran its command and the hold has ended, or else the
// reason to end the run with. winding says whether the maintainer asked to wind down, hearing any
// request waiting; hear takes one as it comes, and says the same.
func (o *Loop) probeEnvironment(
	ctx context.Context, stop *stopReason, winding func() bool, hear func(drainRequest) bool,
) *stopReason {
	after := o.cfg.EnvProbe
	if after <= 0 || o.envProbed || stop == nil || stop != o.envStop || winding() || ctx.Err() != nil {
		return stop
	}
	o.envProbed = true
	o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
		"PROBE: the run holds for the environment; "+
			"in %s one worker without a ticket runs a command, and if it does the run takes tickets again",
		command.ShortDuration(after))})
	wait := time.NewTimer(after)
	defer wait.Stop()
	for waiting := true; waiting; {
		select {
		case <-ctx.Done():
			return stop
		case r := <-o.drainReqs:
			if hear(r) {
				return stop
			}
		case v := <-o.verdicts:
			o.triaged(context.WithoutCancel(ctx), v) // held already: it changes nothing but the row
		case <-wait.C:
			waiting = false
		}
	}
	tab, err := o.probe(ctx)
	if ctx.Err() != nil {
		return stop
	}
	if err != nil {
		return halt(ExitEnvironment, stopEnvironment,
			": %s; a worker probing the machine %s later failed too: %v; check the machine, then restart",
			o.envWhy, command.ShortDuration(after), err).causedBy(err)
	}
	o.closeTab(ctx, tab)
	// Triage's verdicts still to come on the tickets queued so far are forgotten with the row, and the
	// tickets whose setup failed are taken again, as those reopened are.
	o.envGen.Add(1)
	o.envStop, o.fastFails, o.envVerdicts = nil, nil, nil
	o.retrySetups()
	o.emit(Event{Kind: EvProbed, Text: fmt.Sprintf(
		"PROBE_OK: a worker without a ticket ran a command %s after the hold; taking tickets again",
		command.ShortDuration(after))})
	return nil
}

// probe starts a worker without a ticket in the main checkout and asks it to run one command,
// which writes a file under .orchestra/run/. It returns the worker's tab, and why the probe failed:
// the worker could not be started, never took its prompt, or stopped or timed out without the file.
func (o *Loop) probe(ctx context.Context) (tab string, err error) {
	c := o.cfg
	// The probe's worker writes the file, so it is reached through an os.Root at the main checkout,
	// which won't follow a symlink out of it.
	root, err := project.OpenRun(c.Repo)
	if err != nil {
		return "", fmt.Errorf("cannot make %s in %s: %w", project.RunPath(""), c.Repo, err)
	}
	proof := project.RunPath("probe")
	defer func() {
		_ = root.Remove(proof) // best effort: the next probe removes it first
		_ = root.Close()       // nothing written through it is lost
	}()
	if err := project.RemoveRun(root, c.Repo, proof); err != nil {
		return "", fmt.Errorf("cannot remove an earlier probe's %s: %w", proof, err)
	}
	ran := func() bool {
		b, err := root.ReadFile(proof)
		return err == nil && strings.TrimSpace(string(b)) == "ok"
	}

	agent := o.agentName(probeID)
	if err := o.freeProbeName(ctx, agent); err != nil {
		return "", err
	}
	args, err := o.probeArgs()
	if err != nil {
		return "", err
	}
	tab, deadline, err := o.startProbe(ctx, agent, args)
	if err != nil {
		return tab, err
	}
	prompt := fmt.Sprintf("Orchestra is checking that commands run on this machine; there is no ticket. "+
		"Run exactly this one shell command, then stop without doing anything else: echo ok > %s",
		command.ShellQuote(filepath.Join(c.Repo, proof)))
	if !o.deliverPrompt(ctx, agent, prompt) {
		return tab, fmt.Errorf("it never started on its prompt (tab %s)", tab)
	}
	return tab, o.awaitProbe(ctx, agent, tab, deadline, ran)
}

// freeProbeName renames an earlier probe's worker, left open because it failed, that may hold the
// probe worker's name, agent.
func (o *Loop) freeProbeName(ctx context.Context, agent string) error {
	if st, err := o.readStatus(ctx, agent, 5); err != nil || st != StateGone { // unreadable: the name may be taken
		if name := o.namer.FreeName(ctx, agent); name == "" || o.namer.RenameAgent(ctx, agent, name) != nil {
			return fmt.Errorf("an earlier worker holds the name %s and could not be renamed", agent)
		}
	}
	return nil
}

// probeArgs are the probe worker's arguments. It runs one echo, so a Claude probe starts without
// MCP servers, in the workers' permission mode and without the tools they never use, reporting what
// it does when the project asks for that (Config.ProbeHooks).
func (o *Loop) probeArgs() ([]string, error) {
	c := o.cfg
	if !c.ClaudeWorkers() {
		return nil, nil
	}
	args := append(append([]string{"--strict-mcp-config"}, o.permission...), o.unusedTools...)
	if c.ProbeHooks && o.reporter != nil {
		report, err := o.reporter.ReportArgsIn(c.Repo, project.ProbeRun)
		if err != nil {
			return nil, fmt.Errorf("cannot set it up to report what it does: %w", err)
		}
		args = append(args, report...)
	}
	return args, nil
}

// startProbe starts the probe worker, agent, with args in a tab of its own in the main checkout,
// plainly if the terminal refuses the arguments. It returns the tab, once made, and when the probe
// runs out of time: probeLimit after its tab was made.
func (o *Loop) startProbe(
	ctx context.Context, agent string, args []string,
) (tab string, deadline time.Time, err error) {
	c := o.cfg
	tab, pane, err := o.tabs.CreateTab(ctx, c.Workspace, c.Repo, probeID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("no tab for it in workspace %s: %w", c.Workspace, err)
	}
	deadline = time.Now().Add(probeLimit)
	err = o.starter.StartAgent(ctx, agent, c.AgentKind, pane, args)
	if err != nil && len(args) > 0 && errors.Is(err, ErrArgumentRefused) {
		o.log.Raw("", err)
		err = o.starter.StartAgent(ctx, agent, c.AgentKind, pane, nil)
	}
	if err != nil {
		// 'agent start' can fail while the agent still comes up.
		if st, serr := o.agents.Status(ctx, agent); serr != nil || st == StateGone { // the start's error is the one to report
			return tab, deadline, fmt.Errorf("it could not be started in tab %s: %s",
				tab, strings.Join(strings.Fields(err.Error()), " "))
		}
		o.log.Raw("", err) // it came up all the same
	}
	o.info("  probe worker %s started in tab %s", agent, tab)
	return tab, deadline, nil
}

// awaitProbe waits until the probe worker, agent in tab, has run its command (ran says whether it
// has), and returns why it didn't: it went away or stopped without, or deadline passed.
func (o *Loop) awaitProbe(ctx context.Context, agent, tab string, deadline time.Time, ran func() bool) error {
	for {
		if ran() {
			return nil
		}
		st, err := o.agents.Status(ctx, agent) // unreadable: asked again after the next poll
		switch {
		case err == nil && st == StateGone:
			return fmt.Errorf("it went away without running its command (tab %s)", tab)
		case err == nil && (st == StateIdle || st == StateDone) && !ran():
			return fmt.Errorf("it stopped without running its command; see tab %s", tab)
		case err == nil && st == StateLimited && !ran():
			return fmt.Errorf("it reached the account's usage limit without running its command; see tab %s", tab)
		case time.Now().After(deadline):
			return fmt.Errorf("it ran no command within %s; see tab %s",
				command.ShortDuration(probeLimit), tab)
		}
		if !sleep(ctx, o.pollEvery()) {
			return errors.New("interrupted")
		}
	}
}
