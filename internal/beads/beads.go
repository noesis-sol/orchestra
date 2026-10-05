// Package beads is orchestra's adapter for Beads (the bd command): the ready queue, a ticket's
// status and dependencies, and the notes and deferrals orchestra writes back.
package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// unwrap accepts either bd JSON shape: the bare payload, or the v2 envelope {schema_version, data}.
func unwrap(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Data != nil {
			return env.Data
		}
	}
	return raw
}

// parseReady returns the open tickets from 'bd ready --json', highest priority (lowest number) first.
// Questions for the maintainer and tickets of the excluded issue types are left out.
func parseReady(raw []byte, excludeTypes []string) ([]dispatch.Ticket, error) {
	var all []dispatch.Ticket
	if err := json.Unmarshal(unwrap(raw), &all); err != nil {
		return nil, err
	}
	var open []dispatch.Ticket
	for _, t := range all {
		if t.Status == dispatch.StatusOpen && !slices.Contains(excludeTypes, t.IssueType) &&
			!dispatch.HasLabel(t, dispatch.HumanLabel) {
			open = append(open, t)
		}
	}
	byPriority(open)
	return open, nil
}

// byPriority sorts tickets highest priority (lowest number) first, keeping bd's order otherwise.
func byPriority(ts []dispatch.Ticket) {
	prio := func(t dispatch.Ticket) int {
		if t.Priority == nil {
			return 9
		}
		return *t.Priority
	}
	sort.SliceStable(ts, func(i, j int) bool { return prio(ts[i]) < prio(ts[j]) })
}

// parseList returns the tickets from 'bd list --json'; with status, only those in it.
func parseList(raw []byte, status dispatch.TicketStatus) ([]dispatch.Ticket, error) {
	var all []dispatch.Ticket
	if err := json.Unmarshal(unwrap(raw), &all); err != nil {
		return nil, err
	}
	var kept []dispatch.Ticket
	for _, t := range all {
		if status == "" || t.Status == status {
			kept = append(kept, t)
		}
	}
	return kept, nil
}

// parseOpen returns the open tickets from 'bd list --json', leaving out questions for the
// maintainer and tickets of the excluded issue types, and the blocks links among their
// dependencies (see parseLinked).
func parseOpen(raw []byte, excludeTypes []string) ([]dispatch.Ticket, []dispatch.Link, error) {
	all, links, err := parseLinked(raw)
	if err != nil {
		return nil, nil, err
	}
	var open []dispatch.Ticket
	for _, t := range all {
		if t.Status == dispatch.StatusOpen && !slices.Contains(excludeTypes, t.IssueType) &&
			!dispatch.HasLabel(t, dispatch.HumanLabel) {
			open = append(open, t)
		}
	}
	return open, links, nil
}

