package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// The dashboard's a key, offered while questions wait, opens the answer talk (answer_talk.go) in a pane
// split off orchestra's, as the refinement opens beside it, while the run goes on: the talk records each
// answer with bd, which closes the question, and the loop takes the ticket that waited on it back as
// it does whoever answered (ANSWERED). The dashboard holds orchestra's terminal, so the talk can't fall
// back on it: where no pane can be opened, or orchestra runs in none, the maintainer answers with
// orchestra answer in another terminal (answer_command.go). answer_on_ask in settings.json has the
// dashboard open the talk by itself as a ticket comes to wait on a question.

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
// dashboard opens no other while one is open. Each tells the dashboard once it is over (send).
type answerTalks struct {
	ctx    context.Context
	cancel func() // closes the talk open, if any
	talk   answerTalk
	scope  string
	log    *dispatch.Log
	send   func(tea.Msg) // to the dashboard's program
	wg     sync.WaitGroup
	open   atomic.Bool
}

// newAnswerTalks opens talk for the dashboard, under ctx, about the questions in scope ("" for every
// one), saying in log what each left open; send gives the dashboard its end.
func newAnswerTalks(ctx context.Context, talk answerTalk, scope string, log *dispatch.Log,
	send func(tea.Msg)) *answerTalks {
	ctx, cancel := context.WithCancel(ctx)
	return &answerTalks{ctx: ctx, cancel: cancel, talk: talk, scope: scope, log: log, send: send}
}

// start opens the talk in the background: the dashboard's callback, which mustn't block.
func (a *answerTalks) start() {
	a.open.Store(true)
	a.wg.Go(func() {
		defer a.open.Store(false)
		r, err := a.talk.answer(a.ctx, a.scope, "")
		switch {
		case a.ctx.Err() != nil: // the run stopped: the dashboard is closing
			return
		case err != nil:
			a.log.Raw("", fmt.Errorf("the answer talk: %w", err))
		case len(r.Answered)+len(r.Open) == 0:
			err = errNothingToAnswer
		default:
			a.log.Line(time.Now(), answeredLine(r))
		}
		a.send(tui.AnswerOver{Answered: r.Answered, Err: err})
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

// finish waits for the talk still open, if any, once the run is over, having said so on out: orchestra
// ends once it does. A stop signal meanwhile closes it. One the run's stop closed already is only
// waited for, as it closes its pane.
func (a *answerTalks) finish(stops *stopWatch, out io.Writer) {
	if a == nil {
		return
	}
	defer a.cancel()
	if a.open.Load() && a.ctx.Err() == nil {
		fmt.Fprintln(out, "The answer talk is still open in the pane on the right: orchestra ends once it is over "+
			"(Ctrl+C closes it).")
		stops.on(func(os.Signal) { a.cancel() })
		defer stops.on(nil)
	}
	a.wg.Wait()
}
