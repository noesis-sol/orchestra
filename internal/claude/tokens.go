package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// What a worker used: Claude Code writes each assistant message to the session's transcript with the
// model that answered and its usage, the tokens the request took. A message of several content blocks
// (thinking, text, tool calls) is written as a line for each, each with the message's ID and its usage
// in full, so the usage is counted once a message. A subagent's messages (the verifier's) are kept
// apart, in <the session's transcript, without .jsonl>/subagents/agent-<its ID>.jsonl, as Claude Code
// 2.1.285 to 2.1.306 keep them, and counted with the session's; a Claude Code that kept them in the
// session's own transcript, marked as a sidechain, has them counted there.

// usageLine is a transcript line, as far as its tokens need it.
type usageLine struct {
	Time    time.Time `json:"timestamp"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// usageKey is what a line that gives usage has in it: a line without it is skipped unread.
var usageKey = []byte(`"usage"`)

// Tokens returns the tokens the sessions' transcripts, and their subagents', say were used from since
// on, by model, each message counted once, and an error for each transcript that couldn't be read,
// whose tokens are left out. A line it can't read is skipped, as is one with no time: the last line
// may be cut off, being written. A session's subagents with no transcripts of their own (an older
// Claude Code's, or none run) add nothing.
func (Reporter) Tokens(sessions []agentio.Session, since time.Time) (agentio.Tokens, []error) {
	var (
		t    agentio.Tokens
		errs []error
	)
	seen := map[string]bool{} // the messages counted, by ID
	for _, s := range sessions {
		paths := []string{s.Transcript}
		subs, err := subagentTranscripts(s.Transcript)
		if err != nil {
			errs = append(errs, err)
		}
		for _, p := range append(paths, subs...) {
			if err := addTokens(&t, p, since, seen); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return t, errs
}

// subagentTranscripts returns the transcripts of the subagents of the session whose transcript is
// transcript; none when it has no folder of subagents.
func subagentTranscripts(transcript string) ([]string, error) {
	dir := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the subagents' transcripts: %w", err)
	}
	var paths []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(paths)
	return paths, nil
}

// addTokens adds to t what the transcript at path says was used from since on, of the messages not
// in seen, which it adds them to.
func addTokens(t *agentio.Tokens, path string, since time.Time, seen map[string]bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("reading %s: not a plain file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	r := bufio.NewReaderSize(f, chunkBytes)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, usageKey) {
			addLine(t, line, since, seen)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
	}
}

// addLine adds the usage of the transcript line b to t, unless it can't be read, is before since or
// of a message counted already, or used nothing (as Claude Code's own notices, of model <synthetic>).
func addLine(t *agentio.Tokens, b []byte, since time.Time, seen map[string]bool) {
	var l usageLine
	if json.Unmarshal(b, &l) != nil || l.Message.Usage == nil || l.Message.Model == "" || l.Time.Before(since) {
		return
	}
	if id := l.Message.ID; id != "" {
		if seen[id] {
			return
		}
		seen[id] = true
	}
	u := l.Message.Usage
	n := agentio.ModelTokens{Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead}
	if n == (agentio.ModelTokens{}) {
		return
	}
	*t = t.Add(agentio.Tokens{l.Message.Model: n})
}
