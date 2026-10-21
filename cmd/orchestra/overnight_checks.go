package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's checks that refuse to start the run: each finds what would stop the run,
// or every ticket's merge, in the night, and offers its fix where orchestra can make one. The merge
// check on Base runs last, as it takes longest.

// limitCheck fails a run with a ticket limit (--limit, LIMIT, DONE_SO_FAR), which would stop it early,
// and offers to run without one.
func limitCheck(_ context.Context, s *session) preflightResult {
	c := &s.cfg
	if c.Limit == dispatch.NoLimit {
		return preflightResult{verdict: tui.CheckPassed, text: "no ticket limit"}
	}
	given := fmt.Sprintf("--limit %d", c.Limit)
	if s.e.getenv("LIMIT") == strconv.Itoa(c.Limit) {
		given = fmt.Sprintf("LIMIT=%d", c.Limit)
	}
	left := max(c.Limit-c.DoneSoFar, 0)
	text := fmt.Sprintf("%s is set: the run would stop after %s", given, ticketCount(left))
	if c.DoneSoFar > 0 {
		text = fmt.Sprintf("%s is set, with DONE_SO_FAR=%d: the run would stop after %d more", given, c.DoneSoFar, left)
	}
	return preflightResult{verdict: tui.CheckFailed, text: text,
		fix: &preflightFix{question: "Run without a limit?", done: "the run has no ticket limit",
			apply: func(s *session) error {
				s.cfg.Limit = dispatch.NoLimit
				return nil
			}}}
}

// ticketCount is "1 ticket" or "n tickets".
func ticketCount(n int) string {
	if n == 1 {
		return "1 ticket"
	}
	return fmt.Sprintf("%d tickets", n)
}

// concurrencyCheck fails a run that works on one ticket at a time, where one slow ticket holds up the
// night, and offers to run as many at a time as the machine allows (offeredConcurrency).
func concurrencyCheck(_ context.Context, s *session) preflightResult {
	c := &s.cfg
	if c.Concurrency >= 2 {
		return preflightResult{verdict: tui.CheckPassed, text: ticketCount(c.Concurrency) + " at a time"}
	}
	n := offeredConcurrency(runtime.NumCPU(), c.MachineWorkers)
	return preflightResult{verdict: tui.CheckFailed,
		text: fmt.Sprintf("%s at a time (concurrent in settings.json, --concurrent): one slow ticket would hold "+
			"up the night", ticketCount(c.Concurrency)),
		fix: &preflightFix{question: fmt.Sprintf("Run %s at a time?", ticketCount(n)),
			done: fmt.Sprintf("the run works on %s at a time", ticketCount(n)),
			apply: func(s *session) error {
				s.cfg.Concurrency = n
				return nil
			}}}
}

// offeredConcurrency is how many tickets at a time the concurrency check offers on a machine of cores
// cores whose runs may have machineWorkers workers between them (0 for no cap): one for each four
// cores, from 2 to 4, as init's choices go, but no more than the cap allows, nor fewer than 2.
func offeredConcurrency(cores, machineWorkers int) int {
	n := min(max(cores/4, 2), 4)
	if machineWorkers > 0 {
		n = max(min(n, machineWorkers), 2)
	}
	return n
}

// neverAsks are the permission modes in which a worker doesn't stop to ask before a tool, so that no
// prompt waits for an answer in the night.
var neverAsks = []string{project.PermissionAuto, project.PermissionAcceptEdits}

// permissionCheck fails a run whose Claude workers start in a permission mode that asks before a tool
// (default, manual, or Claude Code's own default), where a worker would wait on a prompt and the run
// stop for it, and offers auto.
func permissionCheck(_ context.Context, s *session) preflightResult {
	c := &s.cfg
	if kind := c.Agent(); !kind.ClaudeArgs {
		return preflightResult{verdict: tui.CheckPassed,
			text: "workers are " + kind.Name + ", which has no Claude Code permission mode"}
	}
	mode := c.permissionModes.unplanned
	if slices.Contains(neverAsks, mode) {
		return preflightResult{verdict: tui.CheckPassed,
			text: "workers start in " + mode + " permission mode, which doesn't stop to ask"}
	}
	in := "Claude Code's default permission mode"
	if mode != "" {
		in = mode + " permission mode (worker_permission_mode, --worker-permission-mode)"
	}
	return preflightResult{verdict: tui.CheckFailed,
		text: "workers start in " + in + ", which asks before every tool the settings don't allow: " +
			"a worker would wait on a prompt, and the run stop for it",
		fix: &preflightFix{question: "Start workers in auto permission mode?",
			done: "workers start in auto permission mode",
			apply: func(s *session) error {
				// settleWork filled in the mode workers start in at configuration: settle it again.
				s.cfg.permissionModes.unplanned = project.PermissionAuto
				s.cfg.settleWork()
				return nil
			}}}
}

