package cmux

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// features are what the cmux CLI can do that an older one can't, by its version. From 0.65.0
// (manaflow-ai/cmux#15302) send, send-key and paste refuse to write over a draft in an agent's prompt
// or into a question or permission dialog it has open, unless --force comes before the text or key;
// orchestra presses keys into Claude Code's dialogs, and before 0.65.0 --force is no flag but text,
// typed into the terminal. From 0.65.0 (manaflow-ai/cmux#14837) cmux paste also pastes text from its
// stdin as Cmd+V does: bracketed, when the program asked for it, as Claude Code does, so its lines
// arrive together. From 0.65.0, too, close-workspace and close-surface refuse to close a terminal a
// process runs in, unless given --force.
type features struct {
	force bool // send and send-key take --force, before the text or key; close-workspace and close-surface too
	paste bool // cmux paste pastes its stdin into a surface
}

// guardVersion is the first version of cmux with the input guard, and cmux paste.
var guardVersion = [3]int{0, 65, 0}

// versionPattern is the version cmux version prints first: cmux 0.65.0 (95) [1a2b3c4].
var versionPattern = regexp.MustCompile(`^cmux (\d+)\.(\d+)(?:\.(\d+))?\b`)

// featuresOf are the features of the CLI that cmux version answered: none for an answer without a
// version, as a build of cmux's own may give (cmux version unknown).
func featuresOf(out string) features {
	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return features{}
	}
	var v [3]int
	for i, s := range m[1:] {
		v[i], _ = strconv.Atoi(s) // digits, or "" for a version without a patch number: 0
	}
	for i := range v {
		if v[i] != guardVersion[i] {
			newer := v[i] > guardVersion[i]
			return features{force: newer, paste: newer}
		}
	}
	return features{force: true, paste: true}
}

// cliFile is a cmux CLI as it is on disk: the app replaces it when it updates itself, which it may do
// while a run goes on, and a CLI of another version is asked again.
type cliFile struct {
	path string
	size int64
	mod  time.Time
}

// known are the features of each CLI asked, kept for the run.
var (
	knownMu sync.Mutex
	known   = map[cliFile]features{}
)

// features returns the features of the cmux CLI, asking it (cmux version, which needs no socket)
// once for each version of the file. A CLI that can't be asked is taken to have none of them, as
// cmux 0.64.22, and is asked again next time.
func (t Terminal) features(ctx context.Context) features {
	bin := Bin(ctx)
	if bin == "" {
		return features{}
	}
	fi, err := os.Stat(bin)
	if err != nil {
		return features{}
	}
	file := cliFile{path: bin, size: fi.Size(), mod: fi.ModTime()}
	knownMu.Lock()
	f, ok := known[file]
	knownMu.Unlock()
	if ok {
		return f
	}
	out, err := command.OutputWithInput(ctx, command.ReadLimit, "", []string{"CMUX_QUIET=1"}, "", bin, "version")
	if err != nil {
		return features{}
	}
	f = featuresOf(out)
	knownMu.Lock()
	known[file] = f
	knownMu.Unlock()
	return f
}
