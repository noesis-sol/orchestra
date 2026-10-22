package main

import (
	"context"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// takeFetched has the run s take what f, the fast-forward of Base before its loop starts (the overnight
// pre-flight's, a spec run's), brought in, as the loop's own fetch would (dispatch.TakeFastForward):
// the settings and the worker prompt, asking g what it changed. It logs the lines that say so, as the
// loop does, records them in the event stream once it opens, and returns them.
func (s *session) takeFetched(ctx context.Context, g dispatch.FastForwardGit, f dispatch.BaseFetched) []dispatch.Event {
	c := &s.cfg.Config
	c.PromptFile = s.cfg.WorkerPrompt
	lines := dispatch.TakeFastForward(context.WithoutCancel(ctx), g, c, &s.prompt, f.TakeBy(), f.From, f.To)
	for i := range lines {
		lines[i].Time = time.Now()
		s.log.Line(lines[i].Time, lines[i].Text)
		s.log.Record(lines[i]) // written once the stream opens, after the run's start record
	}
	return lines
}