// claudeCheck is the check that the claude bin names starts and is logged in, from what 'claude auth
// status --json' says: workers and organs alike need it.
func claudeCheck(bin string) preflightCheck {
	return func(ctx context.Context, s *session) preflightResult {
		failed := func(text string) preflightResult { return preflightResult{verdict: tui.CheckFailed, text: text} }
		if _, err := command.LookPath(ctx, bin); err != nil {
			return failed("claude isn't on the PATH: no worker could start")
		}
		out, err := command.Output(ctx, command.ReadLimit, s.cfg.Repo, bin, "auth", "status", "--json")
		var status struct {
			LoggedIn   *bool  `json:"loggedIn"`
			AuthMethod string `json:"authMethod"`
		}
		switch {
		case json.Unmarshal([]byte(out), &status) != nil || status.LoggedIn == nil:
			if err == nil {
				err = errors.New("its status said nothing of a login")
			}
			return failed(fmt.Sprintf("claude couldn't say whether it is logged in ('claude auth status' %s): "+
				"no worker might start", oneLine(err.Error())))
		case !*status.LoggedIn:
			return failed("claude isn't logged in: no worker could start. Log in with 'claude auth login'")
		}
		text := "claude starts, logged in"
		if status.AuthMethod != "" {
			text += " (" + status.AuthMethod + ")"
		}
		return preflightResult{verdict: tui.CheckPassed, text: text}
	}
}

// oneLine is text on one line, its runs of white space each a space.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// gb is a GB, as the disk check counts them.
const gb = 1 << 30

// diskCheck is the check that the disk where the worktrees go, as free says how many bytes it has
// free, has the GB settings.json's overnight_free_disk_gb wants: each worktree holds its own
// dependencies, and a full disk stops everything, bd included.
func diskCheck(free func(path string) (uint64, error)) preflightCheck {
	return func(_ context.Context, s *session) preflightResult {
		want := s.cfg.overnightFreeDisk
		if want == 0 {
			return preflightResult{verdict: tui.CheckPassed, text: "free disk isn't checked (overnight_free_disk_gb is 0)"}
		}
		dir := existingDir(s.cfg.WTRoot)
		n, err := free(dir)
		if err != nil {
			return preflightResult{verdict: tui.CheckWarned,
				text: fmt.Sprintf("orchestra couldn't tell how much disk is free in %s: %v", dir, err)}
		}
		if n < uint64(want)*gb {
			return preflightResult{verdict: tui.CheckFailed, text: fmt.Sprintf(
				"%d GB free in %s, where the worktrees go, under the %d GB an overnight run wants "+
					"(overnight_free_disk_gb in settings.json): each worktree holds its own dependencies, "+
					"and a full disk would stop the run. Free some space", n/gb, dir, want)}
		}
		return preflightResult{verdict: tui.CheckPassed,
			text: fmt.Sprintf("%d GB free where the worktrees go, %d GB wanted", n/gb, want)}
	}
}

// existingDir is dir, or where it isn't there yet, the nearest folder above it that is.
func existingDir(dir string) string {
	for {
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return dir
		}
		dir = up
	}
}

// fetchCheck brings Base up to its upstream before the other checks read it, as the run would as it
// starts (dispatch.FetchBase), so that they check the Base the run builds on; the run then doesn't
// fetch again. It warns where Base stays as it is (the fetch failed, Base has diverged), the warning
// recorded in the event stream once it opens, as the run's start would, and fails where the main
// checkout would stop the run before its first ticket: a spec the run is to commit may be uncommitted.
// Nothing with --no-fetch. The run then takes the setup and the requirements the fast-forward brought
// in (takeFetchedSettings), as a run's start does, before the other checks read them.
func fetchCheck(ctx context.Context, s *session) preflightResult {
	return fetchCheckWith(ctx, s, git.Git{})
}

