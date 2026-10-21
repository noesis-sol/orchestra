package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker gets its standing rules in its system prompt, and only what is particular to its
// start in its first message. Claude Code sends the system prompt with every request, so the rules
// survive when a long ticket's conversation is compacted, while a summary may lose instructions
// given early in it: never push, the ways to end (DONE, a question, a deferral), checks in the
// foreground. Workers of other kinds get the whole prompt as their first message.

// workerRules are ticket id's worker's standing rules: the worker prompt with its TICKET_ID filled
// in, and what the run adds for the whole of the ticket (its scope, its setup, its time limit, the
// usage limit).
func (o *Loop) workerRules(id string) string {
	return strings.ReplaceAll(o.prompt, "TICKET_ID", id) + o.scopeNote(id) + o.setupNote() + o.requiresNote() +
		o.checkNote() + o.checkTimeNote() + o.budgetNote() + o.limitNote()
}

// fullPrompt is ticket id's whole prompt as one first message, for a worker without its rules in
// its system prompt: the worker prompt, then first (see firstNotes) and the run's notes.
func (o *Loop) fullPrompt(id, first string) string {
	return strings.ReplaceAll(o.prompt, "TICKET_ID", id) + first + o.scopeNote(id) + o.setupNote() +
		o.requiresNote() + o.checkNote() + o.checkTimeNote() + o.budgetNote() + o.limitNote()
}

// setupNote tells a worker that its worktree's dependencies are installed with the project's setup
// command (see setUpWorktree), so that it doesn't install them again itself, but does after changing
// what they are installed from; "" when the project has none.
func (o *Loop) setupNote() string {
	cmd := o.setupNow().cmd
	if cmd == "" {
		return ""
	}
	return fmt.Sprintf("\n\nThis worktree's dependencies are installed with '%s'. Run it again only after "+
		"changing a file it reads, such as a manifest or a lockfile.\n", cmd)
}

// requiresNote tells a worker that what the project needs of the machine is its requires list (see
// requires.go), which it names with the entries it has now: a ticket that needs a tool, service or
// permission the list lacks adds an entry for it, commits it with the ticket, and installs it with that
// entry's own install, so that the next machine has it too; workers installed what they found missing
// by hand (Homebrew packages, Docker Desktop started), and nothing recorded it. A worker prompt that
// says so already (project.RequiresRule), as orchestra's template does, is told only what the list has.
func (o *Loop) requiresNote() string {
	list := o.requiresNow()
	has := "which has no entries yet"
	if len(list) > 0 {
		names := make([]string, len(list))
		for i, r := range list {
			names[i] = r.Name
		}
		has = "which has " + strings.Join(names, ", ") + " now"
	}
	where := fmt.Sprintf("\"requires\" in %s", project.SettingsFile)
	if strings.Contains(o.prompt, project.RequiresRule) {
		return fmt.Sprintf("\n\nThe project's requires list is %s, %s.%s\n", where, has, o.readmeNote())
	}
	return fmt.Sprintf("\n\nWhat this project needs of the machine (a tool, a service, a permission) is its requires "+
		"list, %s, %s. A ticket that needs one the list doesn't have adds an entry for it (a name, a check, and an "+
		"install command that runs without sudo, or a manual step for a person), commits it with the ticket, and "+
		"installs it with that entry's own install command, %s: what isn't on the list isn't on the next "+
		"machine. A manual step is asked of the maintainer as a question.%s\n", where, has, project.RequiresRule,
		o.readmeNote())
}

// readmeNote asks a worker that changes the requires list to write the README's section on what to
// install from it again, in the same commit, so that the people who read the README and the runs that
// read the list find the same; "" for a project with no README.md in its main checkout, which orchestra
// writes none for.
func (o *Loop) readmeNote() string {
	if _, err := os.Stat(filepath.Join(o.cfg.Repo, project.ReadmeName)); err != nil {
		return ""
	}
	return fmt.Sprintf(" A ticket that changes the list runs orchestra requires --readme, which writes %s's "+
		"section on what to install from it, and commits that with the list.", project.ReadmeName)
}

// workerCheckLog is the file in a worktree's .orchestra/run/ that checkNote has a worker save a check's
// whole output to: not checkLogName, which holds the output of the run's own check that failed there.
const workerCheckLog = "worker-check.log"

// checkNote tells a worker to run a check once, keeping its whole output in a file git ignores and
// printing its exit status, then to read the file, rather than to run the check again to see what it
// said: workers piped a check through tail, which hides its exit status, then ran it again, minutes
// of a full check each time, to learn whether it passed. Its example is the worker's own check
// (check_own) when the project has one, else its merge check (check_fast). A worker given its share
// of the machine's cores (see coresNote) is told so too.
func (o *Loop) checkNote() string {
	return fmt.Sprintf("\n\nRun a check (tests, linters, a build) once, its whole output saved to a file in "+
		"%s (never committed) and its exit status printed, as in '%s > %s 2>&1; echo \"exit $?\"', then read "+
		"the file's end or search it, rather than running the check again to see its output or whether it "+
		"passed. Piping a check through tail hides its exit status.%s\n",
		project.RunPath(""), o.checkCommand(), project.RunPath(workerCheckLog), o.coresNote())
}

