package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/load"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A spec broken down for the first time has its environment ticket filed first (spec-prompt.md's The
// environment first): the project's setup, labelled solo, priority 0, which every other ticket waits
// for. Nothing else can start before it merges, so the run starts on it as soon as the breakdown has
// filed it, rather than wait for the whole tree with every slot idle: the loop takes that ticket
// alone (dispatch.Filing) until the breakdown is over and its tree checked, and then the rest, as
// ready tickets. A breakdown that fails, or is stopped, ends the run as it would before the loop,
// the loop stopped as Ctrl+C stops it, leaving its workers. Plain output prints the breakdown's
// lines and the loop's as they come; a terminal prints the loop's above the breakdown's busy line,
// and opens the dashboard once the breakdown is over. An update of a spec's tree, and a breakdown
// with the user (--refine), still run before the loop.

// envStart is a spec's environment ticket, under the root epic, as bd had it when the run started on it.
type envStart struct {
	root string
	env  dispatch.Ticket
}

// envWatch finds the environment ticket among the issues a breakdown files, as it files them
// (breakdownRequest.Filed): the first issue is the root epic, and the first ticket under it (past
// the epics under it) is the environment ticket, if it is labelled solo, has priority 0 and is open.
// Anything else, or bd failing to show an issue, and it looks no further: the run waits for the
// whole breakdown, as without it.
type envWatch struct {
	bd specTracker

	mu   sync.Mutex
	ids  []string      // filed, not yet looked at
	wake chan struct{} // a send once ids has more
}

func newEnvWatch(bd specTracker) *envWatch {
	return &envWatch{bd: bd, wake: make(chan struct{}, 1)}
}

// filed takes the IDs of issues the breakdown filed, without waiting on bd.
func (w *envWatch) filed(ids ...string) {
	w.mu.Lock()
	w.ids = append(w.ids, ids...)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// take returns the IDs filed since it was last called.
func (w *envWatch) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := w.ids
	w.ids = nil
	return ids
}

// watch looks at each issue filed, in order, until it has found the environment ticket, which it
// sends on found, or knows there is none, or ctx ends.
func (w *envWatch) watch(ctx context.Context, found chan<- envStart) {
	root, tree := "", map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}
		for _, id := range w.take() {
			t, err := w.bd.Show(ctx, id)
			switch {
			case err != nil:
				return
			case root == "":
				if t.IssueType != "epic" || t.Parent != "" {
					return
				}
				root, tree[id] = id, true
			case !tree[t.Parent]: // not in the tree: not the run's
			case t.IssueType == "epic":
				tree[id] = true
			default: // the first ticket in the tree
				if dispatch.HasLabel(t, dispatch.SoloLabel) && t.Priority != nil && *t.Priority == 0 &&
					t.Status == dispatch.StatusOpen {
					found <- envStart{root: root, env: t}
				}
				return
			}
		}
	}
}

// specFiling is a spec's breakdown going on as the loop starts on its environment ticket.
type specFiling struct {
	envStart
	opened chan struct{}   // closed once what the breakdown filed may run: dispatch.Filing's Done
	over   chan struct{}   // closed once the breakdown is over, checked or not, code set
	code   int             // ExitOK, or the code a breakdown that failed or was stopped ends the run with
	failed context.Context // cancelled once the breakdown failed or was stopped, its cause saying so
	fail   context.CancelCauseFunc
	stop   context.CancelFunc // stops the breakdown, as a stop signal does
}

func newSpecFiling(e envStart, stop context.CancelFunc) *specFiling {
	failed, fail := context.WithCancelCause(context.Background())
	return &specFiling{envStart: e, opened: make(chan struct{}), over: make(chan struct{}), failed: failed,
		fail: fail, stop: stop}
}

// gate is the loop's dispatch.Filing: the environment ticket alone until the breakdown is over.
func (f *specFiling) gate() *dispatch.Filing {
	return &dispatch.Filing{First: f.env.ID, Done: f.opened}
}

// end ends the filing with the breakdown's code, why being how the loop says it stopped when that
// isn't ExitOK: the rest joins the run, or the loop is stopped.
func (f *specFiling) end(code int, why string) {
	f.code = code
	if code == dispatch.ExitOK {
		close(f.opened)
	} else {
		f.fail(dispatch.InterruptedError(why))
	}
	close(f.over)
	f.stop() // the breakdown is over: this only lets go of its context
}

