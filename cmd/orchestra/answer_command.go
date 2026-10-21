package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// answerSynopsis is orchestra answer's flags and argument, as its usage line gives them (see
// subcommand).
const answerSynopsis = "[--ticket <id>] [<question>]"

// runAnswer runs the answer talk (answer_talk.go) on this terminal, about the questions open in the
// project around e's dir: every one, those of --ticket's scope, or the one named. It is the way to
// answer where the dashboard can't open the talk beside it (orchestra runs in no pane, or plainly) or
// once a run is over; a run going on takes an answered question's ticket back as it next reads bd. It
// returns the exit code.
//
// A stop signal before claude starts stops it, saying so, exit code 130. claude then takes the
// terminal, Ctrl+C included, which is claude's to interrupt with: so runAnswer goes by a stopWatch of
// its own, which drops SIGINT meanwhile (claudeOnTerminal), rather than the context its stopCatch
// gives, which any stop signal cancels; SIGTERM and SIGHUP still end claude, and orchestra with it.
func runAnswer(ctx context.Context, args []string, e cmdEnv, _ stopCatch) int {
	stdout, stderr := e.stdout, e.stderr
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	scope := fs.String("ticket", "", "only the questions blocking this `id` or a ticket under it, or under it themselves")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usageLines("Usage: ", "answer", answerSynopsis)+"\n"+
			"Answer the questions workers asked (issues labelled human) with claude, on this terminal: every\n"+
			"open one, those of --ticket's scope, or the one named. claude asks each with its options, the\n"+
			"recommended one first, talks it through if you ask, and records your answer with bd, which closes\n"+
			"the question: the ticket waiting on it comes back to the run going on, or to the next. Type /exit\n"+
			"to leave the rest open. In a run's dashboard, a opens the same talk in a pane beside it.\n\n")
		printFlags(fs)
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 talked through (or none open), 2 it couldn't start, 130 stopped.\n")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return dispatch.ExitOK
		}
		return dispatch.ExitSetup
	}
	var named string
	switch rest := fs.Args(); {
	case len(rest) > 1:
		fmt.Fprintf(stderr, "orchestra answer: unexpected argument %q (see orchestra answer -h)\n", rest[1])
		return dispatch.ExitSetup
	case len(rest) == 1 && *scope != "":
		fmt.Fprintln(stderr, "orchestra answer: give a question or --ticket, not both")
		return dispatch.ExitSetup
	case len(rest) == 1:
		named = rest[0]
	}
	c, problems := answerOptions(ctx, e)
	if len(problems) > 0 {
		fmt.Fprintln(stderr, "orchestra answer cannot start:")
		for _, p := range problems {
			fmt.Fprintln(stderr, "  - "+p)
		}
		return dispatch.ExitSetup
	}
	stops := catchStops()
	defer stops.release()
	ctx, stop := stops.context(ctx)
	defer stop()
	a := answerTalkIn(c, "") // on this terminal
	a.out, a.err = stdout, stderr
	a.terminal = func(ctx context.Context, message string) error {
		return answerOnTerminal(ctx, stops, a.claude, message, e.stdin, stdout, stderr)
	}
	r, err := a.answer(ctx, *scope, named)
	const backTo = "the run going on, or to the next" // where the answered questions' tickets go
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(stderr, "orchestra answer: stopped")
		sayAnswered(stdout, r, backTo)
		return dispatch.ExitInterrupted
	case err != nil:
		fmt.Fprintln(stderr, "orchestra answer:", err)
		sayAnswered(stdout, r, backTo)
		return dispatch.ExitSetup
	case len(r.Answered)+len(r.Open) == 0:
		where := ""
		if *scope != "" {
			where = " about " + *scope
		}
		fmt.Fprintf(stdout, "No question is open%s.\n", where)
		return dispatch.ExitOK
	}
	sayAnswered(stdout, r, backTo)
	return dispatch.ExitOK
}

// answerOptions is what the answer talk needs of the project around e's dir, from its settings as a
// run reads them without flags: its main checkout, and the effort, MCP servers and Chrome its own
// Claude Code sessions get. problems are what stops it, as they would a run.
func answerOptions(ctx context.Context, e cmdEnv) (options, []string) {
	var c options
	if problems := findRepo(ctx, &c, e.dir); len(problems) > 0 {
		return c, problems
	}
	settings, problems := loadSettings(c.Repo)
	if len(problems) > 0 {
		return c, problems
	}
	return c, resolveProject(ctx, &c, &runFlags{}, settings, e.getenv)
}

// sayAnswered says how the talk left its questions: those answered, whose tickets come back to
// backTo, and those still open; nothing when it asked none.
func sayAnswered(out io.Writer, r answerResult, backTo string) {
	if len(r.Answered) > 0 {
		fmt.Fprintf(out, "Answered %s: the tickets waiting on %s come back to %s.\n",
			strings.Join(r.Answered, ", "), itOrThem(len(r.Answered)), backTo)
	}
	if len(r.Open) > 0 {
		fmt.Fprintf(out, "Still open: %s. orchestra answer asks %s again.\n", strings.Join(r.Open, ", "),
			itOrThem(len(r.Open)))
	}
}

// itOrThem is the pronoun for n questions.
func itOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
