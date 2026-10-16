package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/project"
)

// permissionLabel is what the START line says of the permission mode workers start in, after the
// agent: "" when none is set, as workers then start in Claude Code's own default.
func (c Config) permissionLabel() string {
	switch {
	case c.WorkerPermissionMode == "":
		return ""
	case !c.ClaudeWorkers():
		return ", permission mode: not passed to " + c.AgentKind + " workers"
	}
	return ", permission mode: " + c.WorkerPermissionMode
}

// permissionArgs are the arguments that start Claude workers in Config.WorkerPermissionMode, which
// begin asks the workers' claude about once, as the run starts: --permission-mode and the mode
// where its --help lists the mode, by its name or the one Claude Code renamed it from or to
// (default and manual), which is then the name passed. Where it doesn't, or its help can't be
// read, the run says so and workers start without them, in the mode Claude Code's settings give:
// a claude that doesn't know the mode refuses it, and no worker would start.
func (o *Loop) permissionArgs(ctx context.Context) []string {
	c := o.cfg
	mode := c.WorkerPermissionMode
	if mode == "" || !c.ClaudeWorkers() {
		return nil
	}
	help, err := "", errors.New("nothing to ask it with")
	if o.help != nil {
		help, err = o.help.Help(ctx)
	}
	listed := listedPermissionMode(help, mode)
	switch {
	case ctx.Err() != nil:
	case err != nil:
		o.log.Raw("", err)
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  PERMISSION_MODE: cannot read claude --help to tell whether "+
			"it takes the permission mode %s (%s); workers start without it, in the mode Claude Code's settings give",
			mode, strings.Join(strings.Fields(err.Error()), " "))})
	case listed == "":
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  PERMISSION_MODE: claude --help doesn't list the permission "+
			"mode %s; workers start without it, in the mode Claude Code's settings give (claude update may bring it)",
			mode)})
	default:
		return []string{"--permission-mode", listed}
	}
	return nil
}

// listedPermissionMode is the name help, claude's --help, lists the permission mode mode by: mode
// itself, else the name Claude Code renamed it from or to (project.RenamedPermissionModes), else
// "" when it lists neither.
func listedPermissionMode(help, mode string) string {
	for _, m := range []string{mode, project.RenamedPermissionModes[mode]} {
		if m != "" && listsPermissionMode(help, m) {
			return m
		}
	}
	return ""
}

// listsPermissionMode tells whether help, claude's --help, lists mode among the choices of its
// --permission-mode: "(choices: "acceptEdits", "auto", …)", on the option's line or the lines its
// description wraps onto, which are indented further.
func listsPermissionMode(help, mode string) bool {
	var option strings.Builder
	listed := func() bool { return strings.Contains(option.String(), strconv.Quote(mode)) }
	indent := -1 // the option's line's, once found
	for line := range strings.Lines(help) {
		text := strings.TrimLeft(line, " \t")
		f := strings.Fields(text)
		switch {
		case indent < 0 && len(f) > 0 && f[0] == "--permission-mode":
			indent = len(line) - len(text)
		case indent < 0:
			continue
		case len(f) == 0 || len(line)-len(text) <= indent: // the next option, or the end of the options
			return listed()
		}
		option.WriteString(line)
	}
	return listed()
}
