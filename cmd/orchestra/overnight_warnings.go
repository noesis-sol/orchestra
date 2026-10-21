package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/load"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's warnings: each finds what would slow the night, cost it tickets, or leave
// something for the morning, and offers its fix where orchestra can make one. None refuses to start
// the run.

// sessionLimit is how long the account's session limit takes to reset.
const sessionLimit = 5 * time.Hour

// usageWaitCheck warns of a run that waits less for the usage limit to reset than the session limit
// takes (usage_limit_wait under 5h, or 0): the limit, reached in the night, would stop the run. It
// offers the default wait, 6h.
func usageWaitCheck(_ context.Context, s *session) preflightResult {
	wait := s.cfg.UsageLimitWait
	if wait >= sessionLimit {
		return preflightResult{verdict: tui.CheckPassed, text: "the run waits up to " + command.ShortDuration(wait) +
			" for the account's usage limit to reset"}
	}
	text := "usage_limit_wait is 0: the account's usage limit, reached in the night, would stop the run"
	if wait > 0 {
		text = fmt.Sprintf("usage_limit_wait is %s, under the %s the session limit takes to reset: the limit, "+
			"reached in the night, would stop the run", command.ShortDuration(wait), command.ShortDuration(sessionLimit))
	}
	six := command.ShortDuration(project.DefaultUsageLimitWait)
	return preflightResult{verdict: tui.CheckWarned, text: text,
		fix: &preflightFix{question: "Wait up to " + six + " for the usage limit to reset?",
			done: "the run waits up to " + six + " for the usage limit to reset",
			apply: func(s *session) error {
				s.cfg.UsageLimitWait = project.DefaultUsageLimitWait
				return nil
			}}}
}

// usageNowCheck is the check that the account isn't at its usage limit now, from the smallest call the
// claude bin names answers (organ.Client.UsageNow): at the limit, it warns that no ticket starts until
// the limit resets, and when that is.
func usageNowCheck(bin string) preflightCheck {
	return func(ctx context.Context, s *session) preflightResult {
		if kind := s.cfg.Agent(); !kind.ClaudeArgs {
			return preflightResult{verdict: tui.CheckPassed,
				text: "workers are " + kind.Name + ": the Claude account's usage limit isn't asked"}
		}
		said, limited, err := organ.Client{Bin: bin}.UsageNow(ctx)
		switch {
		case err != nil:
			return preflightResult{verdict: tui.CheckWarned, text: "orchestra couldn't tell whether the account is " +
				"at its usage limit now: " + oneLine(err.Error())}
		case !limited:
			return preflightResult{verdict: tui.CheckPassed, text: "the account isn't at its usage limit now"}
		}
		return preflightResult{verdict: tui.CheckWarned, text: usageLimitedNow(said, s.cfg.UsageLimitWait, time.Now())}
	}
}

// usageLimitedNow says that the account is at its usage limit at now, as Claude Code said it, said:
// when it resets, where said tells, and whether the run, waiting wait for it, would stop first.
func usageLimitedNow(said string, wait time.Duration, now time.Time) string {
	text := fmt.Sprintf("the account is at its usage limit now (%q): no ticket starts until it resets", oneLine(said))
	reset := agentio.LimitReset(said, now)
	if reset.IsZero() {
		return text
	}
	text += fmt.Sprintf(", at %s, in %s", dispatch.ResetLabel(reset, now),
		command.ShortDuration(reset.Sub(now).Round(time.Minute)))
	if wait > 0 && reset.After(now.Add(wait)) {
		text += fmt.Sprintf(", after the run's wait for it (usage_limit_wait %s) runs out: the run would stop",
			command.ShortDuration(wait))
	}
	return text
}

// overnightTicketLimit is the ticket limit the pre-flight offers a run without one.
const overnightTicketLimit = 2 * time.Hour

