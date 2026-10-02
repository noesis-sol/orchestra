package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// lastLines keeps the end of a long text, where a worker's conclusion is.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// gatherDeferral collects the evidence for one deferred ticket.
func (o *Loop) gatherDeferral(ctx context.Context, id, how, wt string) organ.Deferral {
	c := o.cfg
	show := o.tickets.Describe(ctx, id)
	status := o.history.ShortStatus(ctx, wt)
	commits := o.history.OneLineLog(ctx, wt, c.Base+"..HEAD")
	stat := o.history.DiffStat(ctx, wt)
	return organ.Deferral{ID: id, How: how, Ticket: show,
		Screen: lastLines(o.agents.Screen(ctx, o.agentName(id), ""), 80),
		Worktree: "Uncommitted changes:\n" + orNone(status) + "\n\nCommits on the ticket branch:\n" +
			orNone(commits) + "\n\nDiff against its last commit:\n" + orNone(stat)}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return strings.TrimSpace(s)
}

// StartTriage runs triage in the background, one ticket at a time, so the loop never waits for it.
// Nobody closes triageQ: workers may still be sending when the run ends. FinishTriage cancels
// triageStop instead, and the goroutine triages what is already queued, then returns.
func (o *Loop) StartTriage() {
	// Triaging a ticket takes a model's answer, much longer than a worker takes to defer one, and
	// a worker sending to a full queue waits: 64 is far more deferrals than are ever waiting at once.
	o.triageQ = make(chan organ.Deferral, 64)
	o.triageStop, o.triageFinish = context.WithCancel(context.Background())
	o.triageDone = make(chan struct{})
	go func() {
		defer close(o.triageDone)
		for {
			select {
			case d := <-o.triageQ:
				o.triage(d)
			case <-o.triageStop.Done():
				for {
					select {
					case d := <-o.triageQ:
						o.triage(d)
					default:
						return
					}
				}
			}
		}
	}()
}

// queueTriage hands a deferral to triage. It does nothing without triage, after Ctrl+C, or once
// FinishTriage has run: a worker may still be settling when the run ends.
func (o *Loop) queueTriage(ctx context.Context, d organ.Deferral) {
	if o.triageQ == nil || ctx.Err() != nil || o.triageStop.Err() != nil {
		return
	}
	select {
	case o.triageQ <- d:
	case <-o.triageStop.Done():
	case <-ctx.Done():
	}
}

// FinishTriage waits for queued triage to finish, or for ctx to be cancelled. Nothing is queued
// after it.
func (o *Loop) FinishTriage(ctx context.Context) {
	if o.triageQ == nil {
		return
	}
	o.triageFinish()
	select {
	case <-o.triageDone:
	case <-ctx.Done():
	}
}

// triage has the triage organ judge one deferred ticket and writes its verdict down. A panic loses
// that verdict, not the run.
func (o *Loop) triage(d organ.Deferral) {
	defer func() {
		if p := recover(); p != nil {
			o.emit(Event{Kind: EvWarn, Ticket: d.ID, Text: fmt.Sprintf("  TRIAGE_FAILED for %s: panic: %s (the stack is in %s)",
				d.ID, o.logPanic("triage of "+d.ID, p), o.cfg.LogPath)})
		}
	}()
	t, err := o.organ.Triage(o.organCtx, d)
	// The verdict is written down even if the organs are skipped meanwhile, each bd call within its
	// time limit.
	ctx := context.Background()
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: d.ID, Text: fmt.Sprintf(
			"  TRIAGE_FAILED for %s: %v", d.ID, FirstLine(err.Error()))})
		return
	}
	o.appendNotes(ctx, d.ID, t.Note())
	o.emit(Event{
		Kind: EvTriage, Ticket: d.ID, Title: t.Summary, Detail: t.Cause + " · " + t.Confidence,
		Text: fmt.Sprintf("  triage %s: %s (%s confidence) - %s", d.ID, t.Cause, t.Confidence, t.Summary),
	})
	o.blamed(ctx, verdict{d.ID, t.Cause, t.Confidence, t.Summary})
}