// coresNote tells a worker that its environment gives it its share of the machine's cores (see
// cores.go), for its checks to keep to: " " and the sentence, or "" for a worker not given one, as
// one of another kind, or one whose hooks can't give it an environment.
func (o *Loop) coresNote() string {
	if !o.givesEnv() {
		return ""
	}
	return fmt.Sprintf(" Other workers' checks run on this machine beside yours: %s in your environment is your "+
		"share of its cores, so a check that runs things side by side (test packages, a linter's jobs, a build) "+
		"runs no more than that many at once; GOFLAGS has Go's commands keep to it already (-p).", CoresVar)
}

// givesEnv reports whether a worker's settings give it its environment (see workerEnv): its hooks'
// do, when the run's Reporter can give them one.
func (o *Loop) givesEnv() bool {
	_, ok := o.reporter.(EnvReporter)
	return ok && o.cfg.Agent().Hooks
}

// checkCommand is the check the notes on running one give as their example: the worker's own check
// (check_own) when the project has one, else its merge check (check_fast), else a placeholder.
func (o *Loop) checkCommand() string {
	switch {
	case o.cfg.CheckOwn != "":
		return o.cfg.CheckOwn
	case o.cfg.Check != "":
		return o.cfg.Check
	}
	return "<the check>"
}

// bashMaxTimeout is the longest Claude Code's Bash waits on a command in the foreground, in the
// milliseconds its timeout takes: 10 minutes, unless its settings raise it (see bashTimeoutEnv).
// Without a timeout it waits 2, then moves the command to the background.
const bashMaxTimeout = 600000

// bashDefaultTimeoutVar and bashMaxTimeoutVar are the variables in which Claude Code's settings give
// its Bash tool the timeout it waits on a command for when given none, and the longest it may be
// given, in milliseconds. Claude Code reads them from its environment at each command, its
// subagents' too, as they run in its process.
const (
	bashDefaultTimeoutVar = "BASH_DEFAULT_TIMEOUT_MS"
	bashMaxTimeoutVar     = "BASH_MAX_TIMEOUT_MS"
)

// bashTimeoutEnv is the environment that has a Claude worker's Bash, and its subagents' (the
// verifier), wait on a command as long as orchestra's merge check may take (checkTimeout) by default,
// rather than 2 minutes, without its being told to: workers ran checks with Bash's default timeout,
// which moved them to the background, then idled waiting on them. Its maximum is never lowered below
// Claude Code's own. An empty map for workers of other kinds.
func (o *Loop) bashTimeoutEnv() map[string]string {
	if !o.cfg.Agent().CheckTimeNote {
		return map[string]string{}
	}
	limit := o.checkTimeout().Milliseconds()
	return map[string]string{
		bashDefaultTimeoutVar: strconv.FormatInt(limit, 10),
		bashMaxTimeoutVar:     strconv.FormatInt(max(limit, bashMaxTimeout), 10),
	}
}

// checkTimeNote tells a Claude worker how to give a check its time. One whose settings give it its
// environment (see givesEnv) has Bash's default timeout at the merge check's limit (bashTimeoutEnv): it
// is told to pass none, as a shorter one would move the check to the background. One whose settings
// can't is told to pass Bash's maximum, and for a check that can take longer, as long as orchestra's
// merge check may (checkTimeout), to run it in the background with Monitor to wait on it. Workers ran
// checks with Bash's default 2 minutes, which moved them to the background, and waited with a long
// sleep, which Bash refuses. "" for workers of other kinds.
func (o *Loop) checkTimeNote() string {
	if !o.cfg.Agent().CheckTimeNote {
		return ""
	}
	limit := command.ShortDuration(o.checkTimeout())
	if o.givesEnv() {
		return fmt.Sprintf("\n\nGive a check its time: orchestra has set Bash's default timeout to %s, as long as "+
			"its merge check may take, so run every check in the foreground and pass Bash no timeout of your "+
			"own: a shorter one moves the check to the background when it runs out. Never wait on a check in "+
			"the background, nor with a long sleep, which Bash refuses.\n", limit)
	}
	log := project.RunPath(workerCheckLog)
	return fmt.Sprintf("\n\nGive a check its time: Bash waits 2 minutes on a command unless told otherwise, "+
		"then moves it to the background. Pass Bash timeout: %d, its maximum (10 minutes), for every check. "+
		"A check that can take longer (orchestra gives its merge check up to %s) is the one to run in the "+
		"background (run_in_background), its exit status in the file too, as in '%s > %s 2>&1; echo \"exit $?\" "+
		">> %s', and to wait on with Monitor, an until-loop that greps the file for its exit line: never with a "+
		"long sleep, which Bash refuses.\n",
		bashMaxTimeout, limit, o.checkCommand(), log, log)
}

