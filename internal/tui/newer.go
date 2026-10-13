package tui

import (
	"strings"

	"github.com/noesis-sol/orchestra/internal/update"
)

// Newer prints the line a run ends with when release newer is out and current runs (update.Notice):
// on a terminal in the warning colour, with the command that installs it picked out.
func (p Printer) Newer(newer, current string) {
	line := Printable(update.Notice(newer, current)) // the tag is GitHub's
	if p.Styled {
		if head, ok := strings.CutSuffix(line, "orchestra update"); ok {
			line = deferredStyle.Render(head) + keyStyle.Render("orchestra update")
		} else {
			line = deferredStyle.Render(line)
		}
	}
	p.print(line + "\n")
}
