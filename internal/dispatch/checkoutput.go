package dispatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// What orchestra keeps of a check's output (the check's, the setup's, the full check's) as the
// command prints it: the whole of it in a temporary file, which a failure keeps in the worktree's
// .orchestra/run/, and in memory only what is said of it, each part bounded. A check that loops
// printing, until its time limit, holds no more of orchestra's memory than one that prints a page.

// How much of a check's output is held in memory.
const (
	// keptLines is how many of its last lines are kept: the most anything says of its end (the
	// log's 40 lines, fullCheckEnd, maxSaid).
	keptLines = 40
	// maxLineBytes is how much of one line is kept; the rest of a longer one is in the file alone.
	maxLineBytes = 16 << 10
	// maxFlaky is how many FLAKY: lines are kept; the rest are counted.
	maxFlaky = 50
	// maxSeen is how many of the lines that say what failed are told apart, to count each once;
	// those after it are each counted.
	maxSeen = 1 << 16
)

// checkOutput takes a check's output as it is printed (it is the command's io.Writer) and keeps the
// whole of it in a temporary file and in memory its last lines, the lines that say what failed (see
// failureLines), its FLAKY: lines (see reportFlaky) and the suite it was in (see failingSuite). The
// zero value keeps no file. Call end once the command has exited, and close when done with it.
type checkOutput struct {
	file    *os.File // the whole output, removed from its folder already; nil without one
	fileErr error    // why the whole output isn't in file, if it isn't

	line []byte // the line being printed, at most maxLineBytes of it

	tail   [keptLines][]byte // the last lines, a ring: next is the oldest once it is full
	next   int
	n      int // how many lines tail holds
	blanks int // the blank lines printed since the last line in tail, kept once another line follows

	said    []string            // the first maxSaid lines that say what failed, each once
	saidN   int                 // how many lines said what failed, each once
	seen    map[uint64]struct{} // those lines, hashed
	seed    maphash.Seed
	running bool // in the tests named after "running tests:", until a blank line

	flaky  []string // the FLAKY: lines' tests, the first maxFlaky
	flakyN int      // how many FLAKY: lines there were

	suite string // the suite named last (see failingSuite)
}

// newCheckOutput is a checkOutput with a temporary file for the whole output. A file that can't be
// made is said when the output is saved.
func newCheckOutput() *checkOutput {
	c := &checkOutput{}
	f, err := os.CreateTemp("", "orchestra-check-*.log")
	if err != nil {
		c.fileErr = err
		return c
	}
	// Removed now, it holds no disk space once closed, whatever becomes of orchestra; and the check
	// can't touch it, though it may empty its worktree (git clean -fdx).
	if err := os.Remove(f.Name()); err != nil {
		c.fileErr = errors.Join(err, f.Close())
		return c
	}
	c.file = f
	return c
}

// runOutput runs the command line cmd in dir as the check, its setup or the full check runs (see
// command.GroupRun), stopping everything it started when ctx is done, and returns what is kept of
// its output, for the caller to close.
func runOutput(ctx context.Context, dir, cmd string) (*checkOutput, error) {
	out := newCheckOutput()
	err := command.GroupRun(ctx, 5*time.Second, dir, out, "sh", "-c", cmd)
	out.end()
	return out, err
}

// Write takes the next of the output: it never fails, so the command's output is all read.
func (c *checkOutput) Write(p []byte) (int, error) {
	if c.file != nil && c.fileErr == nil {
		if _, err := c.file.Write(p); err != nil {
			c.fileErr = err
		}
	}
	for rest := p; len(rest) > 0; {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			c.take(rest)
			break
		}
		c.take(rest[:i])
		c.endLine()
		rest = rest[i+1:]
	}
	return len(p), nil
}

// take adds b to the line being printed, as far as maxLineBytes.
func (c *checkOutput) take(b []byte) {
	if room := maxLineBytes - len(c.line); room > 0 {
		c.line = append(c.line, b[:min(room, len(b))]...)
	}
}

// end takes a last line printed without a newline.
func (c *checkOutput) end() {
	if len(c.line) > 0 {
		c.endLine()
	}
}

// endLine takes the line printed, and starts the next.
func (c *checkOutput) endLine() {
	line := c.line
	c.keepTail(line)
	line = bytes.TrimRight(line, "\r")
	c.scanFailure(line)
	if test, ok := bytes.CutPrefix(line, []byte(flakyPrefix)); ok {
		if c.flakyN++; len(c.flaky) < maxFlaky {
			c.flaky = append(c.flaky, string(bytes.TrimSpace(test)))
		}
	}
	if name, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte(project.SuiteMarker)); ok {
		if name := strings.Join(strings.Fields(string(name)), " "); name != "" {
			c.suite = name
		}
	}
	c.line = c.line[:0]
}

