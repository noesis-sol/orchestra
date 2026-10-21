// Package requires checks a project's requirements, settings.json's "requires": what its machine must
// have (see project.Requirement). It runs every check in the main checkout, side by side, each within
// its time limit; for a failed check with an install, it runs the install, one at a time (package
// managers lock their own state), its whole output kept in .orchestra/run/, and checks again. A run
// checks them before its first ticket and stops REQUIREMENTS_MISSING for what is still missing (see
// Report.MissingLine), orchestra init reports them, and 'orchestra requires' prints them.
package requires

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// DefaultCheckLimit is how long each check may run: it only looks, so it is short.
const DefaultCheckLimit = time.Minute

// atOnce is the most checks run side by side.
const atOnce = 8

// grace is how long a command stopped at its time limit has between SIGTERM and SIGKILL.
const grace = 5 * time.Second

// Options are how Check runs the requirements.
type Options struct {
	Dir          string        // the main checkout, where every command runs
	CheckLimit   time.Duration // each check's time limit; 0 for DefaultCheckLimit
	InstallLimit time.Duration // each install's time limit; 0 for project.DefaultCheckTimeout
	NoInstall    bool          // only check: a failed check is missing, its install not run
	GOOS         string        // the operating system the platforms are read for; "" for runtime.GOOS
	// Working is told what Check runs as it starts it ("checking 3 requirements", "installing jq: …"),
	// for a busy line; nil for nothing.
	Working func(string)
}

// State is how a requirement was found.
type State int

// The states a requirement is found in.
const (
	Met       State = iota // its check passed
	Installed              // its check failed, its install ran, and the check passes now
	Missing                // its check fails: nothing installs it, or its install didn't help
	OtherOS                // it doesn't apply on this operating system, and wasn't checked
	Unchecked              // the check was cut short, by a stop
)

// Result is how one requirement was found.
type Result struct {
	project.Requirement
	State State
	// CheckErr is why its last check failed; nil when it passed or didn't run.
	CheckErr error
	// InstallErr is why its install failed, nil when it worked or didn't run; Output is where its whole
	// output is, "" when it didn't run or its file couldn't be written (KeptErr says why).
	InstallErr error
	Output     string
	Ran        bool  // its install ran
	KeptErr    error // why its install's output couldn't be kept, if it couldn't
}

// Report is how each requirement was found, in the list's order.
type Report []Result

// Check checks every requirement in list as o says (see the package's comment), and returns how each
// was found. A ctx that ends cuts it short: what it didn't finish is Unchecked.
func Check(ctx context.Context, list []project.Requirement, o Options) Report {
	goos := o.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	report := make(Report, len(list))
	var due []int
	for i, r := range list {
		report[i] = Result{Requirement: r, State: Unchecked}
		if !r.AppliesOn(goos) {
			report[i].State = OtherOS
			continue
		}
		due = append(due, i)
	}
	if len(due) == 0 {
		return report
	}
	o.working(fmt.Sprintf("checking %s", countOf(len(due), "requirement")))
	var wg sync.WaitGroup
	slots := make(chan struct{}, atOnce)
	for _, i := range due {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			report[i].settle(ctx, o.check(ctx, report[i].Check))
		})
	}
	wg.Wait()
	for _, i := range due {
		r := &report[i]
		if r.State != Missing || r.Install == "" || o.NoInstall || ctx.Err() != nil {
			continue
		}
		o.working(fmt.Sprintf("installing %s: %s", r.Name, r.Install))
		r.Ran = true
		r.Output, r.KeptErr, r.InstallErr = o.install(ctx, r.Name, r.Install)
		if r.InstallErr != nil {
			if ctx.Err() != nil {
				r.State = Unchecked
			}
			continue
		}
		if r.settle(ctx, o.check(ctx, r.Check)); r.State == Met {
			r.State = Installed
		}
	}
	return report
}

// settle records the check's result, err: met, missing, or unchecked when ctx ended.
func (res *Result) settle(ctx context.Context, err error) {
	switch {
	case err == nil:
		res.State, res.CheckErr = Met, nil
	case ctx.Err() != nil:
		res.State = Unchecked
	default:
		res.State, res.CheckErr = Missing, err
	}
}

// working says what Check runs, if anyone listens.
func (o Options) working(what string) {
	if o.Working != nil {
		o.Working(what)
	}
}

// check runs the check cmd in o.Dir within its time limit, its output dropped: its exit status says.
func (o Options) check(ctx context.Context, cmd string) error {
	return run(ctx, orDefault(o.CheckLimit, DefaultCheckLimit), o.Dir, io.Discard, cmd)
}

// install runs the install cmd of the requirement name in o.Dir within its time limit, and keeps its
// whole output in o.Dir's .orchestra/run/ (project.RequireLogName). It returns where the output is, why
// it couldn't be kept there if it couldn't (output then ""), and why the install failed.
func (o Options) install(ctx context.Context, name, cmd string) (output string, keptErr, err error) {
	limit := orDefault(o.InstallLimit, project.DefaultCheckTimeout)
	rel := project.RunPath(project.RequireLogName(name))
	root, keptErr := project.OpenRun(o.Dir)
	var f *os.File
	if keptErr == nil {
		f, keptErr = project.CreateRun(root, o.Dir, rel, 0o644)
		_ = root.Close() // f stays open
	}
	if keptErr != nil {
		return "", keptErr, run(ctx, limit, o.Dir, io.Discard, cmd)
	}
	out := &dropOnFail{w: f}
	err = run(ctx, limit, o.Dir, out, cmd)
	if keptErr = errors.Join(out.err, f.Close()); keptErr != nil {
		return "", keptErr, err
	}
	return filepath.Join(o.Dir, rel), nil, err
}