// stopBreakdown stops the breakdown going on, if any, as a stop signal stops the loop.
func (f *specFiling) stopBreakdown() {
	if f != nil {
		f.stop()
	}
}

// bind has the breakdown failing, or being stopped, cancel the loop's context with its cause, until
// the function it returns is called. With no filing, it does nothing.
func (f *specFiling) bind(cancel context.CancelCauseFunc) (unbind func() bool) {
	if f == nil {
		return func() bool { return false }
	}
	return context.AfterFunc(f.failed, func() { cancel(context.Cause(f.failed)) })
}

// result is the run's exit code, once the loop ended with code: the breakdown's, once it is over, if
// it failed or was stopped.
func (f *specFiling) result(code int) int {
	if f == nil {
		return code
	}
	if <-f.over; f.code != dispatch.ExitOK {
		return f.code
	}
	return code
}

// breakDownEarly has the spec broken down, as req asks, and returns as soon as the breakdown has
// filed the spec's environment ticket, with the root epic and the specFiling the breakdown goes on
// in; or once the breakdown is over, without one, as start does.
func (r specRun) breakDownEarly(ctx context.Context, req breakdownRequest) (string, int, *specFiling) {
	w := newEnvWatch(r.tracker)
	req.Filed = w.filed
	type outcome struct {
		res breakdownResult
		err error
	}
	broke := make(chan outcome, 1)
	go func() {
		res, err := r.breakdown.BreakDown(ctx, req)
		broke <- outcome{res, err}
	}()
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	found := make(chan envStart, 1)
	go w.watch(watchCtx, found)
	select {
	case o := <-broke:
		root, code := r.broken(ctx, req.Spec, o.res, o.err)
		return root, code, nil
	case e := <-found:
		f := newSpecFiling(e, r.cancel)
		line := fmt.Sprintf("SPEC: the run starts on %s, the environment ticket, while the breakdown files the rest of %s",
			e.env.ID, e.root)
		r.say.Say(line, fmt.Sprintf("Starting on %s, the environment ticket, while the breakdown files the rest",
			e.env.ID))
		r.logLine(line)
		go func() {
			o := <-broke
			f.end(r.brokenEarly(ctx, req.Spec, o.res, o.err, e))
		}()
		return e.root, dispatch.ExitOK, f
	}
}

// brokenEarly is broken for a breakdown the run started on before it was over, with e, its
// environment ticket: what the breakdown filed must be under the same root epic. It returns the
// exit code, and when that isn't ExitOK how the loop says it stopped.
func (r specRun) brokenEarly(ctx context.Context, spec project.Spec, res breakdownResult, err error,
	e envStart) (int, string) {
	const failed = "because the spec's breakdown failed"
	root, code := r.broken(ctx, spec, res, err)
	switch {
	case code == dispatch.ExitInterrupted:
		return code, "as the spec's breakdown was stopped"
	case code != dispatch.ExitOK:
		return code, failed
	case root != e.root:
		detail := fmt.Sprintf("it named %s as the root epic, after the run started on %s", root, e.root)
		fmt.Fprintln(r.err, "orchestra won't run what the spec's breakdown filed:", detail)
		r.logLine("SPEC: not run: " + detail)
		return r.unfinished(ctx, spec, e.root), failed
	}
	r.sayChanged(ctx, e.env)
	return dispatch.ExitOK, ""
}

// sayChanged warns when the breakdown changed the environment ticket, as it was when the run started
// on it (was), after that: a worker started on it before then works from what it read then.
func (r specRun) sayChanged(ctx context.Context, was dispatch.Ticket) {
	now, err := r.tracker.Show(ctx, was.ID)
	if err != nil || now.Title == was.Title && now.Description == was.Description && now.Design == was.Design &&
		now.AcceptanceCriteria == was.AcceptanceCriteria {
		return
	}
	w := fmt.Sprintf("the breakdown changed %s after the run started on it: a worker started on it before then "+
		"works from it as it was (bd show %s)", was.ID, was.ID)
	fmt.Fprintln(r.err, "warning: "+w)
	r.logLine("SPEC: warning: " + w)
}

