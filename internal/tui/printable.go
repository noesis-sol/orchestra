package tui

import (
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// What the dashboard and the printer take in is made printable once, where it is received: an
// event or a status as the dashboard or the printer takes it, the message of a run with nothing to
// run, an interview's progress and the scout's answer. Any of their text can come from a ticket, a
// worker, bd or an organ, a ticket's ID included (Beads checks only its prefix), so the draw sites
// need not each remember to: oneLine and the like only lay text out.

// printableEvent is ev with its text made printable.
func printableEvent(ev dispatch.Event) dispatch.Event {
	ev.Ticket, ev.Title, ev.Detail, ev.Text = Printable(ev.Ticket), Printable(ev.Title), Printable(ev.Detail),
		Printable(ev.Text)
	ev.Blocked, ev.Solo.Ticket = Printable(ev.Blocked), Printable(ev.Solo.Ticket)
	ev.Suite, ev.Output = Printable(ev.Suite), Printable(ev.Output)
	if ev.Ran != nil {
		ran := *ev.Ran
		ran.Suite, ran.Ticket = dispatch.CheckSuite(Printable(string(ran.Suite))), Printable(ran.Ticket)
		ran.Command, ran.Commit = Printable(ran.Command), Printable(ran.Commit)
		ran.Result, ran.Output = dispatch.CheckResult(Printable(string(ran.Result))), Printable(ran.Output)
		ev.Ran = &ran
	}
	if ev.Tokens != nil {
		var tokens dispatch.Tokens // by model, as a transcript names it
		for model, n := range ev.Tokens {
			tokens = tokens.Add(dispatch.Tokens{Printable(model): n})
		}
		ev.Tokens = tokens
	}
	return ev
}

// printableStatus is st with its text made printable. Its Tab is the terminal's own name for the
// worker's tab, handed back to the terminal to switch to it and never shown, so it is left as it is.
func printableStatus(st dispatch.Status) dispatch.Status {
	st.Ticket, st.Title, st.Activity = Printable(st.Ticket), Printable(st.Title), Printable(st.Activity)
	st.Agent, st.Doing = dispatch.AgentState(Printable(string(st.Agent))), dispatch.Doing(Printable(string(st.Doing)))
	st.Merge = dispatch.MergeStage(Printable(string(st.Merge)))
	return st
}

// printableNothing is n with its text made printable: the tickets it names and why they are held.
func printableNothing(n dispatch.NothingToRun) dispatch.NothingToRun {
	n.Scope, n.Built, n.Done = Printable(n.Scope), Printable(n.Built), Printable(n.Done)
	n.StillOpen = printables(n.StillOpen)
	if n.NotDone != nil {
		notDone := make([]dispatch.NotDone, len(n.NotDone))
		for i, d := range n.NotDone {
			notDone[i] = dispatch.NotDone{ID: Printable(d.ID), Why: Printable(d.Why)}
		}
		n.NotDone = notDone
	}
	if n.DeferredFree != nil {
		free := make([]dispatch.DeferredFree, len(n.DeferredFree))
		for i, d := range n.DeferredFree {
			free[i] = dispatch.DeferredFree{ID: Printable(d.ID), Blockers: printables(d.Blockers),
				Worktree: Printable(d.Worktree), Uncommitted: Printable(d.Uncommitted)}
		}
		n.DeferredFree = free
	}
	return n
}

// printable is p with its text made printable: the epic claude filed and its title.
func (p InterviewProgress) printable() InterviewProgress {
	p.Agent, p.Epic, p.Title = dispatch.AgentState(Printable(string(p.Agent))), Printable(p.Epic), Printable(p.Title)
	return p
}

// printableScouting is found with the scout's words made printable: it read the repository's files,
// which may be hostile. A suite's Command and Targeted are left as the scout gave them, for the
// runners to run: suiteLabel makes the command printable where it shows it.
func printableScouting(found organ.Scouting) organ.Scouting {
	found.Note, found.Stack = Printable(found.Note), printables(found.Stack)
	if found.Suites != nil {
		suites := make([]organ.Suite, len(found.Suites))
		for i, su := range found.Suites {
			su.Name, su.Kind = Printable(su.Name), organ.SuiteKind(Printable(string(su.Kind)))
			su.FoundIn, su.Tier, su.Needs = Printable(su.FoundIn), organ.Tier(Printable(string(su.Tier))), printables(su.Needs)
			suites[i] = su
		}
		found.Suites = suites
	}
	return found
}

// printables is each of ss made printable, in a new slice; nil for nil.
func printables(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = Printable(s)
	}
	return out
}