// ticketLimitCheck warns of a run with no ticket limit (ticket_limit, --ticket-limit), where a hung
// worker holds its slot all night, and offers 2h.
func ticketLimitCheck(_ context.Context, s *session) preflightResult {
	if limit := s.cfg.TicketLimit; limit > 0 {
		return preflightResult{verdict: tui.CheckPassed,
			text: "each ticket's worker has " + command.ShortDuration(limit) + " (ticket_limit)"}
	}
	two := command.ShortDuration(overnightTicketLimit)
	return preflightResult{verdict: tui.CheckWarned,
		text: "no ticket limit (ticket_limit in settings.json, --ticket-limit): a hung worker would hold its slot " +
			"all night",
		fix: &preflightFix{question: "Give each ticket's worker " + two + "?",
			done: "each ticket's worker has " + two,
			apply: func(s *session) error {
				s.cfg.TicketLimit = overnightTicketLimit
				return nil
			}}}
}

// otherRunsCheck warns of the other orchestra runs going on the machine, naming each: their workers and
// checks share its cores and memory with the run's all night.
func otherRunsCheck(_ context.Context, s *session) preflightResult {
	runs := dispatch.OtherRuns(runsDir(s.e.getenv))
	if len(runs) == 0 {
		return preflightResult{verdict: tui.CheckPassed, text: "no other orchestra run on the machine"}
	}
	named := make([]string, len(runs))
	for i, r := range runs {
		named[i] = fmt.Sprintf("in %s (PID %d, %s at a time, %d running)", r.Repo, r.PID,
			ticketCount(r.Concurrent), r.Running)
	}
	text := "another orchestra run is going on the machine, "
	if len(runs) > 1 {
		text = fmt.Sprintf("%d other orchestra runs are going on the machine: ", len(runs))
	}
	return preflightResult{verdict: tui.CheckWarned, text: text + joinAnd(named) +
		": their workers and checks share the machine with the run's, and slow each other down"}
}

// loadCheck warns of a machine already busy as the run starts, from what read says of its kernel's
// figures (load.ReadKernel): a load past load.BusyLoad per core, or the kernel's memory pressure at warn
// or worse, naming the processes that weigh most, as list lists them.
func loadCheck(read func(context.Context) (load.Kernel, error),
	list func(context.Context) ([]namedProcess, error)) preflightCheck {
	return func(ctx context.Context, _ *session) preflightResult {
		k, err := read(ctx)
		if err != nil {
			return preflightResult{verdict: tui.CheckWarned,
				text: "orchestra couldn't read the machine's load: " + oneLine(err.Error())}
		}
		busy := k.Cores > 0 && k.Load1 > load.BusyLoad*float64(k.Cores)
		pressed := k.Pressure >= load.PressureWarn
		figures := fmt.Sprintf("a load of %.1f on %d cores", k.Load1, k.Cores)
		if k.Pressure != load.PressureUnknown {
			figures += ", memory pressure " + k.Pressure.String()
		}
		if !busy && !pressed {
			return preflightResult{verdict: tui.CheckPassed, text: "the machine isn't busy: " + figures}
		}
		ps, err := list(ctx)
		if err != nil {
			ps = nil // the warning goes without the processes' names
		}
		var found []string
		if busy {
			text := fmt.Sprintf("the machine is busy, %s", figures)
			if top := heaviest(ps, 3, func(p namedProcess) float64 { return p.cpu },
				func(p namedProcess) string { return fmt.Sprintf("%s %.0f%%", p.name, p.cpu) }); len(top) > 0 {
				text += " (most of it " + joinAnd(top) + ")"
			}
			found = append(found, text)
		}
		if pressed {
			text := "memory is short (the kernel's memory pressure is " + k.Pressure.String() + ")"
			if top := heaviest(ps, 3, func(p namedProcess) float64 { return float64(p.memory) },
				func(p namedProcess) string { return fmt.Sprintf("%s %s", p.name, memoryGB(p.memory)) }); len(top) > 0 {
				text += ", most of it held by " + joinAnd(top)
			}
			found = append(found, text)
		}
		return preflightResult{verdict: tui.CheckWarned, text: strings.Join(found, "; ") +
			": the workers and their checks would run slowly. Quit what the run doesn't need"}
	}
}

