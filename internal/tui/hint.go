package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// hintLine says which keys do what, each key brighter than what it does: the workers' numbers go to
// their tabs (not while the stop question is open, nor when the tabs are out of sight), s stops
// after the running tickets, or once asked goes on taking them (a run stopping for another reason
// has no s to offer), and a opens the answer talk while questions wait, the hint saying in its place
// while the talk is open (answerKey). Ctrl+C needs no hint. A narrow pane gets a shorter form, and
// then one without the numbers; with no key to name, the line is empty.
func (m Dashboard) hintLine(w int) string {
	var keys []keyHint
	numbers := false
	if n := min(len(m.Running()), numbered); n > 0 && !m.asking && m.focus != nil {
		k := keyHint{key: "1", does: "go to the worker's tab", short: "worker tab"}
		if n > 1 {
			k.key, k.does = fmt.Sprintf("1–%d", n), "go to a worker's tab"
		}
		keys, numbers = append(keys, k), true
	}
	switch {
	case m.draining:
		keys = append(keys, keyHint{key: "s", does: "keep taking tickets", short: "keep going"})
	case !m.stopping:
		keys = append(keys, keyHint{key: "s", does: "stop after the current tickets", short: "stop after current"})
	}
	if k, ok := m.answerKey(); ok {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	forms := []string{hintForm(keys, false), hintForm(keys, true)}
	if numbers && len(keys) > 1 { // the numbers and the rest: then the rest alone
		forms = append(forms, hintForm(keys[1:], false), hintForm(keys[1:], true))
	}
	hint := forms[len(forms)-1]
	for _, f := range forms {
		if ansi.StringWidth(f) <= w {
			hint = f
			break
		}
	}
	return ansi.Truncate(hint, w, "…")
}

// keyHint is a key the dashboard answers to and what it does, in full and in short for a narrow pane;
// with no key, what it says in full and in short, as of the answer talk while it is open.
type keyHint struct{ key, does, short string }

// hintForm is the hint line naming keys, in full ("  s to stop after the current tickets") or short
// (" s stop after current").
func hintForm(keys []keyHint, short bool) string {
	lead := "  "
	if short {
		lead = " "
	}
	return lead + keyWords(keys, short)
}

// keyWords names keys as the dashboard does wherever it names them, in the hint and the stop
// question alike: the keys in keyStyle, the rest faint; in full ("s to stop after the current
// tickets") or short ("s stop after current").
func keyWords(keys []keyHint, short bool) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		switch {
		case k.key == "" && short:
			parts[i] = dimStyle.Render(k.short)
		case k.key == "":
			parts[i] = dimStyle.Render(k.does)
		case short:
			parts[i] = keyStyle.Render(k.key) + dimStyle.Render(" "+k.short)
		default:
			parts[i] = keyStyle.Render(k.key) + dimStyle.Render(" to "+k.does)
		}
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}