// limitNote tells a Claude worker what to do at the account's usage limit, which Claude Code may let its
// turn run a little past, telling it so (its grace allowance): the run holds for the limit and has the
// worker carry on after the reset, while a ticket deferred or closed for it would be set aside or merged
// unfinished. "" for workers of other kinds.
func (o *Loop) limitNote() string {
	if !o.cfg.Agent().LimitNote {
		return ""
	}
	return "\n\nAt the account's usage limit, commit what you have and stop, without deferring or closing the " +
		"ticket for it: you'll carry on after the reset.\n"
}

// ticketPrompt is the first message of ticket id's worker, its standing rules in its system prompt:
// its ticket, and first (see firstNotes).
func ticketPrompt(id, first string) string {
	return fmt.Sprintf("Your ticket is %s. Your standing rules for it are in your system prompt "+
		"(a copy is in %s): follow them exactly, from claiming the ticket to closing it.\n",
		id, project.RunPath(project.RulesName)) + first
}

// firstNotes is what a new worker's first message says of its start alone, beside its ticket or its
// whole prompt: the spec ticket t comes from (see specNote), what the closed tickets blocking it handed
// on (see handoffNote), then what an earlier attempt left, earlier (see earlierNote).
func firstNotes(t Ticket, earlier string) string {
	notes := handoffNote(t) + earlier
	if s := specNote(t, "start"); s != "" {
		return "\n\n" + s + "\n" + notes
	}
	return notes
}

// Each hand-off a new worker is given is cut to handoffChars characters, from at most handoffMax tickets.
const (
	handoffChars = 600
	handoffMax   = 5
)

// handoffNote gives ticket t's new worker the hand-offs of the closed tickets blocking t, as bd show
// gives its dependencies: each one's ID, title and close reason, which the worker prompt asks a worker
// to write for the tickets built on its own (the interfaces, files, commands or decisions it changed),
// so that t's worker needn't rediscover them from the code. A blocker closed without a real one (see
// placeholderReason) is left out, as is a question, whose answer t's design holds already; each reason
// is cut to handoffChars, on one line, and at most handoffMax are given. "" when there is none: a ticket
// blocked by nothing, or a worker resumed, which was given them as it started.
func handoffNote(t Ticket) string {
	var lines []string
	for _, d := range t.Dependencies {
		if d.DependencyType != "blocks" || d.Status != StatusClosed || HasLabel(d, HumanLabel) {
			continue
		}
		reason := oneLine(d.CloseReason)
		if placeholderReason(reason) {
			continue
		}
		if r := []rune(reason); len(r) > handoffChars {
			reason = strings.TrimSpace(string(r[:handoffChars-1])) + "…"
		}
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", d.ID, oneLine(d.Title), reason))
		if len(lines) == handoffMax {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nThe closed tickets yours builds on handed on, as they closed, what it needs to know of them:\n" +
		strings.Join(lines, "\n") + "\n"
}

// placeholderReasons are the close reasons that hand nothing on, lower-cased and without the quotes or
// full stop around them: bd's own, for a ticket closed without one (Closed) and a question answered with
// bd human respond (Responded), and the worker prompt's for a ticket with nothing for the tickets built on it.
var placeholderReasons = []string{"", "closed", "responded", "nothing for dependents"}

// placeholderReason reports whether close reason r, on one line, is empty or one of placeholderReasons.
func placeholderReason(r string) bool {
	return slices.Contains(placeholderReasons, strings.ToLower(strings.Trim(r, "`'\". ")))
}

// oneLine is s on one line: its runs of spaces, tabs and line breaks one space each, and the control
// characters left out, as a ticket's text is anyone's to write and a worker of another kind is given its
// first message pasted.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// SpecKey is the metadata key naming the spec a ticket comes from: the committed spec's path in the
// repository, which a spec run's breakdown gives every issue it files.
const SpecKey = "spec"

// specNote tells ticket t's worker that t is part of the spec its metadata names (SpecKey): to read
// the sections of it that t cites before it does what (start, carry on), and to treat it as
// settled. It is one sentence, which goes in a new worker's first message and in what a resumed
// worker is told; "" when t names no spec, or one that isn't a path on one line: the metadata is
// anyone's to change, and a resumed worker is told what it is told in one line.
func specNote(t Ticket, what string) string {
	var path string
	if json.Unmarshal(t.MetadataField(SpecKey), &path) != nil {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsFunc(path, unicode.IsControl) {
		return ""
	}
	return fmt.Sprintf("%s is part of the spec at %s: read the sections of it that %s cites before you %s, "+
		"and treat the spec as settled.", t.ID, path, t.ID, what)
}

// rulesArgs writes ticket id's worker's standing rules to its worktree wt and returns the
// arguments that append them to a Claude worker's system prompt; nil for workers of other kinds.
func (o *Loop) rulesArgs(wt, id string) ([]string, error) {
	if !o.cfg.Agent().Rules {
		return nil, nil
	}
	path, err := project.WriteRules(wt, o.workerRules(id))
	if err != nil {
		return nil, fmt.Errorf("cannot write its standing rules to %s: %w", project.RunPath(project.RulesName), err)
	}
	return []string{"--append-system-prompt-file", path}, nil
}