// parseLinked returns the tickets from 'bd list --json' and the blocks links among their
// dependencies. bd list gives each dependency as a link (issue_id, depends_on_id, type), not as a
// ticket, so the tickets' Dependencies are left empty.
func parseLinked(raw []byte) ([]dispatch.Ticket, []dispatch.Link, error) {
	var all []dispatch.Ticket
	if err := json.Unmarshal(unwrap(raw), &all); err != nil {
		return nil, nil, err
	}
	var deps []struct {
		Dependencies []struct {
			IssueID     string `json:"issue_id"`
			DependsOnID string `json:"depends_on_id"`
			Type        string `json:"type"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(unwrap(raw), &deps); err != nil {
		return nil, nil, err
	}
	var links []dispatch.Link
	for i := range all {
		for _, d := range deps[i].Dependencies {
			if d.Type == "blocks" && d.IssueID != "" && d.DependsOnID != "" {
				links = append(links, dispatch.Link{Blocker: d.DependsOnID, Blocked: d.IssueID})
			}
		}
		all[i].Dependencies = nil
	}
	return all, links, nil
}

// Tracker is Beads for one repository, as the loop uses it.
type Tracker struct {
	Repo         string
	ExcludeTypes []string // issue types never dispatched, such as epics, whose children are the work
}

// Ready returns the open tickets bd considers ready, highest priority first; questions for the
// maintainer and tickets of the excluded types are left out. bd filters them and parseReady filters
// again, and the query has no limit, so work behind more than bd's default 100 ready entries is
// still seen. With a scope, only that ticket and its descendants (bd ready --parent) are returned.
// Like the bash version it judges by the output; when that can't be read, the error carries bd's
// stderr.
func (b Tracker) Ready(ctx context.Context, scope string) ([]dispatch.Ticket, error) {
	all, err := b.ready(ctx)
	if err != nil || scope == "" {
		return all, err
	}
	under, err := b.ready(ctx, "--parent", scope)
	if err != nil {
		return nil, err
	}
	for _, t := range all {
		if t.ID == scope {
			under = append(under, t)
		}
	}
	byPriority(under)
	return under, nil
}

// ready runs bd ready with the filters every read uses and these extra arguments.
func (b Tracker) ready(ctx context.Context, extra ...string) ([]dispatch.Ticket, error) {
	args := []string{"ready", "--json", "--limit", "0", "--exclude-label", dispatch.HumanLabel}
	if len(b.ExcludeTypes) > 0 {
		args = append(args, "--exclude-type", strings.Join(b.ExcludeTypes, ","))
	}
	out, runErr := command.Output(ctx, command.ReadLimit, b.Repo, "bd", append(args, extra...)...)
	ready, err := parseReady([]byte(out), b.ExcludeTypes)
	switch {
	case err != nil && runErr != nil:
		return nil, runErr
	case err != nil:
		return nil, fmt.Errorf("could not parse 'bd ready --json': %w", err)
	}
	return ready, nil
}

// Closed returns the closed tickets carrying the label. When bd's output can't be read, the error
// carries bd's stderr.
func (b Tracker) Closed(ctx context.Context, label string) ([]dispatch.Ticket, error) {
	return b.list(ctx, dispatch.StatusClosed, "--status", "closed", "--label", label)
}

// Unclosed returns every ticket that isn't closed, without its text: what the loop needs is each
// one's parent.
func (b Tracker) Unclosed(ctx context.Context) ([]dispatch.Ticket, error) {
	return b.list(ctx, "", "--brief")
}

// Descendants returns the ticket's subtickets, their subtickets and so on, closed or not, without
// their text. bd lists one level at a time.
func (b Tracker) Descendants(ctx context.Context, id string) ([]dispatch.Ticket, error) {
	var all []dispatch.Ticket
	seen := map[string]bool{id: true}
	for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
		children, err := b.list(ctx, "", "--all", "--brief", "--parent", queue[0])
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if !seen[c.ID] {
				seen[c.ID] = true
				all = append(all, c)
				queue = append(queue, c.ID)
			}
		}
	}
	return all, nil
}

// Children returns the ticket's subtickets that aren't closed, with their text and metadata, and
// the blocks links bd has for them. When bd's output can't be read, the error carries bd's stderr.
func (b Tracker) Children(ctx context.Context, id string) ([]dispatch.Ticket, []dispatch.Link, error) {
	out, runErr := command.Output(ctx, command.ReadLimit, b.Repo, "bd", "list", "--json", "--limit", "0", "--parent", id)
	children, links, err := parseLinked([]byte(out))
	switch {
	case err != nil && runErr != nil:
		return nil, nil, runErr
	case err != nil:
		return nil, nil, fmt.Errorf("could not parse 'bd list --json': %w", err)
	}
	return children, links, nil
}

// list runs 'bd list --json' without a limit and these arguments, keeping the tickets in status
// ("" for all it lists). When bd's output can't be read, the error carries bd's stderr.
func (b Tracker) list(ctx context.Context, status dispatch.TicketStatus, args ...string) ([]dispatch.Ticket, error) {
	args = append([]string{"list", "--json", "--limit", "0"}, args...)
	out, runErr := command.Output(ctx, command.ReadLimit, b.Repo, "bd", args...)
	tickets, err := parseList([]byte(out), status)
	switch {
	case err != nil && runErr != nil:
		return nil, runErr
	case err != nil:
		return nil, fmt.Errorf("could not parse 'bd list --json': %w", err)
	}
	return tickets, nil
}

// Open returns the open tickets, leaving out questions for the maintainer and tickets of the
// excluded types, and the blocks links bd already has for them. When bd's output can't be read,
// the error carries bd's stderr.
func (b Tracker) Open(ctx context.Context) ([]dispatch.Ticket, []dispatch.Link, error) {
	out, runErr := command.Output(ctx, command.ReadLimit, b.Repo,
		"bd", "list", "--json", "--status", "open", "--limit", "0")
	open, links, err := parseOpen([]byte(out), b.ExcludeTypes)
	switch {
	case err != nil && runErr != nil:
		return nil, nil, runErr
	case err != nil:
		return nil, nil, fmt.Errorf("could not parse 'bd list --json': %w", err)
	}
	return open, links, nil
}

// AddBlock links two tickets so that blocked waits for blocker.
func (b Tracker) AddBlock(ctx context.Context, blocker, blocked string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "dep", "add", blocked, blocker)
	return err
}

// parseTicket reads one ticket from 'bd show --json'; ok is false if it cannot be read.
func parseTicket(raw []byte) (dispatch.Ticket, bool) {
	data := unwrap(raw)
	var list []dispatch.Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0], true
		}
		return dispatch.Ticket{}, false
	}
	var t dispatch.Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t, true
	}
	return dispatch.Ticket{}, false
}

// Show returns the ticket with its dependencies. If it cannot be read, Status is StatusUnknown and the
// error says why.
func (b Tracker) Show(ctx context.Context, id string) (dispatch.Ticket, error) {
	out, err := command.Output(ctx, command.ReadLimit, b.Repo, "bd", "show", id, "--json")
	t, ok := parseTicket([]byte(out))
	if !ok {
		return dispatch.Ticket{ID: id, Status: dispatch.StatusUnknown}, unreadable(id, out, err)
	}
	return t, nil
}

// Status returns the ticket's status. If it cannot be read, it is StatusUnknown and the error says why.
func (b Tracker) Status(ctx context.Context, id string) (dispatch.TicketStatus, error) {
	t, err := b.Show(ctx, id)
	return t.Status, err
}

// unreadable says why 'bd show --json' gave no status: bd failed (the error carries its stderr), or
// its output held none.
func unreadable(id, out string, err error) error {
	if err != nil {
		return err
	}
	out = strings.TrimSpace(out)
	if r := []rune(out); len(r) > 200 {
		out = string(r[:200]) + "…"
	}
	return fmt.Errorf("'bd show %s --json' gave no status: %q", id, out)
}

// AppendNotes adds a note to the ticket.
func (b Tracker) AppendNotes(ctx context.Context, id, note string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "update", id, "--append-notes", note)
	return err
}

// Defer sets the ticket aside, with the reason.
func (b Tracker) Defer(ctx context.Context, id, reason string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "defer", id, "--reason="+reason)
	return err
}

// Describe returns 'bd show' for the ticket, as a person reads it.
func (b Tracker) Describe(ctx context.Context, id string) string {
	out, err := command.Output(ctx, command.ReadLimit, b.Repo, "bd", "show", id)
	if err != nil {
		return fmt.Sprintf("(bd show %s failed: %v)", id, err)
	}
	return out
}

// Reopen puts the ticket back in the queue.
func (b Tracker) Reopen(ctx context.Context, id string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "update", id, "--status", "open")
	return err
}

// AddLabel adds the label to the ticket.
func (b Tracker) AddLabel(ctx context.Context, id, label string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "label", "add", id, label)
	return err
}

// RemoveLabel removes the label from the ticket.
func (b Tracker) RemoveLabel(ctx context.Context, id, label string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "label", "remove", id, label)
	return err
}

// SetMetadata sets one key of the ticket's metadata, keeping the others.
func (b Tracker) SetMetadata(ctx context.Context, id, key, value string) error {
	_, err := command.Output(ctx, command.WriteLimit, b.Repo, "bd", "update", id, "--set-metadata", key+"="+value)
	return err
}

// NewTicket is a ticket to file with Create.
type NewTicket struct {
	Title, Description, Acceptance string
	Type                           string // bd's issue type: epic, feature, task, bug or chore
	Priority                       int    // 0 (critical) to 4 (backlog)
	Parent                         string // "" for none
	Files                          []string
	Labels                         []string
}

// FileBug files a P2 bug carrying label and returns its ID.
func (b Tracker) FileBug(ctx context.Context, title, description, label string) (string, error) {
	return b.Create(ctx, NewTicket{Title: title, Description: description, Type: "bug", Priority: 2,
		Labels: []string{label}})
}

// Create files the ticket and returns its ID. Files go in the metadata under dispatch.FilesKey,
// which footprint scheduling reads. When bd's output can't be read, the error carries bd's stderr.
func (b Tracker) Create(ctx context.Context, t NewTicket) (string, error) {
	args := []string{"create", "--json", "--title=" + t.Title, "--description=" + t.Description,
		"--type=" + t.Type, "--priority=" + fmt.Sprint(t.Priority)}
	if t.Acceptance != "" {
		args = append(args, "--acceptance="+t.Acceptance)
	}
	if t.Parent != "" {
		args = append(args, "--parent="+t.Parent)
	}
	if len(t.Labels) > 0 {
		args = append(args, "--labels="+strings.Join(t.Labels, ","))
	}
	if len(t.Files) > 0 {
		meta, err := json.Marshal(map[string][]string{dispatch.FilesKey: t.Files})
		if err != nil {
			return "", err
		}
		args = append(args, "--metadata="+string(meta))
	}
	out, runErr := command.Output(ctx, command.WriteLimit, b.Repo, "bd", args...)
	if runErr != nil {
		return "", runErr
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(unwrap([]byte(out)), &created); err != nil || created.ID == "" {
		return "", fmt.Errorf("'bd create --json' gave no ID: %q", strings.TrimSpace(out))
	}
	return created.ID, nil
}
