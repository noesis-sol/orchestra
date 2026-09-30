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
func (o *Loop) gatherDeferral(id, title, how, wt string) organ.Deferral {
	c := o.cfg
	show := o.tickets.Describe(id)
	status := o.history.ShortStatus(wt)
	commits := o.history.OneLineLog(wt, c.Base+"..HEAD")
	stat := o.history.DiffStat(wt)
	return organ.Deferral{ID: id, Title: title, How: how, Ticket: show,
		Screen: lastLines(o.agents.Screen(o.agentName(id), ""), 80),
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
func (o *Loop) StartTriage() {
	o.triageMu.Lock()
	o.triageOn = true
	o.triageWake = make(chan struct{}, 1)
	o.triageDone = make(chan struct{})
	o.triageMu.Unlock()
	go func() {
		defer close(o.triageDone)
		for {
			d, ok, finished := o.nextTriage()
			switch {
			case ok:
				o.triage(d)
			case finished:
				return
			default:
				<-o.triageWake
			}
		}
	}()
}

// nextTriage takes the oldest queued deferral; finished says nothing more will be queued.
func (o *Loop) nextTriage() (d organ.Deferral, ok, finished bool) {
	o.triageMu.Lock()
	defer o.triageMu.Unlock()
	if len(o.triageQ) > 0 {
		d, o.triageQ = o.triageQ[0], o.triageQ[1:]
		return d, true, false
	}
	return d, false, o.triageClosed
}

// queueTriage hands a deferral to triage without waiting. It does nothing without triage, after
// Ctrl+C, or once FinishTriage has run: a worker may still be settling when the run ends.
func (o *Loop) queueTriage(ctx context.Context, d organ.Deferral) {
	if ctx.Err() != nil {
		return
	}
	o.triageMu.Lock()
	defer o.triageMu.Unlock()
	if !o.triageOn || o.triageClosed {
		return
	}
	o.triageQ = append(o.triageQ, d)
	o.wakeTriage()
}

// wakeTriage tells the triage goroutine there is news. The caller holds triageMu.
func (o *Loop) wakeTriage() {
	select {
	case o.triageWake <- struct{}{}:
	default: // already told
	}
}

// FinishTriage waits for queued triage to finish, or for ctx to be cancelled. Nothing is queued
// after it.
func (o *Loop) FinishTriage(ctx context.Context) {
	o.triageMu.Lock()
	if !o.triageOn {
		o.triageMu.Unlock()
		return
	}
	o.triageClosed = true
	o.wakeTriage()
	o.triageMu.Unlock()
	select {
	case <-o.triageDone:
	case <-ctx.Done():
	}
}

func (o *Loop) triage(d organ.Deferral) {
	t, err := o.organ.Triage(o.organCtx, d)
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: d.ID, Text: fmt.Sprintf("  TRIAGE_FAILED for %s: %v", d.ID, FirstLine(err.Error()))})
		return
	}
	o.appendNotes(d.ID, t.Note())
	o.emit(Event{Kind: EvTriage, Ticket: d.ID, Title: t.Summary, Detail: t.Cause + " · " + t.Confidence, Text: fmt.Sprintf(
		"  triage %s: %s (%s confidence) - %s", d.ID, t.Cause, t.Confidence, t.Summary)})
}

// FirstLine returns the first line of s, trimmed: enough of an error for a one-line message.
func FirstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// reviewInput gathers the evidence for the reviewer.
func (o *Loop) reviewInput(code int, final string) string {
	c := o.cfg
	commits := o.history.Subjects(c.Repo, o.startHead+".."+c.Base)
	var setAside strings.Builder
	for _, id := range o.setAside() {
		show := o.tickets.Describe(id)
		setAside.WriteString(show + "\n")
	}
	var stopped strings.Builder
	for _, st := range o.activeList() {
		show := o.tickets.Describe(st.Ticket)
		fmt.Fprintf(&stopped, "%s was in progress in Herdr tab %s when the run stopped.\n\n%s\n\nEnd of its worker's terminal:\n%s\n\n",
			st.Ticket, st.Tab, show, lastLines(o.agents.Screen(o.agentName(st.Ticket), ""), 60))
	}
	ready, _ := o.tickets.Ready()
	return fmt.Sprintf("Run on branch %s of %s, from %s to %s. Exit code %d (%s). Final line: %s\n\n",
		c.Base, c.Repo, o.started.Format("15:04"), time.Now().Format("15:04"), code, exitMeaning(code), final) +
		organ.Section("Orchestrator log for this run", strings.Join(o.log.RunLines(), "\n")) +
		organ.Section("Commits merged into "+c.Base+" in this run", commits) +
		organ.Section("Tickets set aside in this run (bd show, including triage notes)", setAside.String()) +
		organ.Section("Tickets in progress when the run stopped", stopped.String()) +
		organ.Section("Tickets still ready", fmt.Sprintf("%d", len(ready)))
}

// Review has the reviewer write the run report.
func (o *Loop) Review(ctx context.Context, code int, final string) (string, error) {
	result, err := o.organ.Review(ctx, o.reviewInput(code, final))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# Orchestra run · %s %s–%s · %s\n\n%s\n", o.started.Format("2006-01-02"),
		o.started.Format("15:04"), time.Now().Format("15:04"), o.cfg.Base, strings.TrimSpace(result)), nil
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
		return "the queue was empty or the limit was reached"
	case ExitStuck:
		return "a worker was blocked, paused or ran past its time limit and needs attention"
	case ExitTool:
		return "a Herdr, Beads or git command failed"
	case ExitDirty:
		return "the main checkout had uncommitted changes or left its branch"
	case ExitMerge:
		return "a finished ticket's branch did not fast-forward"
	case ExitInterrupted:
		return "stopped with Ctrl+C"
	}
	return "unknown"
}

// setAside lists tickets deferred or left unmerged in this run, in order, without repeats.
func (o *Loop) setAside() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.asideIDs...)
}

func (o *Loop) markAside(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, x := range o.asideIDs {
		if x == id {
			return
		}
	}
	o.asideIDs = append(o.asideIDs, id)
}
