package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
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
// environment that keeps failing ends the run the second time it holds.

// failedAtOnce reports whether a worker settled as one failed by its environment does: soon after
// started, its ticket still open, its branch still at head and its worktree clean.
func (o *Loop) failedAtOnce(status string, started time.Time, br, head, wt string) bool {
	c := o.cfg
	return c.EnvHoldCount > 0 && status == "open" && time.Since(started) < c.EnvHoldWindow &&
		o.checkout.Head(c.Repo, br) == head && o.checkout.DirtyWorktree(wt) == ""
}

// settledFast counts a settled worker toward the hold: one that failed at once adds to the tickets
// failing in a row, and any other ends the row. A ticket failing this way once the run holds is
// reopened too.
func (o *Loop) settledFast(id string, fast bool) {
	n := o.cfg.EnvHoldCount
	if n == 0 {
		return
	}
	o.mu.Lock()
	if !fast {
		o.fastFails = nil
		o.mu.Unlock()
		return
	}
	o.fastFails = append(o.fastFails, id)
	ids := append([]string(nil), o.fastFails...)
	held := o.envStop != nil
	o.mu.Unlock()
	switch {
	case held:
		o.reopenFailed(id)
	case len(ids) >= n:
		o.holdForEnvironment(fmt.Sprintf("the last %d tickets (%s) each settled within %s of starting without being claimed or changed",
			len(ids), strings.Join(ids, ", "), ShortDuration(o.cfg.EnvHoldWindow)))
	}
}

// triaged counts a triage verdict toward the hold: one blaming the environment with high
// confidence adds to the row, any other ends it.
func (o *Loop) triaged(id, cause, confidence, summary string) {
	n := o.cfg.EnvHoldCount
	if n == 0 {
		return
	}
	o.mu.Lock()
	if cause != "environment" || confidence != "high" {
		o.envVerdicts = nil
		o.mu.Unlock()
		return
	}
	o.envVerdicts = append(o.envVerdicts, id)
	ids := append([]string(nil), o.envVerdicts...)
	o.mu.Unlock()
	if len(ids) >= n {
		o.holdForEnvironment(fmt.Sprintf("triage blamed the environment for the last %d tickets (%s) with high confidence (%s)",
			len(ids), strings.Join(ids, ", "), strings.TrimSuffix(strings.TrimSpace(summary), ".")))
	}
}

// holdForEnvironment holds the run, once, and reopens the tickets in the current row of workers
// that failed at once. Run picks the reason up before it starts another ticket.
func (o *Loop) holdForEnvironment(why string) {
	o.mu.Lock()
	if o.envStop != nil {
		o.mu.Unlock()
		return
	}
	o.envWhy = why
	o.envStop = halt(ExitEnvironment, "ENVIRONMENT: %s; check the machine, then restart", why)
	reopen := append([]string(nil), o.fastFails...)
	o.mu.Unlock()
	for _, id := range reopen {
		o.reopenFailed(id)
	}
	select {
	case o.envWake <- struct{}{}:
	default: // already told, or no Run to tell
	}
}

// environmentStop is the reason the run holds for the environment, or nil.
func (o *Loop) environmentStop() *stopReason {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.envStop
}

// reopenFailed puts a ticket whose worker failed at once back in the queue, keeping its notes: it
// did nothing, and its worktree is clean.
func (o *Loop) reopenFailed(id string) {
	if err := o.notes.Reopen(id); err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  REOPEN_FAILED: %s failed at once like the tickets before it, and bd could not reopen it%s; reopen it with: bd update %s --status open",
			id, because(err), id)})
		return
	}
	o.unmarkAside(id)
	o.appendNotes(id, "Orchestra: reopened; its worker failed at once, like the tickets before it, so the run held for the environment rather than for this ticket.")
	o.info("  %s reopened: its worker failed at once, like the tickets before it", id)
}

// probeID labels the probe worker's tab, and names it as a ticket's worker would be named.
const probeID = "orchestra-probe"

// probeLimit is how long the probe worker may take, from its tab opening, to run its command.
const probeLimit = 5 * time.Minute