// vmCheck warns of a virtual machine holding much of the machine's memory (Docker's above all), as
// list lists the processes, naming it.
func vmCheck(list func(context.Context) ([]namedProcess, error)) preflightCheck {
	return func(ctx context.Context, _ *session) preflightResult {
		ps, err := list(ctx)
		if err != nil {
			return preflightResult{verdict: tui.CheckWarned,
				text: "orchestra couldn't list the machine's processes for a large virtual machine: " + oneLine(err.Error())}
		}
		vms := largeVMs(ps)
		if len(vms) == 0 {
			return preflightResult{verdict: tui.CheckPassed,
				text: "no virtual machine holds more than " + memoryGB(vmMemory) + " of memory"}
		}
		return preflightResult{verdict: tui.CheckWarned, text: strings.Join(vms, "; ") +
			": the workers and their checks get only what it leaves. Stop it, or give it less memory, " +
			"if the run doesn't need it"}
	}
}

// batteryCheck warns of a machine on the system goos running on battery, as power says: it may run
// flat, or sleep, in the night.
func batteryCheck(goos string, power func(context.Context, string) (bool, error)) preflightCheck {
	return func(ctx context.Context, _ *session) preflightResult {
		battery, err := power(ctx, goos)
		switch {
		case err != nil:
			return preflightResult{verdict: tui.CheckWarned, text: "orchestra couldn't tell whether the machine runs " +
				"on battery (" + oneLine(err.Error()) + "): plug it in for the night"}
		case battery:
			return preflightResult{verdict: tui.CheckWarned, text: "the machine runs on battery: it would run flat, " +
				"or sleep, in the night. Plug it in"}
		}
		return preflightResult{verdict: tui.CheckPassed, text: "the machine is plugged in"}
	}
}

// templatesCheck warns of a worker prompt or verifier older than orchestra's templates, which the
// workers would follow all night (project.OutdatedTemplates), and offers to bring them up to date as
// orchestra init --update does, committed on the branch the run works on: the run refuses to start on
// uncommitted changes. The fix then has the run read the worker prompt again, which it read as it
// opened, so that its workers follow the one committed.
func templatesCheck(ctx context.Context, s *session) preflightResult {
	if s.cfg.ownPrompt {
		return preflightResult{verdict: tui.CheckPassed, text: "the run's worker prompt is its own (--prompt), " +
			"from no template"}
	}
	outdated := project.OutdatedTemplates(s.cfg.Repo)
	if len(outdated) == 0 {
		return preflightResult{verdict: tui.CheckPassed, text: "the worker prompt and the verifier are up to date " +
			"with orchestra's templates"}
	}
	notes := make([]string, len(outdated))
	for i, o := range outdated {
		notes[i] = o.Note()
	}
	return preflightResult{verdict: tui.CheckWarned, text: strings.Join(notes, "; "),
		fix: &preflightFix{question: "Bring them up to date (orchestra init --update) and commit that?",
			done: "the worker prompt and the verifier are up to date with orchestra's templates, committed " +
				"(git show reads the change)",
			apply: func(s *session) error {
				if err := updateTemplates(ctx, s.cfg.Repo); err != nil {
					return err
				}
				if err := s.loadPrompt(); err != nil {
					return fmt.Errorf("committed, but the worker prompt could not be read again, so the run "+
						"gives its workers the one it read before: %w", err)
				}
				return nil
			}}}
}

// updateTemplatesMessage is the message of the commit that brings the templates up to date.
const updateTemplatesMessage = "Bring the worker prompt and the verifier up to date with orchestra's templates\n\n" +
	"orchestra --overnight's pre-flight ran orchestra init --update, as answered."