// fetchCheckWith is fetchCheck, asking g of git. A fast-forward done is said, whatever stopped the
// pre-flight as it went on: Ctrl+C doesn't cut it short, and the stop then says Base moved.
func fetchCheckWith(ctx context.Context, s *session, g dispatch.BaseFetchGit) preflightResult {
	c := s.cfg.Config
	if c.NoFetch {
		return preflightResult{verdict: tui.CheckPassed,
			text: c.Base + " isn't fetched from its upstream (--no-fetch): the run builds on it as it is"}
	}
	f, err := dispatch.FetchBase(ctx, c, g, specBesides(c)...)
	switch {
	case f.Behind > 0:
	case ctx.Err() != nil:
		return preflightResult{verdict: tui.CheckWarned, text: c.Base + " wasn't fetched: stopped"}
	case err != nil:
		return preflightResult{verdict: tui.CheckFailed, text: fmt.Sprintf("%s couldn't be brought up to its "+
			"upstream: %s, and the run would stop before its first ticket", c.Base, oneLine(err.Error()))}
	}
	s.cfg.BaseFetched = true
	s.log.Raw(f.Output, nil)
	switch {
	case f.Warning != "":
		ev := f.WarnEvent()
		ev.Time = time.Now()
		s.log.Record(ev) // written once the stream opens; the checklist gives the log its line
		return preflightResult{verdict: tui.CheckWarned, text: f.Text}
	case f.Behind > 0:
		s.fetched = f.Text
		if err := s.takeFetchedSettings(); err != nil {
			return preflightResult{verdict: tui.CheckWarned, text: fmt.Sprintf("%s, but %s could not be read "+
				"again: %s; the run keeps the setup and the requirements it read before", f.Text,
				project.SettingsFile, oneLine(err.Error()))}
		}
		return preflightResult{verdict: tui.CheckPassed, text: f.Text}
	case f.Upstream == "":
		return preflightResult{verdict: tui.CheckPassed, text: c.Base + " follows no upstream: nothing to fetch"}
	}
	return preflightResult{verdict: tui.CheckPassed, text: c.Base + " has every commit of " + f.Upstream}
}

// takeFetchedSettings has the run take setup, setup_files and requires from settings.json as the
// pre-flight's fast-forward left it, as a run's start takes those its own fast-forward brings in
// (dispatch's takeSettings): the run read them before. Settings it can't read or resolve leave them
// as they were, and the error says why.
func (s *session) takeFetchedSettings() error {
	settings, _, err := project.LoadSettings(s.cfg.Repo)
	if err != nil {
		return err
	}
	files, err := project.ResolveSetupFiles(settings)
	if err != nil {
		return err
	}
	list, err := project.ResolveRequires(settings)
	if err != nil {
		return err
	}
	s.cfg.Setup, s.cfg.SetupFiles, s.cfg.Requires = settings.Setup, files, list
	return nil
}

// baseCheck runs the project's merge check (check_fast) once on Base's head, as dispatch.CheckBase
// does, and fails where it fails there: every ticket's merge would fail too, its branch rebased onto
// Base.
func baseCheck(ctx context.Context, s *session) preflightResult {
	c := s.cfg.Config
	if c.Check == "" {
		return preflightResult{verdict: tui.CheckPassed, text: "no merge check (check_fast) to run on " + c.Base}
	}
	failed := func(text string) preflightResult { return preflightResult{verdict: tui.CheckFailed, text: text} }
	busy := s.printer().Busy("", "Running the merge check on "+c.Base+"…", "(Ctrl+C stops)")
	b, err := dispatch.CheckBase(ctx, c, git.Git{})
	busy.Done("Ran the merge check on " + c.Base)
	if b.Ran.Suite != "" {
		s.log.Check(b.Ran) // recorded once the run starts, after its start record
	}
	if b.Kept != "" {
		s.printer().Warn(fmt.Sprintf("overnight pre-flight: the merge check's worktree %s is left "+
			"(git worktree remove --force %s removes it)", b.Kept, command.ShellWord(b.Kept)))
	}
	at := b.At[:min(len(b.At), 7)]
	switch {
	case err != nil:
		return failed(fmt.Sprintf("the merge check '%s' couldn't run on %s: %v", c.Check, c.Base, err))
	case b.Failed == "":
		return preflightResult{verdict: tui.CheckPassed, text: fmt.Sprintf("the merge check '%s' passes on %s at %s (%s)",
			c.Check, c.Base, at, command.ShortDuration(b.Took))}
	}
	text := fmt.Sprintf("the merge check %s on %s at %s, so every ticket's merge would fail", b.Failed, c.Base, at)
	if said := b.Said[:min(len(b.Said), 3)]; len(said) > 0 {
		text += ": " + strings.Join(said, "; ")
	}
	if b.Output != "" {
		text += " (the whole output: " + b.Output + ")"
	}
	return failed(text + ". Fix it on " + c.Base + " first")
}
