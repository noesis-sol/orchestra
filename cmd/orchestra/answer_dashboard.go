package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// The dashboard opens the answer talk (answer_talk.go) in a pane split off orchestra's, as the
// refinement opens beside it, while the run goes on: by itself, without the keyboard focus, as soon as
// a question waits, or with "questions": "key" in settings.json, or overnight (talksByItself), on its a
// key, focused. While the talk is open, a goes to its pane, and a question that comes to wait is queued
// into it: the talk reads bd for those in the run's scope it doesn't ask yet and gives them to claude
// once its turn is over. The talk records each answer with bd, which closes the question, and the loop
// takes the ticket that waited on it back as it does whoever answered (ANSWERED). The dashboard holds
// orchestra's terminal, so the talk can't fall back on it: where no pane can be opened, or orchestra
// runs in none, the maintainer answers with orchestra answer in another terminal (answer_command.go).

// errAnswerNoPane is the answer talk's error under the dashboard when no pane could be opened beside
// orchestra's.
var errAnswerNoPane = errors.New("no pane could be opened beside orchestra's (the log says why): " +
	"answer in another terminal with orchestra answer")

// errNothingToAnswer is the answer talk's error under the dashboard when bd has none of the questions
// open any more, answered meanwhile some other way.
var errNothingToAnswer = errors.New("bd has no question open in the run's scope: " +
	"their tickets come back as the run next reads bd")

// dashboardAnswerTalk is the answer talk the dashboard opens for the run c, in a pane split off
// orchestra's: what the talk would say on orchestra's terminal, which the dashboard holds, goes to the
// log.
func dashboardAnswerTalk(c options, log *dispatch.Log) answerTalk {
	a := answerTalkIn(c, c.where.pane)
	a.out, a.err = logLines{log}, logLines{log}
	a.terminal = func(context.Context, string) error { return errAnswerNoPane }
	return a
}

// logLines is an io.Writer that writes each line written to it to the log.
type logLines struct{ log *dispatch.Log }

func (l logLines) Write(p []byte) (int, error) {
	for line := range strings.Lines(string(p)) {
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			l.log.Line(time.Now(), line)
		}
	}
	return len(p), nil
}

// answerTalks opens the answer talk for the dashboard, about the run's scope, one at a time: the
// dashboard opens no other while one is open, and queues the questions that come meanwhile into it.
// Each tells the dashboard once it is over (send).
type answerTalks struct {
	ctx    context.Context
	cancel func() // closes the talk open, if any
	talk   answerTalk
	scope  string
	log    *dispatch.Log
	send   func(tea.Msg) // to the dashboard's program
	wg     sync.WaitGroup
	open   atomic.Bool
	live   atomic.Pointer[answerLive] // the talk last opened, as the dashboard's callbacks reach it
	ended  atomic.Pointer[answerEnd]  // how the talk last over ended, for finish to say
	// deferred is what the loop, with nothing ready but questions, gave the next talk to say of the
	// deferred tickets (see ask)
	deferred atomic.Pointer[string]
	// put is the questions put to the maintainer in a talk now over, marked as each talk ends, whatever
	// the loop was doing then: a talk that opens by itself leaves them out, and the loop opens none on
	// them alone (runAnswers.Questions). The a key's talk asks them again.
	putMu sync.Mutex
	put   map[string]bool
}

// markPut records the questions ids as put to the maintainer in a talk now over.
func (a *answerTalks) markPut(ids []string) {
	a.putMu.Lock()
	defer a.putMu.Unlock()
	if a.put == nil {
		a.put = map[string]bool{}
	}
	for _, id := range ids {
		a.put[id] = true
	}
}

// wasPut reports whether the question id was put to the maintainer in a talk now over.
func (a *answerTalks) wasPut(id string) bool {
	a.putMu.Lock()
	defer a.putMu.Unlock()
	return a.put[id]
}

// answerEnd is how an answer talk ended: how it left its questions, and its error, if any.
type answerEnd struct {
	r   answerResult
	err error
}

// newAnswerTalks opens talk for the dashboard, under ctx, about the questions in scope ("" for every
// one), saying in log what each left open; send gives the dashboard its end.
func newAnswerTalks(ctx context.Context, talk answerTalk, scope string, log *dispatch.Log,
	send func(tea.Msg)) *answerTalks {
	ctx, cancel := context.WithCancel(ctx)
	return &answerTalks{ctx: ctx, cancel: cancel, talk: talk, scope: scope, log: log, send: send}
}

// callbacks are the talk as the dashboard reaches it.
func (a *answerTalks) callbacks() tui.AnswerTalk {
	return tui.AnswerTalk{Open: a.start, Queue: a.queue, Focus: a.focus}
}

