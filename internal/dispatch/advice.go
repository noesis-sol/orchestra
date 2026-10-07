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
	transcript, hooks := "", ""
	if o.reporter != nil {
		transcript = o.reporter.TranscriptTail(wt)
		hooks = o.reporter.HookRecord(wt).Evidence()
	}
	return organ.Deferral{ID: id, How: how, Ticket: show,
		Screen:     lastLines(o.agents.Screen(ctx, o.agentName(id), ""), 80),
		Transcript: transcript,
		Hooks:      hooks,
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
	// a worker sending to a full queue waits (Run doesn't: see offerTriage): 64 is far more
	// deferrals than are ever waiting at once.
	o.triageQ = make(chan queuedDeferral, 64)
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

// triageDeferred hands a deferred ticket to triage. Its evidence takes a bd, three git and a Herdr
// read, each allowed up to its time limit, so it is gathered only while triage takes deferrals.
func (o *Loop) triageDeferred(ctx context.Context, id, how, wt string) {
	if !o.triageTaking(ctx) {
		return
	}
	o.queueTriage(ctx, o.gatherDeferral(ctx, id, how, wt))
}

// triageTaking says whether triage takes a deferral: not without triage, after Ctrl+C, once the
// organs are skipped, or once FinishTriage has run (a worker may still be settling when the run ends).
func (o *Loop) triageTaking(ctx context.Context) bool {
	return o.triageQ != nil && ctx.Err() == nil && o.triageStop.Err() == nil && o.organCtx.Err() == nil
}

// queuedDeferral is a deferral in triage's queue, with the hold's generation (Loop.envGen) as it
// was queued.
type queuedDeferral struct {
	organ.Deferral
	gen uint64
}

// queueTriage hands a deferral to triage, unless triage no longer takes one.
func (o *Loop) queueTriage(ctx context.Context, d organ.Deferral) {
	if !o.triageTaking(ctx) {
		return
	}
	select {
	case o.triageQ <- queuedDeferral{d, o.envGen.Load()}:
	case <-o.triageStop.Done():
	case <-ctx.Done():
	}
}

// offerTriage is queueTriage for Run's own goroutine, which never waits for room in the queue:
// triage may be waiting to hand Run a verdict (blamed), and with the queue full neither would go
// on. A deferral the queue has no room for is dropped, and said.
func (o *Loop) offerTriage(ctx context.Context, d organ.Deferral) {
	if !o.triageTaking(ctx) {
		return
	}
	select {
	case o.triageQ <- queuedDeferral{d, o.envGen.Load()}:
	default:
		o.emit(Event{Kind: EvInfo, Ticket: d.ID, Text: fmt.Sprintf(
			"  %s is not triaged: %d deferrals wait for triage already", d.ID, cap(o.triageQ))})
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
func (o *Loop) triage(d queuedDeferral) {
	defer func() {
		if p := recover(); p != nil {
			o.emit(Event{Kind: EvWarn, Ticket: d.ID, Text: fmt.Sprintf("  TRIAGE_FAILED for %s: panic: %s (the stack is in %s)",
				d.ID, o.logPanic("triage of "+d.ID, p), o.cfg.LogPath)})
		}
	}()
	// Once the maintainer skips the organs, what is still queued is dropped without a word, and so
	// is the triage they stopped: they asked for it.
	if o.organCtx.Err() != nil {
		return
	}
	t, err := o.organs().Triage(o.organCtx, d.Deferral)
	// The verdict is written down even if the organs are skipped meanwhile, each bd call within its
	// time limit.
	ctx := context.Background()
	if err != nil {
		if o.organCtx.Err() == nil {
			o.emit(Event{Kind: EvWarn, Ticket: d.ID, Text: fmt.Sprintf(
				"  TRIAGE_FAILED for %s: %v", d.ID, FirstLine(err.Error()))})
		}
		return
	}
	o.appendNotes(ctx, d.ID, t.Note())
	o.emit(Event{
		Kind: EvTriage, Ticket: d.ID, Title: t.Summary, Detail: t.Cause + " · " + t.Confidence,
		Text: fmt.Sprintf("  triage %s: %s (%s confidence) - %s", d.ID, t.Cause, t.Confidence, t.Summary),
	})
	o.blamed(ctx, verdict{d.ID, t.Cause, t.Confidence, t.Summary, d.gen})
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
	var setAside, checks strings.Builder
	for _, id := range o.setAside() {
		show := o.tickets.Describe(ctx, id)
		setAside.WriteString(show + "\n")
		if f, ok := o.checkSaidOf(id); ok {
			checks.WriteString(f.evidence(id, c.Check, c.Base) + "\n")
		}
	}
	var closed strings.Builder
	for _, id := range o.closedInRun() {
		closed.WriteString(o.closedEvidence(ctx, id) + "\n")
	}
	failedChecks := ""
	if checks.Len() > 0 {
		failedChecks = organ.Section(tag, "What the check said of each ticket set aside after its check failed",
			checks.String())
	}
	fullChecked := ""
	if f, ok := o.fullCheckOf(); ok {
		fullChecked = organ.Section(tag, "The full check, run once on "+c.Base+" after the run's last merge",
			f.evidence(c.CheckFull, c.Base))
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
	// summary written from a ticket. orchestra's own line on the run comes last, after the evidence,
	// as Claude reads long inputs best with the request at the end.
	return organ.Section(tag, "The run's final line", final) +
		organ.Section(tag, "Orchestrator log for this run", strings.Join(o.log.RunLines(), "\n")) +
		organ.Section(tag, "Commits merged into "+c.Base+" in this run", commits) +
		organ.Section(tag, "Tickets closed in this run, merged or with no change to merge (ID, title, close reason)",
			closed.String()) +
		organ.Section(tag, "Tickets set aside in this run (bd show, including triage notes)", setAside.String()) +
		failedChecks + fullChecked +
		organ.Section(tag, "Tickets in progress when the run stopped", stopped.String()) +
		organ.Section(tag, "Tickets still ready", stillReady) +
		organ.Section(tag, "Permission prompts and compactions of this run's workers, as their hooks noted them",
			o.hookEvidence()) +
		organ.Section(tag, "What the run's organs (triage, the predictor and the like) cost", o.organCostEvidence()) +
		outside + feature +
		fmt.Sprintf("%s, on branch %s of %s, from %s to %s. Exit code %d (%s). Write its report.\n",
			scope, c.Base, c.Repo, o.started.Format("15:04"), time.Now().Format("15:04"), code, meaning)
}

// closeReasonWidth is how many characters of a closed ticket's close reason the reviewer is given:
// enough to say why it closed as it did, short enough for a run of many tickets.
const closeReasonWidth = 400

// closedEvidence is what the reviewer is told of ticket id, closed in this run: its ID, title and
// close reason, cut short, from which the reviewer tells a ticket that closed as it meant to from one
// that needs the maintainer.
func (o *Loop) closedEvidence(ctx context.Context, id string) string {
	t, err := o.tickets.Show(ctx, id)
	if err != nil {
		return fmt.Sprintf("%s: (bd show failed: %s)", id, FirstLine(err.Error()))
	}
	reason := strings.Join(strings.Fields(t.CloseReason), " ")
	if r := []rune(reason); len(r) > closeReasonWidth {
		reason = string(r[:closeReasonWidth-1]) + "…"
	}
	if reason == "" {
		reason = "(none given)"
	}
	return fmt.Sprintf("%s: %s\nClose reason: %s", id, t.Title, reason)
}

// maxDirs is the most directories the reviewer is given of those a ticket's commits change.
const maxDirs = 20

// evidence is what the reviewer is told of ticket id's failed check, check, which ran on its branch
// rebased onto base: how it failed, the lines that say what failed and the directories the ticket's
// own commits change, so the reviewer can tell a failure in its code from one elsewhere.
func (f checkFail) evidence(id, check, base string) string {
	var b strings.Builder
	if f.setup != "" {
		fmt.Fprintf(&b, "%s: the setup '%s', run before the check as the rebase changed the project's dependency "+
			"files, %s on %s rebased onto %s at %s, so the check did not run; its output is in %s.\n",
			id, f.setup, f.how, f.br, base, short(f.onto), f.output)
	} else {
		fmt.Fprintf(&b, "%s: '%s' %s on %s rebased onto %s at %s; its output is in %s.\n",
			id, check, f.how, f.br, base, short(f.onto), f.output)
	}
	switch {
	case len(f.said) == 0:
		b.WriteString("It printed nothing.\n")
	case f.saidEnd:
		b.WriteString("No line of its output says what failed; its last lines:\n")
	default:
		b.WriteString("The lines of its output that say what failed:\n")
	}
	for _, l := range f.said {
		b.WriteString("    " + l + "\n")
	}
	dirs := "none that git could list"
	if len(f.dirs) > 0 {
		var named []string
		for _, d := range f.dirs[:min(len(f.dirs), maxDirs)] {
			if d == "." {
				d = "the top level (.)"
			}
			named = append(named, d)
		}
		dirs = strings.Join(named, ", ")
		if more := len(f.dirs) - len(named); more > 0 {
			dirs += fmt.Sprintf(" and %d more", more)
		}
	}
	fmt.Fprintf(&b, "Directories that %s's own commits change (%s..%s): %s\n", id, short(f.onto), f.br, dirs)
	return b.String()
}

// Review has the reviewer write the run report, followed by the end of the full check's output when
// it failed and by what the run's organs cost, the report included.
func (o *Loop) Review(ctx context.Context, code int, final string) (string, error) {
	result, err := o.organs().Review(ctx, o.reviewInput(ctx, code, final))
	if err != nil {
		return "", err
	}
	full := ""
	if f, ok := o.fullCheckOf(); ok {
		full = f.report(o.cfg.CheckFull)
	}
	return fmt.Sprintf("# Orchestra run · %s %s–%s · %s%s\n\n%s\n%s%s", o.started.Format("2006-01-02"),
		o.started.Format("15:04"), time.Now().Format("15:04"), o.cfg.Base, ScopeLabel(o.cfg),
		strings.TrimSpace(result), full, o.organCostLine()), nil
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
