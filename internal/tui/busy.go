package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/x/ansi"
)

// A busy line: a step the orchestrator works on outside the event stream, such as the run report,
// shown on a terminal with a spinner and the time it has taken until the step ends, then redrawn
// as its final line. It is drawn from a goroutine with carriage return and clear-line rather than
// by a Bubble Tea program, which would put the terminal in raw mode and take Ctrl+C as a key: the
// organ phase needs it to come as a signal.

// busySpinner is the dashboard's spinner.
var busySpinner = spinner.Dot

// Terminal returns a styled Printer for a terminal width columns wide whose busy lines (Busy) are
// animated. Its copies share the line: what any of them prints while one is drawn goes above it.
func Terminal(out io.Writer, width int) Printer {
	return Printer{Out: out, Styled: true, Width: width, live: &liveLine{}}
}

// liveLine is the busy line a Terminal printer's copies share, and the lock on their output.
type liveLine struct {
	mu   sync.Mutex
	line string // the busy line as drawn, the cursor at its end; "" while none is
}

// print writes s, whole lines, to the printer's output: above the busy line, if one is drawn.
func (p Printer) print(s string) {
	if p.live == nil {
		_, _ = io.WriteString(p.Out, s) // a terminal that can't be written to has no one to tell
		return
	}
	p.live.mu.Lock()
	defer p.live.mu.Unlock()
	if p.live.line != "" {
		s = "\r" + ansi.EraseEntireLine + s + p.live.line
	}
	_, _ = io.WriteString(p.Out, s)
}

// draw replaces the busy line with line, or clears it with "".
func (l *liveLine) draw(out io.Writer, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.line == "" && line == "" {
		return
	}
	_, _ = io.WriteString(out, "\r"+ansi.EraseEntireLine+line)
	l.line = line
}

// Busy is a step under way, shown by Printer.Busy until Done or Warn ends it.
type Busy struct {
	p     Printer
	began time.Time
	stop  chan struct{} // closed by the end of the step
	spun  chan struct{} // closed once the spinner's goroutine has returned; nil when none runs
}

// Busy shows a step under way until Done or Warn ends it, which the caller must call. On a
// Terminal printer it is a spinner, the styled text, the time taken so far and the hint, redrawn
// from a goroutine; a step that ends within the spinner's first frame shows only its final line.
// Any other styled printer prints the text and hint once, as Say does, and plain output the plain
// line, as the log words it.
func (p Printer) Busy(plain, styled, hint string) *Busy {
	b := &Busy{p: p, began: time.Now(), stop: make(chan struct{})}
	switch {
	case !p.Styled:
		p.sayPlain(plain)
	case p.live == nil:
		if hint != "" {
			styled += " " + hint
		}
		p.Say(plain, styled)
	default:
		b.spun = make(chan struct{})
		go b.spin(styled, hint)
	}
	return b
}

// spin draws the busy line at each of the spinner's frames until the step ends.
func (b *Busy) spin(text, hint string) {
	defer close(b.spun)
	tick := time.NewTicker(busySpinner.FPS)
	defer tick.Stop()
	for i := 0; ; i++ {
		select {
		case <-b.stop:
			return
		case <-tick.C:
		}
		frame := strings.TrimSpace(busySpinner.Frames[i%len(busySpinner.Frames)])
		line := pickedStyle.Render(frame) + " " + sayStyle.Render(text) +
			" " + dimStyle.Render(clock(time.Since(b.began)))
		if hint != "" {
			line += " " + sayStyle.Render(hint)
		}
		if b.p.Width > 1 {
			line = ansi.Truncate(line, b.p.Width-1, "") // a line that wrapped couldn't be redrawn
		}
		b.p.live.draw(b.p.Out, line)
	}
}

// end stops the spinner, if any, waits for its goroutine and clears its line.
func (b *Busy) end() {
	close(b.stop)
	if b.spun != nil {
		<-b.spun
		b.p.live.draw(b.p.Out, "")
	}
}

// Done ends the step with its final line on a terminal, the styled text after the organs' ◆ and
// the time it took, if a second or more; plain output, which said the step began, prints nothing.
func (b *Busy) Done(styled string) {
	took := time.Since(b.began)
	b.end()
	if !b.p.Styled {
		return
	}
	line := organStyle.Render("◆ ") + sayStyle.Render(styled)
	if took >= time.Second {
		line += " " + dimStyle.Render("("+duration(took)+")")
	}
	b.p.print(line + "\n")
}

// Warn ends the step with a warning in its place, printed as Printer.Warn prints it.
func (b *Busy) Warn(text string) {
	b.end()
	b.p.Warn(text)
}

// clock is d as a busy line shows it: minutes and seconds, "0:42", with hours once there are any.
func clock(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// duration is d as a busy line's final line says it: "12s", "1m04s", "1h02m03s".
func duration(d time.Duration) string {
	s := int(d / time.Second)
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh%02dm%02ds", s/3600, s/60%60, s%60)
	case s >= 60:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}
