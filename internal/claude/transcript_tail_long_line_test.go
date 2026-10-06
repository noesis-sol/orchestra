package claude

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// recordSession records session testID with its transcript at p as the worker's in a new worktree,
// as its SessionStart hook does, and returns the worktree.
func recordSession(t *testing.T, p string) string {
	t.Helper()
	wt := t.TempDir()
	root, err := project.OpenRun(wt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := project.WriteRun(root, wt, project.RunPath(sessionName), []byte(sessionInput(testID, p)), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt
}

// screenshot is a transcript line of over n bytes: a tool result with an image in it.
func screenshot(n int) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"image","source":` +
		`{"type":"base64","data":"` + strings.Repeat("A", n) + `"}}]}]}}`
}

// message is the transcript line of the user's message i, padded to vary the lines' lengths.
func message(i int) string {
	return fmt.Sprintf(`{"type":"user","message":{"content":"message %d %s"}}`, i, strings.Repeat("y", i*37%3000))
}

// A transcript whose last line is longer than the part of it read still gives its last entries, the
// long line skipped.
func TestTranscriptTailAfterAVeryLongLastLine(t *testing.T) {
	var lines []string
	var want []string
	for i := range tailEntries + 10 {
		lines = append(lines, fmt.Sprintf(`{"type":"user","message":{"content":"message %d"}}`, i))
		if i >= 10 {
			want = append(want, fmt.Sprintf("user: message %d", i))
		}
	}
	lines = append(lines, screenshot(2*tailBytes))
	wt := recordSession(t, transcriptAt(t, t.TempDir(), testID, lines...))
	if got := (Reporter{}).TranscriptTail(wt); got != strings.Join(want, "\n") {
		t.Errorf("TranscriptTail = %.60q … %q", got, got[max(len(got)-60, 0):])
	}
}

// Long lines among the last ones are skipped, and the lines around them read whole, wherever the
// reads back from the end fall.
func TestTranscriptTailAroundVeryLongLines(t *testing.T) {
	var lines, want []string
	for i := range 300 {
		if i%25 == 7 {
			lines = append(lines, screenshot(lineBytes+i))
			continue
		}
		lines = append(lines, message(i))
		want = append(want, message(i))
	}
	wt := recordSession(t, transcriptAt(t, t.TempDir(), testID, lines...))
	got, w := Reporter{}.TranscriptTail(wt), transcriptTail([]byte(strings.Join(want, "\n")))
	if got != w || got == "" {
		t.Errorf("TranscriptTail = %.60q … %q, want %.60q … %q", got, got[max(len(got)-60, 0):], w, w[max(len(w)-60, 0):])
	}
}

// readTail keeps whole lines of up to lineBytes, the transcript's first among them, and skips longer
// ones.
func TestReadTailLines(t *testing.T) {
	fits, over := strings.Repeat("f", lineBytes), strings.Repeat("o", lineBytes+1)
	for _, tc := range []struct {
		name, file, want string
	}{
		{"empty", "", ""},
		{"one line", "a", "a"},
		{"a newline at the end", "a\nb\n", "a\nb"},
		{"empty lines", "\n\na\n\nb\n", "a\nb"},
		{"the longest line", "a\n" + fits + "\nb\n", "a\n" + fits + "\nb"},
		{"a longer line", "a\n" + over + "\nb\n", "a\nb"},
		{"a longer first line", over + "\na\n", "a"},
		{"a longer last line", "a\n" + over, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(p, []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			got, err := readTail(f, tailBytes)
			if err != nil || !bytes.Equal(got, []byte(tc.want)) {
				t.Errorf("readTail = %.40q (%d bytes), %v; want %.40q (%d bytes)", got, len(got), err, tc.want, len(tc.want))
			}
		})
	}
}

// readTail stops at the line that brings it to n bytes, and reads no line in part.
func TestReadTailStopsAtNBytes(t *testing.T) {
	var file bytes.Buffer
	for i := range 2000 {
		file.WriteString(message(i) + "\n")
	}
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(p, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, err := readTail(f, tailBytes)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got) + 1; n < tailBytes || n >= tailBytes+len(message(1999))+3000 ||
		!bytes.HasSuffix(file.Bytes(), append(got, '\n')) || !bytes.HasPrefix(got, []byte(`{"type":"user"`)) {
		t.Errorf("readTail: %d bytes, %.40q … %q", len(got), got, got[max(len(got)-40, 0):])
	}
}
