package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Ticket IDs read <prefix>-<hash>[.<child>]: the prefix names every branch (wt/<id>), worktree, tab
// and log line. bd init sets it, from the folder's name unless given one, and bd rename-prefix changes
// it, rewriting every ticket's ID and every mention of one in the tickets' fields. Nothing outside
// Beads is rewritten: a branch, a carried worker or an unmerged ticket's fork naming an old ID would
// name one that no longer exists, so init refuses to rename while any does.

// prefixLabel is the label of the ticket prefix's step in init's summary.
const prefixLabel = "ticket prefix"

// NormalPrefix is the prefix p as typed, without the trailing hyphen bd rename-prefix wants.
func NormalPrefix(p string) string {
	return strings.TrimSuffix(strings.TrimSpace(p), "-")
}

// PrefixProblem says why p (a NormalPrefix) can't be a ticket prefix, as bd requires one, or returns
// "": lowercase letters, digits and hyphens, starting with a letter, not ending in a hyphen.
func PrefixProblem(p string) string {
	switch {
	case p == "":
		return "is empty"
	case p[0] < 'a' || p[0] > 'z':
		return fmt.Sprintf("must start with a lowercase letter (got '%s')", p)
	case strings.HasSuffix(p, "-"):
		return fmt.Sprintf("must not end in a hyphen (got '%s')", p)
	}
	for _, r := range p {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Sprintf("must hold only lowercase letters, digits and hyphens (got '%s')", p)
		}
	}
	return ""
}

// DefaultPrefix is the prefix bd init gives a repository it isn't given one for: its folder's name,
// with dots made underscores.
func DefaultPrefix(repo string) string {
	return strings.ReplaceAll(filepath.Base(repo), ".", "_")
}

// RenamedID is ticket id with its prefix from replaced by to, as bd rename-prefix renames it.
func RenamedID(id, from, to string) string {
	if rest, ok := strings.CutPrefix(id, from+"-"); ok {
		return to + "-" + rest
	}
	return id
}

// BeadsPrefix reads the ticket prefix of the Beads set up in repo: "" when bd has none.
func BeadsPrefix(ctx context.Context, repo string) (string, error) {
	var v struct {
		Value string `json:"value"`
	}
	if err := bdJSON(ctx, repo, &v, "config", "get", "issue_prefix", "--json"); err != nil {
		return "", err
	}
	return strings.TrimSpace(v.Value), nil
}

// PrefixTickets counts the tickets in repo's Beads, closed or not, and returns one's ID ("" for none),
// for the rename's confirmation.
func PrefixTickets(ctx context.Context, repo string) (n int, example string, err error) {
	var count struct {
		Count int `json:"count"`
	}
	if err := bdJSON(ctx, repo, &count, "count", "--json"); err != nil {
		return 0, "", err
	}
	var list []struct {
		ID string `json:"id"`
	}
	if err := bdJSON(ctx, repo, &list, "list", "--all", "--limit", "1", "--json"); err != nil {
		return 0, "", err
	}
	if len(list) > 0 {
		example = list[0].ID
	}
	return count.Count, example, nil
}

// bdJSON runs bd with args in repo and decodes its JSON output into v, bare or in bd's envelope
// ({"schema_version": …, "data": …}).
func bdJSON(ctx context.Context, repo string, v any, args ...string) error {
	out, err := command.Output(ctx, command.ReadLimit, repo, "bd", args...)
	if err != nil {
		return err
	}
	raw := []byte(out)
	var env struct {
		Version *int            `json:"schema_version"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Version != nil && env.Data != nil {
		raw = env.Data
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("could not read 'bd %s': %w", strings.Join(args, " "), err)
	}
	return nil
}

// bdInitArgs are bd init's arguments for the choice: the prefix chosen, where it isn't bd's own.
func bdInitArgs(c Choice) []string {
	args := append([]string{}, bdInit...)
	if c.Prefix != "" && c.Prefix != c.PrefixWas {
		args = append(args, "--prefix", c.Prefix)
	}
	return args
}

// newPrefix is the prefix the choice gives the tickets: Prefix, or PrefixWas where it keeps it.
func newPrefix(c Choice) string {
	if c.Prefix != "" {
		return c.Prefix
	}
	return c.PrefixWas
}

// RenameQuestion is the confirmation the rename of the choice's tickets asks for: how many, with an
// example ID renamed.
func RenameQuestion(c Choice) string {
	example := c.PrefixExample
	if example == "" {
		example = c.PrefixWas + "-…"
	}
	tickets := fmt.Sprintf("%d tickets", c.PrefixTickets)
	if c.PrefixTickets == 1 {
		tickets = "the ticket"
	}
	return fmt.Sprintf("Rename %s, %s → %s?", tickets, example, RenamedID(example, c.PrefixWas, newPrefix(c)))
}

// ApplyPrefix gives the tickets of the Beads set up in repo the prefix the choice says, with bd
// rename-prefix, and returns the step and whether there is one: none where init didn't ask for the
// prefix, or set up a new Beads (SetUpBeads gives that one its prefix). It renames nothing where the
// choice keeps the prefix, the rename isn't confirmed, or refusal (PrefixRefusal, read again) says
// why the tickets can't be renamed now.
func ApplyPrefix(ctx context.Context, repo string, c Choice, refusal string) (Step, bool) {
	prefix := newPrefix(c)
	switch {
	case c.PrefixWas == "" || c.PrefixNew:
		return Step{}, false
	case prefix == c.PrefixWas:
		return Step{Kind: StepKept, Label: prefixLabel, Detail: prefix + ", kept"}, true
	case !c.RenamePrefix && c.PrefixTickets > 0:
		return Step{Kind: StepKept, Label: prefixLabel,
			Detail: c.PrefixWas + ", kept: renaming the tickets to " + prefix + " was declined"}, true
	case refusal != "":
		return Step{Kind: StepMissing, Label: prefixLabel, Detail: c.PrefixWas + ", kept: the tickets can't be " +
			"renamed to " + prefix + " while " + refusal}, true
	}
	if _, err := command.Output(ctx, command.WriteLimit, repo, "bd", "rename-prefix", prefix+"-"); err != nil {
		why, stderr := err, ""
		if e, ok := errors.AsType[*command.Error](err); ok {
			why, stderr = e.Err, e.Stderr
		}
		return Step{Kind: StepMissing, Label: prefixLabel, Detail: c.PrefixWas + ", kept: " +
			failure("bd rename-prefix "+prefix+"-", why, stderr)}, true
	}
	renamed := fmt.Sprintf("%d tickets renamed", c.PrefixTickets)
	if c.PrefixTickets == 1 {
		renamed = "1 ticket renamed"
	}
	return Step{Kind: StepDone, Label: prefixLabel, Detail: prefix + " (was " + c.PrefixWas + "), " + renamed}, true
}
