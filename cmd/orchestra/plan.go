package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"strings"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// planSynopsis is orchestra plan's flags, as its usage line gives them (see subcommand).
const planSynopsis = "[--apply]"

// runPlan proposes blocks links between the open tickets that touch the same code, in the
// repository around dir, and adds them with --apply. It returns the exit code.
func runPlan(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "add the proposed links with bd dep add")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), usageLines("Usage: ", "plan", planSynopsis)+"\n"+
			"Propose blocks links between the open tickets that touch the same code, so they run one after\n"+
			"the other: the higher-priority ticket first. Tickets are linked when they name the same\n"+
			"function, or work on the same file of at most %d lines, unless both name functions it\n"+
			"mentions and none is the same. Without --apply it only prints the proposal.\n\n", dispatch.SmallFileLines)
		printFlags(fs)
	}
	if code, ok := parseArgs(fs, args, "plan"); !ok {
		return code
	}
	repo, err := git.Git{}.TopLevel(ctx, dir)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra plan: not inside a git repository")
		return dispatch.ExitSetup
	}
	settings, _, err := project.LoadSettings(repo)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra plan:", err)
		return dispatch.ExitSetup
	}
	types, err := project.ResolveExcludeTypes(settings)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra plan:", err)
		return dispatch.ExitSetup
	}

	tracker := beads.Tracker{Repo: repo, ExcludeTypes: types}
	open, existing, err := tracker.Open(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra plan: cannot read the open tickets:", err)
		return dispatch.ExitTool
	}
	tracked, err := git.Git{}.TrackedFiles(ctx, repo)
	if err != nil { // the footprints go by the names alone
		fmt.Fprintln(stderr, "orchestra plan: cannot list the files git tracks:", err)
		tracked = nil
	}
	// a footprint leaves out the files any of the checks names
	links := dispatch.PlanLinks(open, existing, tracked, settings.CheckCommands(),
		func(file string) ([]byte, error) { return dispatch.ReadSource(repo, file) }, mergesByUnion(ctx, repo))
	if len(links) == 0 {
		fmt.Fprintf(stdout, "No links to add: no two of the %s touch the same code without being ordered already.\n",
			plural(len(open), "open ticket"))
		return dispatch.ExitOK
	}
	prios := map[string]string{}
	for _, t := range open {
		prios[t.ID] = fmt.Sprintf("P%d", dispatch.PriorityOf(t))
	}
	verb := "Proposed"
	if *apply {
		verb = "Adding"
		// A running loop holds back the tickets it hasn't started yet, but one it has started keeps going
		// whatever it now waits for. A lock that can't be read warns of nothing.
		if h, held, _ := project.RunHolder(ctx, repo); held {
			fmt.Fprintf(stderr, "orchestra plan: warning: %v. A ticket it has started keeps going and may merge "+
				"before a ticket it now waits for.\n", &project.HeldError{Repo: repo, Holder: h})
		}
	}
	fmt.Fprintf(stdout, "%s %s between the %s:\n",
		verb, plural(len(links), "blocks link"), plural(len(open), "open ticket"))
	// A stop signal stops --apply between two links, saying which it added and which not; without the
	// catch, Go would end it with no word between two bd dep add. The link under way is added whole:
	// its bd runs on the parent context, in a process group of its own, out of the terminal's Ctrl+C.
	stop, unwatch := ctx, func() {}
	if *apply {
		stop, unwatch = signal.NotifyContext(ctx, stopSignals...)
	}
	defer unwatch()
	var added, notAdded []string
	for i, l := range links {
		if stop.Err() != nil && ctx.Err() == nil {
			for _, l := range links[i:] {
				notAdded = append(notAdded, waits(l))
			}
			return stoppedPlan(stderr, added, notAdded)
		}
		fmt.Fprintf(stdout, "  %s (%s) waits for %s (%s): both touch %s\n",
			l.Blocked, prios[l.Blocked], l.Blocker, prios[l.Blocker], l.Why)
		if !*apply {
			continue
		}
		if err := tracker.AddBlock(ctx, l.Blocker, l.Blocked); err != nil {
			notAdded = append(notAdded, waits(l))
			fmt.Fprintf(stderr, "orchestra plan: bd dep add %s %s: %v\n", l.Blocked, l.Blocker, err)
			continue
		}
		added = append(added, waits(l))
	}
	failed := len(notAdded)
	switch {
	case !*apply:
		fmt.Fprintln(stdout, "Nothing changed. Add them with: orchestra plan --apply")
	case failed > 0:
		fmt.Fprintf(stdout, "Added %d of %s.\n", len(links)-failed, plural(len(links), "link"))
		return dispatch.ExitTool
	default:
		fmt.Fprintf(stdout, "Added %s.\n", plural(len(links), "link"))
	}
	return dispatch.ExitOK
}

// waits names link l as stoppedPlan lists it: "k-2 waits for k-1".
func waits(l dispatch.Link) string { return l.Blocked + " waits for " + l.Blocker }

// stoppedPlan says that a stop signal stopped plan --apply with the links added and those not, and
// returns exit code 130.
func stoppedPlan(stderr io.Writer, added, notAdded []string) int {
	total := len(added) + len(notAdded)
	line := fmt.Sprintf("orchestra plan: stopped. Added %d of %s", len(added), plural(total, "link"))
	if len(added) > 0 {
		line += ": " + strings.Join(added, ", ")
	}
	fmt.Fprintf(stderr, "%s. Not added: %s.\n", line, strings.Join(notAdded, ", "))
	return dispatch.ExitInterrupted
}

// mergesByUnion reports whether git merges a file of repo by union, as .gitattributes says (git
// check-attr merge), asking git once a file. A file git can't answer for isn't merged by union.
func mergesByUnion(ctx context.Context, repo string) func(file string) bool {
	unions := map[string]bool{}
	return func(file string) bool {
		u, ok := unions[file]
		if !ok {
			merge, err := git.Git{}.Attribute(ctx, repo, "merge", file)
			u = err == nil && merge == "union"
			unions[file] = u
		}
		return u
	}
}

// plural is n and the noun, which takes an s unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