// updateTemplates brings the worker prompt and the verifier of the repository repo up to date with
// orchestra's templates, as orchestra init --update does, and commits what it changed.
func updateTemplates(ctx context.Context, repo string) error {
	steps, _, err := project.UpdateTemplates(repo)
	if err != nil {
		return err
	}
	var paths []string
	for _, st := range steps {
		for _, p := range st.Commit {
			if !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	_, err = git.Git{}.CommitPaths(ctx, repo, updateTemplatesMessage, paths...)
	return err
}

// readyTracker is what the ready check reads of the backlog: the tickets ready, and the questions and
// what waits on them.
type readyTracker interface {
	answerTracker
	Ready(ctx context.Context, scope string) ([]dispatch.Ticket, error)
}

// beadsTracker is the run s's backlog, on bd.
func beadsTracker(s *session) readyTracker {
	return beads.Tracker{Repo: s.cfg.Repo, ExcludeTypes: s.cfg.ExcludeTypes}
}

// readyCheck says how many tickets in the run's scope are ready, and how many more wait only on the
// open questions, as the tracker of tracker says: it warns where none is ready, the run then ending at
// once.
func readyCheck(tracker func(*session) readyTracker) preflightCheck {
	return func(ctx context.Context, s *session) preflightResult {
		tr, scope := tracker(s), s.cfg.Ticket
		ready, err := tr.Ready(ctx, scope)
		if err != nil {
			return preflightResult{verdict: tui.CheckWarned,
				text: "orchestra couldn't read which tickets are ready: " + oneLine(err.Error())}
		}
		waiting, err := waitingOnQuestions(ctx, tr, scope)
		if err != nil {
			return preflightResult{verdict: tui.CheckWarned, text: fmt.Sprintf("%s ready; orchestra couldn't read "+
				"which wait on questions: %s", ticketCount(len(ready)), oneLine(err.Error()))}
		}
		more := ""
		switch {
		case waiting == 1:
			more = ", and 1 more waits only on questions, to be ready once they are answered"
		case waiting > 1:
			more = fmt.Sprintf(", and %d more wait only on questions, to be ready once those are answered", waiting)
		}
		if len(ready) == 0 {
			return preflightResult{verdict: tui.CheckWarned, text: "no ticket is ready" + more +
				": with nothing to work on, the run would end at once"}
		}
		are := "are"
		if len(ready) == 1 {
			are = "is"
		}
		return preflightResult{verdict: tui.CheckPassed, text: fmt.Sprintf("%s %s ready%s", ticketCount(len(ready)),
			are, more)}
	}
}

// waitingOnQuestions counts the tickets in scope (all where it is "") that wait only on open questions:
// every blocker of theirs not closed is one.
func waitingOnQuestions(ctx context.Context, tr readyTracker, scope string) (int, error) {
	qs, err := readQuestions(ctx, tr, scope, "")
	if err != nil {
		return 0, err
	}
	var in map[string]bool
	if scope != "" {
		under, err := tr.Descendants(ctx, scope)
		if err != nil {
			return 0, err
		}
		in = map[string]bool{scope: true}
		for _, t := range under {
			in[t.ID] = true
		}
	}
	seen := map[string]bool{}
	n := 0
	for _, q := range qs {
		for _, b := range q.blocks {
			if seen[b.ID] || in != nil && !in[b.ID] {
				continue
			}
			seen[b.ID] = true
			t, err := tr.Show(ctx, b.ID) // bd show gives its blockers, each with its status and labels
			if errors.Is(err, dispatch.ErrNoTicket) {
				continue
			}
			if err != nil {
				return 0, err
			}
			if waitsOnlyOnQuestions(t) {
				n++
			}
		}
	}
	return n, nil
}

// waitsOnlyOnQuestions reports whether the ticket t, as bd shows it, is one the run would take but
// waits on its blockers, each not closed an open question.
func waitsOnlyOnQuestions(t dispatch.Ticket) bool {
	if t.Status != dispatch.StatusOpen && t.Status != dispatch.StatusBlocked || dispatch.HasLabel(t, dispatch.HumanLabel) {
		return false
	}
	waits := false
	for _, d := range t.Dependencies {
		if d.DependencyType != "blocks" || d.Status == dispatch.StatusClosed {
			continue
		}
		if !dispatch.HasLabel(d, dispatch.HumanLabel) {
			return false
		}
		waits = true
	}
	return waits
}
