// Package bdjson runs bd and reads its --json output: the one place that knows bd's JSON envelope,
// for internal/beads and for internal/project, which can't import internal/beads (it imports
// internal/dispatch, which imports internal/project).
package bdjson

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Env pins the bd --json shape to the bare payload. Only bd gets it: given to every command, it
// reached a tmux server orchestra started, and through it every pane the user opened there later.
var Env = []string{"BD_JSON_ENVELOPE=0"}

// Run runs bd in repo with these arguments and Env, stopped at limit, and returns its output.
func Run(ctx context.Context, limit time.Duration, repo string, args ...string) (string, error) {
	return command.OutputWithEnv(ctx, limit, repo, Env, "bd", args...)
}

// Unwrap returns the payload of bd's --json output raw, in either shape: bare, or in bd's envelope
// {"schema_version": …, "data": …}. Only an object with both is the envelope, so a bare object that
// has a data field of its own is read as it is.
func Unwrap(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return raw
	}
	var env struct {
		Version *json.RawMessage `json:"schema_version"`
		Data    json.RawMessage  `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Version != nil && env.Data != nil {
		return env.Data
	}
	return raw
}