// keepTail keeps line among the last lines, as lastLines would: blank lines at the end are left out.
func (c *checkOutput) keepTail(line []byte) {
	if len(line) == 0 {
		c.blanks++
		return
	}
	for range min(c.blanks, keptLines) {
		c.push(nil)
	}
	c.blanks = 0
	c.push(line)
}

// push puts line in the ring of the last lines, over the oldest when it is full.
func (c *checkOutput) push(line []byte) {
	c.tail[c.next] = append(c.tail[c.next][:0], line...)
	c.next = (c.next + 1) % keptLines
	c.n = min(c.n+1, keptLines)
}

// scanFailure keeps line, trimmed, if it says what failed, as failureLines picks them.
func (c *checkOutput) scanFailure(line []byte) {
	t := bytes.TrimSpace(line)
	switch {
	case len(t) == 0:
		c.running = false
		return
	case string(t) == "running tests:":
		c.running = true
	case !c.running && !saysFailed(t, line):
		return
	}
	if c.seen == nil {
		c.seen, c.seed = map[uint64]struct{}{}, maphash.MakeSeed()
	}
	h := maphash.Bytes(c.seed, t)
	if _, ok := c.seen[h]; ok {
		return
	}
	if len(c.seen) < maxSeen {
		c.seen[h] = struct{}{}
	}
	if c.saidN++; len(c.said) < maxSaid {
		c.said = append(c.said, string(t))
	}
}

// saysFailed reports whether line, t trimmed, says what failed, as failedAnywhere or failedAtStart
// matches it. Go's regexp reads a long line at a few MB a second, slower than a check that loops can
// print: each is tried only on a line that holds text every line it matches holds, which a plain
// search finds much faster.
func saysFailed(t, line []byte) bool {
	if (bytes.Contains(t, []byte("FAIL")) || bytes.Contains(t, []byte("DATA RACE")) ||
		bytes.Contains(t, []byte("race detected"))) && failedAnywhere.Match(t) {
		return true
	}
	// A colon, but for a tool's own error and gotestsum's DONE line.
	may := bytes.IndexByte(line, ':') >= 0 || bytes.HasPrefix(line, []byte("DONE ")) ||
		bytes.HasPrefix(line, []byte("level=error")) || len(line) >= 5 && bytes.EqualFold(line[:5], []byte("error"))
	return may && failedAtStart.Match(line)
}

// failures is the lines that say what failed, each once, at most maxSaid of them, each cut to
// saidWidth runes: the last kept then says how many more there were. nil when none did.
func (c *checkOutput) failures() []string {
	said := slices.Clone(c.said)
	if c.saidN > len(said) {
		said = append(said[:maxSaid-1], fmt.Sprintf("(… and %d more lines like these)", c.saidN-maxSaid+1))
	}
	return boundLines(said, maxSaid)
}

// lastLines is the output's last n lines, at most keptLines, as lastLines(output, n) gives them,
// but with each cut to maxLineBytes.
func (c *checkOutput) lastLines(n int) string {
	n = min(n, c.n)
	lines := make([]string, 0, n)
	for i := range n {
		lines = append(lines, string(c.tail[(c.next-n+i+keptLines)%keptLines]))
	}
	return strings.Join(lines, "\n")
}

// save writes the whole output to the file rel in root, an os.Root at the checkout dir (see
// project.WriteRunFrom).
func (c *checkOutput) save(root *os.Root, dir, rel string) error {
	if c.fileErr != nil {
		return c.fileErr
	}
	if c.file == nil {
		return errors.New("the output was not kept")
	}
	return project.WriteRunFrom(root, dir, rel, io.NewSectionReader(c.file, 0, 1<<62), 0o644)
}

// close gives up the temporary file; c is nil-safe.
func (c *checkOutput) close() {
	if c != nil && c.file != nil {
		_ = c.file.Close() // read-only from now on, and removed already
		c.file = nil
	}
}

var (
	// failedAnywhere matches a line of a check's output, trimmed, that says a package or a test failed
	// (go test's FAIL and --- FAIL:, gotestsum's === FAIL:, a bare FAIL saying nothing more aside), or
	// that the race detector found a data race, in the test --- FAIL: then names.
	failedAnywhere = regexp.MustCompile(`^(FAIL\W+\S|--- FAIL|=== FAIL)|DATA RACE|race detected`)
	// failedAtStart matches a line that, unindented, says what failed: a panic (a test that timed out),
	// a tool's own error, gotestsum's count of failures, golangci-lint's of issues, and a lint, vet or
	// build error, which starts with its file's position. A failing test's own messages are indented.
	failedAtStart = regexp.MustCompile(`^(panic:|(?i:error)\b|fatal:|level=error|DONE \d.*fail|\d+ issues?:|` +
		`[^\s:]+\.\w+:\d+(:\d+)?: )`)
)

// flakyPrefix starts a line of a check's output that says a test failed and then passed on a rerun:
// a convention for any project's check, which docs/setup.md's The checks documents.
const flakyPrefix = "FLAKY:"
