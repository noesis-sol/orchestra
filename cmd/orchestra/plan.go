package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// runPlan proposes blocks links between the open tickets that touch the same code, in the
// repository around dir, and adds them with --apply. It returns the exit code.
func runPlan(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "add the proposed links with bd dep add")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra plan [--apply]\n\n"+
			"Propose blocks links between the open tickets that touch the same code, so they run one after\n"+
			"the other: the higher-priority ticket first. Tickets are linked when they name the same\n"+
			"function, or, when either names none, the same file of at most %d lines. Without --apply it\n"+
			"only prints the proposal.\n\n", dispatch.SmallFileLines)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return dispatch.ExitOK
		}
		return dispatch.ExitSetup
	}
	if rest := fs.Args(); len(rest) > 0 {
		fmt.Fprintf(stderr, "orchestra plan: unexpected argument %q (see orchestra plan -h)\n", rest[0])
		return dispatch.ExitSetup
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
	links := dispatch.PlanLinks(open, existing, git.Git{}.TrackedFiles(ctx, repo), settings.Check, func(p string) int {
		b, _ := os.ReadFile(filepath.Join(repo, p)) // a file that can't be read counts as one not written yet
		return bytes.Count(b, []byte("\n"))
	})
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
	failed := 0
	for _, l := range links {
		fmt.Fprintf(stdout, "  %s (%s) waits for %s (%s): both touch %s\n",
			l.Blocked, prios[l.Blocked], l.Blocker, prios[l.Blocker], l.Why)
		if !*apply {
			continue
		}
		if err := tracker.AddBlock(ctx, l.Blocker, l.Blocked); err != nil {
			failed++
			fmt.Fprintf(stderr, "orchestra plan: bd dep add %s %s: %v\n", l.Blocked, l.Blocker, err)
		}
	}
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

// plural is n and the noun, which takes an s unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
