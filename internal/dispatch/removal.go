package dispatch

import (
	"regexp"
	"slices"
	"strings"
)

// Claude Code holds some removals for a person whatever the settings allow: its built-in safety check
// stops an rm whose target it can't resolve (a shell variable, which unset or empty could make it
// `rm -rf /`, or a glob) or that names a system, home or workspace directory. Only a person can
// approve one, and a prompt left unanswered denies it, so an allow rule would not have spared the
// wait: the worker should not have written the removal (the worker prompt says so).

// rmCall matches rm where a command starts: at the start, after a separator, an opening bracket or
// quote (a `bash -c '…'` script), or a word that runs the command after it.
var rmCall = regexp.MustCompile("(?:^|[;&|({\\n'\"`]|\\b(?:do|then|else|xargs|sudo|exec|command|nice|time))" +
	`\s*\brm\s`)

// rmEnd matches the end of an rm's arguments: a separator or a closing bracket.
var rmEnd = regexp.MustCompile(`[;&|)}\n]`)

// GuardedRemoval returns the first rm in cmd, a Bash command, that Claude Code's safety check holds
// for a person: one with a target it can't resolve (a variable, a command's output, a glob) or a
// home, root or working directory. It returns the rm and its arguments, at most 80 characters, ""
// when there is none.
func GuardedRemoval(cmd string) string {
	for _, m := range rmCall.FindAllStringIndex(cmd, -1) {
		rest := cmd[m[1]:]
		if e := rmEnd.FindStringIndex(rest); e != nil {
			rest = rest[:e[0]]
		}
		if args := strings.Fields(rest); slices.ContainsFunc(args, unresolved) {
			return Clip("rm "+strings.Join(args, " "), 80)
		}
	}
	return ""
}

// unresolved reports whether a, an rm's argument, is a target the safety check can't resolve or a
// directory it guards; an option is neither.
func unresolved(a string) bool {
	a = strings.Trim(a, `'"`) // the quotes of a `bash -c '…'` script, or of the argument
	if a == "" || strings.HasPrefix(a, "-") {
		return false
	}
	switch strings.TrimRight(a, "/") {
	case "", "~", ".", "..":
		return true
	}
	return strings.ContainsAny(a, "$`*?[")
}

// Clip shortens s, its spaces collapsed, to at most n characters, marking a cut with "…".
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
