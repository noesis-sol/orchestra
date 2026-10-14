package tmux

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// launch runs kind with args in the pane, in place of what ran there, and returns at once. The
// command runs with orchestra's PATH, as orchestra would run it, through no shell's line editor, so
// an argument may be of any length and span lines. The pane stays open when it exits, for its
// screen to be read. With a name, the agent is given it. The pane keeps the process ID of what it
// launched (pidOption), so that state knows the pane's process for its own, which ends dead, never at a
// shell's prompt.
func (t Terminal) launch(ctx context.Context, pane, kind, name string, args []string) error {
	if !isID(pane, '%') {
		return missing("respawn-pane", pane, "pane")
	}
	var script strings.Builder
	script.WriteString("exec " + command.ShellQuote(kind))
	for _, a := range args {
		script.WriteString(" " + command.ShellQuote(a))
	}
	opts := []string{"-p", "-t", pane, kindOption, kind, ";", "set-option", "-p", "-t", pane, "remain-on-exit", "on"}
	if records, ok := claude.RecordsOf(args); ok {
		opts = append(opts, ";", "set-option", "-p", "-t", pane, recordsOption, records)
	} else {
		opts = append(opts, ";", "set-option", "-p", "-u", "-t", pane, recordsOption)
	}
	if _, err := t.run(ctx, command.ReadLimit, "set-option", pane, opts...); err != nil {
		return err
	}
	if name != "" {
		if err := t.setName(ctx, pane, name); err != nil {
			return err
		}
	}
	_, err := t.run(ctx, command.ReadLimit, "respawn-pane", pane,
		"-k", "-t", pane, "-e", "PATH="+os.Getenv("PATH"), "/bin/sh", "-c", script.String(),
		";", "set-option", "-p", "-F", "-t", pane, pidOption, "#{pane_pid}")
	return err
}

// LaunchInPane runs '<kind> <args…>' in the pane and returns at once; AdoptAgent names it.
func (t Terminal) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	return t.launch(ctx, pane, kind, "", args)
}

// StartAgent runs kind with args in the pane, names the agent name and waits up to a minute for it to
// be ready for a prompt (idle). An error says it went away or never got ready.
func (t Terminal) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	if err := t.launch(ctx, pane, kind, name, args); err != nil {
		return err
	}
	return t.waitFor(ctx, "start", name, time.Minute, dispatch.StateIdle, dispatch.StateDone)
}

// IsArgumentRefused is always false: tmux passes any argument.
func (t Terminal) IsArgumentRefused(error) bool { return false }

// IsNameRefused is always false: an agent's name is a pane option, which takes any name.
func (t Terminal) IsNameRefused(error) bool { return false }

// AgentName returns ticket id's worker's agent name: the ID itself, as tmux has no rule for names.
func (t Terminal) AgentName(id string) string { return id }
