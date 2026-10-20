package project

import (
	"fmt"
	"slices"
	"strings"
)

// diffContext is how many unchanged lines a hunk of TemplateUpdate.Diff shows around a change.
const diffContext = 3

// Diff is the update as a unified diff, as git diff prints it, for the user to read before committing
// it: "" where the text is as it was.
func (u TemplateUpdate) Diff() string {
	a, b := diffLines(u.Before), diffLines(u.After)
	ops := lineDiff(a, b)
	var out strings.Builder
	for start := 0; start < len(ops); {
		for start < len(ops) && ops[start].op == ' ' {
			start++
		}
		if start == len(ops) {
			break
		}
		// A hunk: from diffContext lines before the change to diffContext after the last change that
		// lies within 2*diffContext unchanged lines of the one before it.
		from, end := max(start-diffContext, 0), start
		for i, same := start, 0; i < len(ops) && same <= 2*diffContext; i++ {
			if ops[i].op == ' ' {
				same++
			} else {
				end, same = i, 0
			}
		}
		to := min(end+1+diffContext, len(ops))
		if out.Len() == 0 {
			fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", u.Path, u.Path)
		}
		oldAt, newAt, oldN, newN := ops[from].old+1, ops[from].new+1, 0, 0
		var body strings.Builder
		for _, o := range ops[from:to] {
			switch o.op {
			case ' ':
				oldN, newN = oldN+1, newN+1
				body.WriteString(" " + a[o.old] + "\n")
			case '-':
				oldN++
				body.WriteString("-" + a[o.old] + "\n")
			default:
				newN++
				body.WriteString("+" + b[o.new] + "\n")
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n%s", oldAt, oldN, newAt, newN, body.String())
		start = to
	}
	return out.String()
}

// diffLines are text's lines, without their newlines.
func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// diffOp is a line of a diff: ' ' kept, '-' removed, '+' added, with where it is in the old and the new
// text (the next line's place for the text it isn't in).
type diffOp struct {
	op       byte
	old, new int
}

// lineDiff is the shortest edit from a to b, line by line, by their longest common subsequence: the
// files it compares are a template's size, a few hundred lines at most.
func lineDiff(a, b []string) []diffOp {
	// common[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	common := make([][]int, len(a)+1)
	for i := range common {
		common[i] = make([]int, len(b)+1)
	}
	for i, x := range slices.Backward(a) {
		for j, y := range slices.Backward(b) {
			if x == y {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, diffOp{' ', i, j})
			i, j = i+1, j+1
		case j == len(b) || i < len(a) && common[i+1][j] >= common[i][j+1]:
			ops = append(ops, diffOp{'-', i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', i, j})
			j++
		}
	}
	return ops
}