// lineLocked is stdout and stderr, written one Write at a time between them: the loop's lines and
// the breakdown's, as they come beside each other.
func lineLocked(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	mu := &sync.Mutex{}
	return lockedWriter{mu: mu, w: stdout}, lockedWriter{mu: mu, w: stderr}
}

// lockedWriter is w, written to under mu.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// heldSink is the loop's sink on a terminal while the spec's breakdown goes on: it prints each event
// above the breakdown's busy line, and keeps it, and each status, for the dashboard that opens once
// the breakdown is over (handTo). The machine's load is dropped until then.
type heldSink struct {
	out tui.Printer

	mu   sync.Mutex
	kept []any // the events and statuses, in order
	next dashboardSink
}

// dashboardSink is a sink that shows the machine's load, as the dashboard's does.
type dashboardSink interface {
	dispatch.Sink
	dispatch.LoadSink
}

func (h *heldSink) Event(ev dispatch.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.next != nil {
		h.next.Event(ev)
		return
	}
	h.kept = append(h.kept, ev)
	h.out.Event(ev)
}

func (h *heldSink) Status(st dispatch.Status) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.next != nil {
		h.next.Status(st)
		return
	}
	h.kept = append(h.kept, st)
}

// Load gives the machine's load to the dashboard, once there is one.
func (h *heldSink) Load(r load.Reading) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.next != nil {
		h.next.Load(r)
	}
}

// handTo gives next what was kept, in order, and from then on everything.
func (h *heldSink) handTo(next dashboardSink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range h.kept {
		switch k := k.(type) {
		case dispatch.Event:
			next.Event(k)
		case dispatch.Status:
			next.Status(k)
		}
	}
	h.kept, h.next = nil, next
}

// runFiling runs the loop on a terminal while the spec's breakdown goes on (r.filing): its events
// printed above the breakdown's busy line (say) until the breakdown is over, then on the dashboard,
// given them, as runDashboard has it. A stop signal stops both; a breakdown that fails or is stopped
// stops the loop, leaving its workers, and the run ends with the breakdown's exit code.
func runFiling(ctx context.Context, r loopRun, say tui.Printer, stdin io.Reader, stdout *os.File,
	stderr io.Writer) int {
	f := r.filing
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	held := &heldSink{out: say}
	r.orch.SetSink(held)
	stopped := make(chan os.Signal, 1)
	r.stops.on(func(s os.Signal) {
		stopped <- s
		cancel(dispatch.InterruptedError(stoppedHow(s)))
		f.stopBreakdown()
	})
	stopDrain := watchDrain(func() { r.orch.Drain("by " + signalName(drainSignals[0])) })
	codes := make(chan int, 1)
	started := &startedLoop{codes: codes, held: held, began: time.Now()}
	go func() {
		defer func() {
			if v := recover(); v != nil {
				r.log.Line(time.Now(), fmt.Sprintf("PANIC: %v\n\n%s", v, debug.Stack()))
				if kill := started.onPanic.Load(); kill != nil {
					(*kill)()
				}
				panic(v)
			}
		}()
		codes <- r.orch.Run(ctx)
	}()
	var sig os.Signal
	select {
	case <-f.over:
	case sig = <-stopped:
		<-f.over
	}
	if sig == nil {
		select {
		case sig = <-stopped:
		default:
		}
	}
	stopDrain()
	if sig == nil && f.code == dispatch.ExitOK {
		return dashboard(ctx, func() { cancel(nil) }, r, started, stdin, stdout, stderr)
	}
	// Stopped, or the breakdown failed or was stopped: the loop stops, its workers left to the user.
	why := dispatch.InterruptedError("with Ctrl+C")
	if sig != nil {
		why = dispatch.InterruptedError(stoppedHow(sig))
	} else {
		errors.As(context.Cause(f.failed), &why)
		cancel(why)
	}
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: say, exit: os.Exit})
	msg := dispatch.InterruptLine(string(why), r.orch.Running())
	ev := dispatch.Event{Kind: dispatch.EvStop, Detail: dispatch.Interrupted, Text: msg, Time: time.Now()}
	r.log.Line(ev.Time, msg)
	r.log.Notify(dispatch.Notice(ev))
	r.log.Record(ev)
	say.Event(ev)
	r.stops.windDown()
	<-codes
	r.organs(dispatch.ExitInterrupted, msg, say)
	return f.result(dispatch.ExitInterrupted)
}
