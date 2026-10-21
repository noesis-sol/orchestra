package main

import (
	"context"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// organs is what organPhase needs from the loop.
type organs interface {
	FullCheckDue(code int) bool
	FullCheck(ctx context.Context)
	FinishTriage(ctx context.Context)
	Review(ctx context.Context, code int, final string) (string, error)
	SaveReport(report string) (string, error)
}

// organPhase runs after the loop stops: the full check, when it is due (see dispatch.FullCheckDue),
// then it waits for pending triage and has the reviewer write the run report. Ctrl+C, or another of
// stopSignals, skips whatever is left, and one that came while the loop wound down skips it all; so
// does SIGTERM or SIGHUP at any time in the run. The one after the signal that skipped it ends
// orchestra (see stopWatch.further). Its commands run with the environment ctx gives them
// (command.WithEnv), and only the signals cancel it, not ctx. It reports whether a stop signal skipped
// some of it, which closes the answer talk still open rather than wait for it (answerTalks.finish).
func organPhase(ctx context.Context, orch organs, c options, stops *stopWatch, log *dispatch.Log, code int,
	final string, out tui.Printer, cancelOrgans func(),
) (stopped bool) {
	full := orch.FullCheckDue(code)
	if !full && !c.Triage && !c.Review || stops.leaving() {
		return false
	}
	ctx, stop := stops.context(context.WithoutCancel(ctx))
	if ctx.Err() == nil {
		go func() {
			<-ctx.Done()
			cancelOrgans()
		}()
		runOrgans(ctx, orch, c, log, code, final, out, full)
	}
	return stop()
}

// runOrgans runs the organ phase under ctx, which a stop signal cancels: the full check if full, then
// triage and the run report, as c has them.
func runOrgans(ctx context.Context, orch organs, c options, log *dispatch.Log, code int, final string,
	out tui.Printer, full bool,
) {
	if full {
		// The loop's own lines say what it runs and how it went; plain output has only those.
		busy := out.Busy("", "Running the full check…", "(Ctrl+C skips)")
		orch.FullCheck(ctx)
		if ctx.Err() != nil {
			busy.Done("Full check skipped")
			return
		}
		busy.Done("Full check finished")
	}
	if c.Triage {
		busy := out.Busy("finishing triage…", "Finishing triage…", "")
		orch.FinishTriage(ctx)
		if ctx.Err() != nil {
			busy.Done("Triage skipped")
		} else {
			busy.Done("Triage finished")
		}
	}
	if !c.Review || ctx.Err() != nil {
		return
	}
	busy := out.Busy("writing the run report with claude… (ctrl+c skips)", "Writing the run report with Claude…",
		"(Ctrl+C skips)")
	report, err := orch.Review(ctx, code, final)
	switch {
	case err != nil && ctx.Err() != nil:
		busy.Done("Run report skipped")
		return
	case dispatch.LimitRefusal(err) != "":
		msg := "REVIEW_SKIPPED: " + dispatch.LimitRefusal(err)
		log.Line(time.Now(), msg)
		busy.Warn(msg)
		return
	case err != nil:
		msg := "REVIEW_FAILED: " + dispatch.FirstLine(err.Error())
		log.Line(time.Now(), msg)
		busy.Warn(msg)
		return
	}
	busy.Done("Run report written")
	out.Report(report)
	path, err := orch.SaveReport(report)
	if err != nil {
		why := dispatch.FirstLine(err.Error())
		log.Line(time.Now(), "report not saved: "+why)
		out.Say("report not saved: "+why, "Report not saved: "+why)
		return
	}
	log.Line(time.Now(), "REPORT written to "+path)
	out.Say("report saved to "+tui.Tildify(path), "Report saved to "+tui.Tildify(path))
}