// probeEnvironment is Run's next step when nothing runs and stop is the only reason to stop: with
// the run held for the environment, and not probed yet, it waits Config.EnvProbe and then probes
// the machine. It returns nil when the probe ran its command and the hold has ended, or else the
// reason to end the run with. winding says whether the maintainer asked to wind down.
func (o *Loop) probeEnvironment(ctx context.Context, stop *stopReason, winding func() bool) *stopReason {
	after := o.cfg.EnvProbe
	if after <= 0 || o.envProbed || stop == nil || stop != o.environmentStop() || winding() || ctx.Err() != nil {
		return stop
	}
	o.envProbed = true
	o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
		"PROBE: the run holds for the environment; in %s one worker without a ticket runs a command, and if it does the run takes tickets again",
		ShortDuration(after))})
	wait := time.NewTimer(after)
	defer wait.Stop()
	for waiting := true; waiting; {
		select {
		case <-ctx.Done():
			return stop
		case <-o.drainWake:
			if winding() {
				return stop
			}
		case <-wait.C:
			waiting = false
		}
	}
	tab, err := o.probe(ctx)
	if ctx.Err() != nil {
		return stop
	}
	if err != nil {
		return halt(ExitEnvironment, "ENVIRONMENT: %s; a worker probing the machine %s later failed too: %v; check the machine, then restart",
			o.envWhy, ShortDuration(after), err)
	}
	o.tabs.CloseTab(tab)
	o.mu.Lock()
	o.envStop, o.fastFails, o.envVerdicts = nil, nil, nil
	o.mu.Unlock()
	o.emit(Event{Kind: EvProbed, Text: fmt.Sprintf(
		"PROBE_OK: a worker without a ticket ran a command %s after the hold; taking tickets again", ShortDuration(after))})
	return nil
}

// probe starts a worker without a ticket in the main checkout and asks it to run one command,
// which writes a file under .orchestra/run/. It returns the worker's tab, and why the probe failed:
// the worker could not be started, never took its prompt, or stopped or timed out without the file.
func (o *Loop) probe(ctx context.Context) (tab string, err error) {
	c := o.cfg
	proof := filepath.Join(c.Repo, ".orchestra", "run", "probe")
	if err := os.MkdirAll(filepath.Dir(proof), 0o755); err != nil {
		return "", fmt.Errorf("cannot make %s: %w", filepath.Dir(proof), err)
	}
	os.Remove(proof)
	defer os.Remove(proof)
	ran := func() bool {
		b, err := os.ReadFile(proof)
		return err == nil && strings.TrimSpace(string(b)) == "ok"
	}

	// An earlier probe's worker, left open because it failed, may hold the name.
	agent := o.agentName(probeID)
	if st, _ := o.readStatus(ctx, agent, 5); st != "gone" {
		if name := o.namer.FreeName(agent); name == "" || o.namer.RenameAgent(agent, name) != nil {
			return "", fmt.Errorf("an earlier worker holds the name %s and could not be renamed", agent)
		}
	}
	tab, pane, err := o.tabs.CreateTab(c.Workspace, c.Repo, probeID)
	if err != nil {
		return "", fmt.Errorf("no tab for it in workspace %s: %w", c.Workspace, err)
	}
	deadline := time.Now().Add(orDefault(o.wait.probe, probeLimit))
	if err := o.starter.StartAgent(ctx, agent, c.AgentKind, pane, nil); err != nil {
		o.log.Raw("", err)
		// 'agent start' can fail while the agent still comes up.
		if st, _ := o.agents.Status(agent); st == "gone" || st == "unreadable" {
			return tab, fmt.Errorf("it could not be started in tab %s: %s", tab, strings.Join(strings.Fields(err.Error()), " "))
		}
	}
	o.info("  probe worker %s started in tab %s", agent, tab)
	prompt := fmt.Sprintf("Orchestra is checking that commands run on this machine; there is no ticket. "+
		"Run exactly this one shell command, then stop without doing anything else: echo ok > %s", command.ShellQuote(proof))
	if !o.deliverPrompt(ctx, agent, prompt) {
		return tab, fmt.Errorf("it never started on its prompt (tab %s)", tab)
	}
	for {
		if ran() {
			return tab, nil
		}
		st, _ := o.agents.Status(agent)
		switch {
		case st == "gone":
			return tab, fmt.Errorf("it went away without running its command (tab %s)", tab)
		case (st == "idle" || st == "done") && !ran():
			return tab, fmt.Errorf("it stopped without running its command; see tab %s", tab)
		case time.Now().After(deadline):
			return tab, fmt.Errorf("it ran no command within %s; see tab %s", ShortDuration(orDefault(o.wait.probe, probeLimit)), tab)
		}
		if !sleep(ctx, o.pollEvery()) {
			return tab, errors.New("interrupted")
		}
	}
}
