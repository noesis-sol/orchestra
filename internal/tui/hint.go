package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// hintLine says which keys do what, each key brighter than what it does: the workers' numbers go to
// their tabs (not while the stop question is open), and s stops after the running tickets, or once
// asked goes on taking them; a run stopping for another reason has no s to offer. Ctrl+C needs no
// hint. A narrow pane gets a shorter form, and then one without the numbers; with no key to name,
// the line is empty.
func (m Dashboard) hintLine(w int) string {
	var keys []keyHint
	if n := min(len(m.active), numbered); n > 0 && !m.asking {
		k := keyHint{key: "1", does: "go to the worker's tab", short: "worker tab"}
		if n > 1 {
			k.key, k.does = fmt.Sprintf("1–%d", n), "go to a worker's tab"
		}
		keys = append(keys, k)
	}
	switch {
	case m.draining:
		keys = append(keys, keyHint{key: "s", does: "keep taking tickets", short: "keep going"})
	case !m.stopping:
		keys = append(keys, keyHint{key: "s", does: "stop after the current tickets", short: "stop after current"})
	}
	if len(keys) == 0 {
		return ""
	}
	forms := []string{hintForm(keys, false), hintForm(keys, true)}
	if len(keys) > 1 { // the numbers and s: then s alone
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

// keyHint is a key the dashboard answers to and what it does, in full and in short for a narrow pane.
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
		does := " to " + k.does
		if short {
			does = " " + k.short
		}
		parts[i] = keyStyle.Render(k.key) + dimStyle.Render(does)
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}