// dropOnFail writes to w until a write fails, and drops the rest: a command's output is all read
// however its file fares.
type dropOnFail struct {
	w   io.Writer
	err error // the write that failed
}

// Write writes p to w, unless a write failed before.
func (d *dropOnFail) Write(p []byte) (int, error) {
	if d.err == nil {
		_, d.err = d.w.Write(p)
	}
	return len(p), nil
}

// errTimedOut is a command's error when its time limit stopped it.
var errTimedOut = errors.New("timed out")

// run runs the shell command line cmd in dir within limit, in its own process group with no input,
// its output written to out.
func run(ctx context.Context, limit time.Duration, dir string, out io.Writer, cmd string) error {
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	err := command.GroupRun(runCtx, grace, dir, out, "sh", "-c", cmd)
	if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: stopped after %s", errTimedOut, command.ShortDuration(limit))
	}
	return err
}

// orDefault is d, or def where d is 0.
func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// countOf is n things, as "1 requirement" or "3 requirements".
func countOf(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// Missing are the requirements still missing.
func (r Report) Missing() []Result {
	var missing []Result
	for _, res := range r {
		if res.State == Missing {
			missing = append(missing, res)
		}
	}
	return missing
}

// MissingKind is the first word of the line a run stops with for what is still missing.
const MissingKind = "REQUIREMENTS_MISSING"

// MissingLine is the line a run stops with for what is still missing: MissingKind, then
// MissingDetail; "" for none.
func (r Report) MissingLine() string {
	if d := r.MissingDetail(); d != "" {
		return MissingKind + d
	}
	return ""
}

// MissingDetail is what follows MissingKind on the line a run stops with for what is still missing:
// each name and what to do, its manual instruction or why its install failed and where its output
// is; "" for none.
func (r Report) MissingDetail() string {
	missing := r.Missing()
	if len(missing) == 0 {
		return ""
	}
	parts := make([]string, len(missing))
	for i, res := range missing {
		parts[i] = res.Name + " (" + res.Why() + ")"
	}
	return ": " + strings.Join(parts, "; ") + "; " + fixLine(missing)
}

// fixLine says what to do about missing, the requirements still missing.
func fixLine(missing []Result) string {
	return "meet " + map[bool]string{true: "it", false: "them"}[len(missing) == 1] +
		" (each entry of 'requires' in " + project.SettingsFile + " says how), then run again"
}

// Why says what to do about a requirement still missing: its manual instruction, or why its install
// didn't meet it and where its output is, or that nothing installs it.
func (res Result) Why() string {
	switch {
	case res.Manual != "":
		return res.Manual
	case !res.Ran && res.Install != "":
		return fmt.Sprintf("'%s' fails; install it with '%s'", res.Check, res.Install)
	case !res.Ran:
		return fmt.Sprintf("'%s' fails", res.Check)
	}
	where := "its output is in " + res.Output
	if res.Output == "" {
		where = fmt.Sprintf("its output could not be kept: %v", res.KeptErr)
	}
	if res.InstallErr != nil {
		return fmt.Sprintf("'%s' failed: %s; %s", res.Install, firstLine(res.InstallErr), where)
	}
	return fmt.Sprintf("'%s' ran, but '%s' still fails; %s", res.Install, res.Check, where)
}

// Says says how res was found, for 'orchestra requires' and init: met, installed, or missing and
// what to do.
func (res Result) Says() string {
	switch res.State {
	case Met:
		return "met"
	case Installed:
		return "installed with '" + res.Install + "'"
	case OtherOS:
		return "not checked: only on " + strings.Join(res.Platforms, ", ")
	case Unchecked:
		return "not checked: stopped"
	}
	return "missing: " + res.Why()
}

// Summary is a line saying how each requirement was found, the met ones and those installed first,
// for a run's log as it starts; "" for none checked.
func (r Report) Summary() string {
	var met, installed, other []string
	for _, res := range r {
		switch res.State {
		case Met:
			met = append(met, res.Name)
		case Installed:
			installed = append(installed, res.Name+" (installed with '"+res.Install+"')")
		case OtherOS:
			other = append(other, res.Name+" (only on "+strings.Join(res.Platforms, ", ")+")")
		}
	}
	var parts []string
	if len(met) > 0 {
		parts = append(parts, strings.Join(met, ", ")+" met")
	}
	if len(installed) > 0 {
		parts = append(parts, strings.Join(installed, ", ")+" installed")
	}
	if len(other) > 0 {
		parts = append(parts, strings.Join(other, ", ")+" not checked")
	}
	return strings.Join(parts, "; ")
}

// firstLine is err's first line.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}