// FirstLine returns the first line of s, trimmed: enough of an error for a one-line message.
func FirstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// reviewInput gathers the evidence for the reviewer, each section tagged with the same fresh ID.
func (o *Loop) reviewInput(ctx context.Context, code int, final string) string {
	c := o.cfg
	tag := organ.EvidenceID()
	commits := o.history.Subjects(ctx, c.Repo, o.startHead+".."+c.Base)
	var setAside strings.Builder
	for _, id := range o.setAside() {
		show := o.tickets.Describe(ctx, id)
		setAside.WriteString(show + "\n")
	}
	var stopped strings.Builder
	for _, st := range o.activeList() {
		show := o.tickets.Describe(ctx, st.Ticket)
		fmt.Fprintf(&stopped, "%s was in progress in Herdr tab %s when the run stopped.\n\n"+
			"%s\n\nEnd of its worker's terminal:\n%s\n\n",
			st.Ticket, st.Tab, show, lastLines(o.agents.Screen(ctx, o.agentName(st.Ticket), ""), 60))
	}
	stillReady := "unknown, bd could not list them"
	if ready, err := o.tickets.Ready(ctx, c.Ticket); err == nil {
		stillReady = fmt.Sprintf("%d", len(ready))
	}
	meaning := exitMeaning(code)
	if strings.HasPrefix(final, "DRAINED") {
		meaning = "the maintainer asked the run to stop after its running tickets, and they finished"
	}
	scope, outside, feature := "Run", "", ""
	if c.Ticket != "" {
		scope = fmt.Sprintf("Run of ticket %s and its subtickets only", c.Ticket)
		if c.Feature != "" {
			scope = fmt.Sprintf("Run of epic %s and its subtickets only, filed from a feature request (orchestra "+
				"--feature) just before the run", c.Ticket)
			feature = organ.Section(tag, "The feature request epic "+c.Ticket+" was planned from", c.Feature)
		}
		if subs, err := o.tickets.Descendants(ctx, c.Ticket); err == nil {
			if filed, err := o.filedOutside(ctx, subs); err == nil {
				for _, t := range filed {
					outside += o.tickets.Describe(ctx, t.ID) + "\n"
				}
				outside = organ.Section(tag, fmt.Sprintf(
					"Follow-ups filed in this run outside the scope of %s, left for a later run", c.Ticket), outside)
			}
		}
	}
	// The final line goes inside the tags: it can quote what the run gathered, such as a triage
	// summary written from a ticket.
	return fmt.Sprintf("%s, on branch %s of %s, from %s to %s. Exit code %d (%s).\n\n",
		scope, c.Base, c.Repo, o.started.Format("15:04"), time.Now().Format("15:04"), code, meaning) +
		organ.Section(tag, "The run's final line", final) +
		organ.Section(tag, "Orchestrator log for this run", strings.Join(o.log.RunLines(), "\n")) +
		organ.Section(tag, "Commits merged into "+c.Base+" in this run", commits) +
		organ.Section(tag, "Tickets set aside in this run (bd show, including triage notes)", setAside.String()) +
		organ.Section(tag, "Tickets in progress when the run stopped", stopped.String()) +
		organ.Section(tag, "Tickets still ready", stillReady) + outside + feature
}

// Review has the reviewer write the run report.
func (o *Loop) Review(ctx context.Context, code int, final string) (string, error) {
	result, err := o.organ.Review(ctx, o.reviewInput(ctx, code, final))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Orchestra run · %s %s–%s · %s%s\n\n%s\n", o.started.Format("2006-01-02"),
		o.started.Format("15:04"), time.Now().Format("15:04"), o.cfg.Base, ScopeLabel(o.cfg),
		strings.TrimSpace(result)), nil
}

// SaveReport writes the run report to the reports folder and returns its path.
func (o *Loop) SaveReport(report string) (string, error) {
	dir := o.cfg.ReportsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, o.started.Format("2006-01-02-150405")+".md")
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func exitMeaning(code int) string {
	switch code {
	case ExitOK:
		return "the queue was empty or the limit was reached; " +
			"a run of one ticket says in its final line whether all of it is merged"
	case ExitStuck:
		return "a worker was blocked, paused or ran past its time limit and needs attention"
	case ExitTool:
		return "a Herdr, Beads or git command failed"
	case ExitDirty:
		return "the main checkout had uncommitted changes or left its branch"
	case ExitMerge:
		return "a finished ticket's branch did not fast-forward"
	case ExitEnvironment:
		return "workers kept failing at once, whichever ticket they had: the environment, not the tickets"
	case ExitInterrupted:
		return "stopped with Ctrl+C"
	}
	return "unknown"
}