// start opens the talk in the background, with the keyboard focus or without: the dashboard's
// callback, which mustn't block. Opened without, by itself, it leaves out the questions put to the
// maintainer in a talk now over; on the a key it asks every one open. Once it is over, the questions
// it asked count as put, or every one it read where it failed before asking any, so that a talk that
// can't open isn't opened again and again. The log says it opens, and how it ended, even where the
// dashboard has closed meanwhile, as at the run's end, and can't show why it failed.
func (a *answerTalks) start(focus bool) {
	how := "the answer talk opens on the a key"
	if !focus {
		how = "the answer talk opens by itself, unfocused"
	}
	a.log.Line(time.Now(), how)
	live := newAnswerLive()
	talk := a.talk
	talk.aside, talk.live = !focus, live
	if !focus {
		talk.skip = a.wasPut
	}
	if d := a.deferred.Swap(nil); d != nil {
		talk.deferred = *d
	}
	a.live.Store(live)
	a.open.Store(true)
	a.wg.Go(func() {
		defer a.open.Store(false)
		qs, err := talk.read(a.ctx, a.scope, "")
		var r answerResult
		if err == nil && len(qs) > 0 {
			r, err = talk.talkThrough(a.ctx, a.scope, qs)
		}
		put := slices.Concat(r.Answered, r.Open)
		switch {
		case err == nil && len(put) == 0:
			err = errNothingToAnswer
		case err != nil && len(put) == 0:
			for _, q := range qs {
				put = append(put, q.ID)
			}
		}
		a.markPut(put) // before the dashboard hears of the end, and tells the loop waiting on it
		a.ended.Store(&answerEnd{r: r, err: err})
		if a.ctx.Err() != nil { // the run stopped: the dashboard is closing
			a.log.Line(time.Now(), "the answer talk closed as the run stopped")
			return
		}
		if err != nil {
			a.log.Raw("", fmt.Errorf("the answer talk: %w", err))
		} else {
			a.log.Line(time.Now(), answeredLine(r))
		}
		a.send(tui.AnswerOver{Asked: slices.Concat(r.Answered, r.Open), Answered: r.Answered, Err: err})
	})
}

// queue tells the talk open that a question has come to wait: the dashboard's callback.
func (a *answerTalks) queue() {
	if l := a.live.Load(); l != nil {
		l.queue()
	}
}

// focus goes to the talk's pane, in the background, or once it has opened: the dashboard's callback for
// its a key while the talk is open. A failure goes to the log.
func (a *answerTalks) focus() {
	l := a.live.Load()
	if l == nil {
		return
	}
	pane := l.toFocus()
	if pane == "" { // the talk goes to it as it opens (answerSubject.opened)
		return
	}
	a.wg.Go(func() {
		if err := a.talk.panes.FocusPane(a.ctx, pane); err != nil && a.ctx.Err() == nil {
			a.log.Raw("", fmt.Errorf("cannot go to the answer talk's pane %s: %w", pane, err))
		}
	})
}

// answeredLine says how the answer talk left its questions.
func answeredLine(r answerResult) string {
	line := "the answer talk answered " + strings.Join(r.Answered, ", ")
	switch {
	case len(r.Answered) == 0:
		line = "the answer talk answered no question"
	case len(r.Open) == 0:
		return line
	}
	return line + "; still open: " + strings.Join(r.Open, ", ")
}

// finish waits for the talk still open, if any, once the run is over, having said so on out, and then
// says there how it ended: orchestra ends once it does. A stop signal meanwhile closes it. One the run's
// stop closed already is only waited for, as it closes its pane.
func (a *answerTalks) finish(stops *stopWatch, out io.Writer) {
	if a == nil {
		return
	}
	defer a.cancel()
	waits := a.open.Load() && a.ctx.Err() == nil
	if waits {
		fmt.Fprintln(out, "The answer talk is still open in the pane on the right: orchestra ends once it is over "+
			"(Ctrl+C closes it).")
		stops.on(func(os.Signal) { a.cancel() })
		defer stops.on(nil)
	}
	a.wg.Wait()
	if waits {
		a.sayEnded(out)
	}
}

// sayEnded says on out how the talk the run waited on ended, as orchestra answer does: closed by a stop
// signal or with its error, if so, then the questions it answered, whose tickets come back to the next
// run, and those still open. The summary and the report above still have their tickets waiting.
func (a *answerTalks) sayEnded(out io.Writer) {
	end := a.ended.Load()
	if end == nil {
		return
	}
	switch {
	case a.ctx.Err() != nil:
		fmt.Fprintln(out, "The answer talk was closed.")
	case end.err != nil:
		fmt.Fprintln(out, "The answer talk:", end.err)
	}
	sayAnswered(out, end.r, "the next run")
}
