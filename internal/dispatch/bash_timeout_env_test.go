package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker's settings have Bash wait on a command as long as the merge check may take when given
// no timeout, its default, and allow no less than Claude Code's own 10 minutes, so a check stays in the
// foreground without the worker, or its verifier, passing one; workers of other kinds get neither.
func TestWorkerEnvSetsBashTimeoutsToTheMergeChecksLimit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		kind               string
		limit              time.Duration
		defaultMs, maximum string
	}{
		{limit: 0, defaultMs: "1800000", maximum: "1800000"},
		{limit: 45 * time.Minute, defaultMs: "2700000", maximum: "2700000"},
		{limit: 5 * time.Minute, defaultMs: "300000", maximum: "600000"},
		{kind: "codex", limit: 45 * time.Minute},
	} {
		o := New(Config{AgentKind: c.kind, CheckTimeout: c.limit, Limit: NoLimit}, &Log{}, "", Deps{})
		env := o.workerEnv(context.Background())
		if c.defaultMs == "" {
			if env != nil {
				t.Errorf("%s worker: env %v, want none", c.kind, env)
			}
			continue
		}
		if got := env["BASH_DEFAULT_TIMEOUT_MS"]; got != c.defaultMs {
			t.Errorf("check limit %s: BASH_DEFAULT_TIMEOUT_MS %q, want %s", c.limit, got, c.defaultMs)
		}
		if got := env["BASH_MAX_TIMEOUT_MS"]; got != c.maximum {
			t.Errorf("check limit %s: BASH_MAX_TIMEOUT_MS %q, want %s", c.limit, got, c.maximum)
		}
	}
}

// A worker whose settings give it Bash's timeout is started with it, and its rules tell it to run its
// checks in the foreground with no timeout of its own, naming the limit, rather than at Bash's
// 10 minutes with the longer ones in the background.
func TestWorkerGivenBashsTimeoutIsToldToPassNone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		rec := &envRecorder{}
		h.reporter = rec
		h.cfg.Check, h.cfg.CheckTimeout = "true", 45*time.Minute
		h.beads.add("A", "first", 1)
		var rules string
		h.worker("A", func(w *fakeWorker) AgentState {
			rules = readRun(t, w.wt, project.RulesName)
			return finishes("a.txt")(w)
		})
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		rec.mu.Lock()
		envs := rec.envs
		rec.mu.Unlock()
		if len(envs) != 1 || envs[0]["BASH_DEFAULT_TIMEOUT_MS"] != "2700000" {
			t.Errorf("the worker started with %v, want BASH_DEFAULT_TIMEOUT_MS 2700000", envs)
		}
		want := "\n\nGive a check its time: orchestra has set Bash's default timeout to 45m, as long as its merge " +
			"check may take, so run every check in the foreground and pass Bash no timeout of your own: a shorter " +
			"one moves the check to the background when it runs out. Never wait on a check in the background, " +
			"nor with a long sleep, which Bash refuses.\n"
		if !strings.Contains(rules, want) {
			t.Errorf("the rules lack %q:\n%s", want, rules)
		}
		if strings.Contains(rules, "600000") || strings.Contains(rules, "Monitor") {
			t.Errorf("the rules still give Bash's maximum or Monitor:\n%s", rules)
		}
	})
}
