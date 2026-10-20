// Package beads is orchestra's adapter for Beads (the bd command): the ready queue, a ticket's
// status and dependencies, and the notes and deferrals orchestra writes back.
package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads/bdjson"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/tracker"
)

// parseReady returns the open tickets from 'bd ready --json', highest priority (lowest number) first.
// Questions for the maintainer and tickets of the excluded issue types are left out.
func parseReady(raw []byte, excludeTypes []string) ([]tracker.Ticket, error) {
	var all []tracker.Ticket
	if err := json.Unmarshal(bdjson.Unwrap(raw), &all); err != nil {
		return nil, err
	}
	var open []tracker.Ticket
	for _, t := range all {
		if dispatchable(t, excludeTypes) {
			open = append(open, t)
		}
	}
	byPriority(open)
	return open, nil
}

// dispatchable reports whether ticket t is open, not a question for the maintainer, and not of the
// excluded issue types.
func dispatchable(t tracker.Ticket, excludeTypes []string) bool {
	return t.Status == tracker.StatusOpen && !slices.Contains(excludeTypes, t.IssueType) &&
		!tracker.HasLabel(t, tracker.HumanLabel)
}

// byPriority sorts tickets highest priority (lowest number) first, keeping bd's order otherwise.
func byPriority(ts []tracker.Ticket) {
	prio := func(t tracker.Ticket) int {
		if t.Priority == nil {
			return 9
		}
		return *t.Priority
	}
	sort.SliceStable(ts, func(i, j int) bool { return prio(ts[i]) < prio(ts[j]) })
}

// parseList returns the tickets from 'bd list --json'; with status, only those in it.
func parseList(raw []byte, status tracker.Status) ([]tracker.Ticket, error) {
	var all []tracker.Ticket
	if err := json.Unmarshal(bdjson.Unwrap(raw), &all); err != nil {
		return nil, err
	}
	var kept []tracker.Ticket
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
func parseOpen(raw []byte, excludeTypes []string) ([]tracker.Ticket, []tracker.Link, error) {
	all, links, err := parseLinked(raw)
	if err != nil {
		return nil, nil, err
	}
	var open []tracker.Ticket
	for _, t := range all {
		if dispatchable(t, excludeTypes) {
			open = append(open, t)
		}
	}
	return open, links, nil
}

// parseLinked returns the tickets from 'bd list --json' and the blocks links among their
// dependencies. bd list gives each dependency as a link (issue_id, depends_on_id, type), not as a
// ticket, so the tickets' Dependencies are left empty.
func parseLinked(raw []byte) ([]tracker.Ticket, []tracker.Link, error) {
	var all []tracker.Ticket
	if err := json.Unmarshal(bdjson.Unwrap(raw), &all); err != nil {
		return nil, nil, err
	}
	var deps []struct {
		Dependencies []struct {
			IssueID     string `json:"issue_id"`
			DependsOnID string `json:"depends_on_id"`
			Type        string `json:"type"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(bdjson.Unwrap(raw), &deps); err != nil {
		return nil, nil, err
	}
	var links []tracker.Link
	for i := range all {
		for _, d := range deps[i].Dependencies {
			if d.Type == "blocks" && d.IssueID != "" && d.DependsOnID != "" {
				links = append(links, tracker.Link{Blocker: d.DependsOnID, Blocked: d.IssueID})
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

// bd runs bd in the repository with these arguments, stopped at limit.
func (b Tracker) bd(ctx context.Context, limit time.Duration, args ...string) (string, error) {
	return bdjson.Run(ctx, limit, b.Repo, args...)
}

// Ready returns the open tickets bd considers ready, highest priority first; questions for the
// maintainer and tickets of the excluded types are left out. bd filters them and parseReady filters
// again, and the query has no limit, so work behind more than bd's default 100 ready entries is
// still seen. With a scope, only that ticket and its descendants (bd ready --parent) are returned.
// The output decides, not bd's exit status: a list that parses is bd's answer, even should bd exit
// non-zero over something beside the read. Only output that can't be read is an error, which then
// carries bd's stderr.
func (b Tracker) Ready(ctx context.Context, scope string) ([]tracker.Ticket, error) {
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
func (b Tracker) ready(ctx context.Context, extra ...string) ([]tracker.Ticket, error) {
	args := []string{"ready", "--json", "--limit", "0", "--exclude-label", tracker.HumanLabel}
	if len(b.ExcludeTypes) > 0 {
		args = append(args, "--exclude-type", strings.Join(b.ExcludeTypes, ","))
	}
	out, runErr := b.bd(ctx, command.ReadLimit, append(args, extra...)...)
	ready, err := parseReady([]byte(out), b.ExcludeTypes)
	if err = readFailed("bd ready --json", err, runErr); err != nil {
		return nil, err
	}
	return ready, nil
}

// readFailed is the error of a read of cmd's output, which failed to parse with parseErr, when bd
// ran with runErr: bd's error when it failed too, as that carries its stderr; nil when the output
// parsed.
func readFailed(cmd string, parseErr, runErr error) error {
	switch {
	case parseErr == nil:
		return nil
	case runErr != nil:
		return runErr
	}
	return fmt.Errorf("could not parse '%s': %w", cmd, parseErr)
}

// Closed returns the closed tickets carrying the label. When bd's output can't be read, the error
// carries bd's stderr.
func (b Tracker) Closed(ctx context.Context, label string) ([]tracker.Ticket, error) {
	return b.list(ctx, tracker.StatusClosed, "--status", "closed", "--label", label)
}

// Unclosed returns every ticket that isn't closed, without its text: what the loop needs is each
// one's parent.
func (b Tracker) Unclosed(ctx context.Context) ([]tracker.Ticket, error) {
	return b.list(ctx, "", "--brief")
}

// Descendants returns the ticket's subtickets, their subtickets and so on, closed or not, without
// their text. bd lists one level at a time.
func (b Tracker) Descendants(ctx context.Context, id string) ([]tracker.Ticket, error) {
	var all []tracker.Ticket
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
func (b Tracker) Children(ctx context.Context, id string) ([]tracker.Ticket, []tracker.Link, error) {
	out, runErr := b.bd(ctx, command.ReadLimit, "list", "--json", "--limit", "0", "--parent", id)
	children, links, err := parseLinked([]byte(out))
	if err = readFailed("bd list --json", err, runErr); err != nil {
		return nil, nil, err
	}
	return children, links, nil
}

// WithMetadata returns every ticket, closed or not, whose metadata gives key the value, with its text,
// metadata and parent: those a spec's breakdown filed, by the spec's path under SpecKey, say. When
// bd's output can't be read, the error carries bd's stderr.
func (b Tracker) WithMetadata(ctx context.Context, key, value string) ([]tracker.Ticket, error) {
	return b.list(ctx, "", "--all", "--metadata-field", key+"="+value)
}

// Cycles returns the dependency cycles bd finds among every ticket (bd dep cycles), each as the IDs
// of its tickets in the order of its links; none when there is none. bd gives each cycle as an
// object listing its members, or, from an older bd, as the list of its tickets.
func (b Tracker) Cycles(ctx context.Context) ([][]string, error) {
	out, runErr := b.bd(ctx, command.ReadLimit, "dep", "cycles", "--json")
	cycles, err := parseCycles([]byte(out))
	if err = readFailed("bd dep cycles --json", err, runErr); err != nil {
		return nil, err
	}
	return cycles, nil
}

// parseCycles reads 'bd dep cycles --json'.
func parseCycles(raw []byte) ([][]string, error) {
	var all []json.RawMessage
	if err := json.Unmarshal(bdjson.Unwrap(raw), &all); err != nil {
		return nil, err
	}
	type member struct {
		ID string `json:"id"`
	}
	var cycles [][]string
	for _, c := range all {
		var listed struct {
			Members []member `json:"members"`
		}
		var members []member
		if json.Unmarshal(c, &listed) == nil {
			members = listed.Members
		} else if err := json.Unmarshal(c, &members); err != nil {
			return nil, err
		}
		var ids []string
		for _, m := range members {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
		if len(ids) > 0 {
			cycles = append(cycles, ids)
		}
	}
	return cycles, nil
}

// list runs 'bd list --json' without a limit and these arguments, keeping the tickets in status
// ("" for all it lists). When bd's output can't be read, the error carries bd's stderr.
func (b Tracker) list(ctx context.Context, status tracker.Status, args ...string) ([]tracker.Ticket, error) {
	args = append([]string{"list", "--json", "--limit", "0"}, args...)
	out, runErr := b.bd(ctx, command.ReadLimit, args...)
	tickets, err := parseList([]byte(out), status)
	if err = readFailed("bd list --json", err, runErr); err != nil {
		return nil, err
	}
	return tickets, nil
}

// Open returns the open tickets, leaving out questions for the maintainer and tickets of the
// excluded types, and the blocks links bd already has for them. When bd's output can't be read,
// the error carries bd's stderr.
func (b Tracker) Open(ctx context.Context) ([]tracker.Ticket, []tracker.Link, error) {
	out, runErr := b.bd(ctx, command.ReadLimit, "list", "--json", "--status", "open", "--limit", "0")
	open, links, err := parseOpen([]byte(out), b.ExcludeTypes)
	if err = readFailed("bd list --json", err, runErr); err != nil {
		return nil, nil, err
	}
	return open, links, nil
}

// AddBlock links two tickets so that blocked waits for blocker.
func (b Tracker) AddBlock(ctx context.Context, blocker, blocked string) error {
	if err := b.exists(ctx, blocked, blocker); err != nil {
		return err
	}
	return b.AddCheckedBlock(ctx, blocker, blocked)
}

// AddCheckedBlock is AddBlock for two tickets the caller has made sure bd has by those very IDs (it
// filed them, or Show gave them): it doesn't show them again.
func (b Tracker) AddCheckedBlock(ctx context.Context, blocker, blocked string) error {
	_, err := b.bd(ctx, command.WriteLimit, "dep", "add", blocked, blocker)
	return err
}

// parseTicket reads one ticket from 'bd show --json'; ok is false if it cannot be read.
func parseTicket(raw []byte) (tracker.Ticket, bool) {
	data := bdjson.Unwrap(raw)
	var list []tracker.Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0], true
		}
		return tracker.Ticket{}, false
	}
	var t tracker.Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t, true
	}
	return tracker.Ticket{}, false
}

// Show returns the ticket with its dependencies. If it cannot be read, Status is StatusUnknown and the
// error says why. bd answers an ID it has no ticket for with a ticket whose ID starts with it, if there
// is one (a deleted ticket's follow-up, say): Show takes that for not found.
func (b Tracker) Show(ctx context.Context, id string) (tracker.Ticket, error) {
	out, err := b.bd(ctx, command.ReadLimit, "show", id, "--json")
	t, ok := parseTicket([]byte(out))
	switch {
	case ok && t.ID != id:
		err = fmt.Errorf("'bd show %s --json' gave %s, another ticket: bd has none by that ID", id, t.ID)
		return tracker.Ticket{ID: id, Status: tracker.StatusUnknown}, &NotFoundError{ID: id, Err: err}
	case !ok && err != nil && noSuchTicket([]byte(out)):
		return tracker.Ticket{ID: id, Status: tracker.StatusUnknown}, &NotFoundError{ID: id, Err: err}
	case !ok:
		return tracker.Ticket{ID: id, Status: tracker.StatusUnknown}, unreadable(id, out, err)
	}
	return t, nil
}

// exists returns Show's not-found error for the first of the tickets bd has none by: bd's writes,
// like its show, act on a ticket whose ID starts with the one they are given. When Show fails
// otherwise, it is nil, and the write says whether bd can do it.
func (b Tracker) exists(ctx context.Context, ids ...string) error {
	for _, id := range ids {
		if _, err := b.Show(ctx, id); errors.Is(err, tracker.ErrNoTicket) {
			return err
		}
	}
	return nil
}

// NotFoundError is 'bd show' failing because bd has no trace of the ticket, as one deleted from it,
// rather than failing to say: errors.Is takes it for tracker.ErrNoTicket. Err is bd's failure,
// carrying its stderr.
type NotFoundError struct {
	ID  string
	Err error
}

func (e *NotFoundError) Error() string { return e.Err.Error() }

func (e *NotFoundError) Unwrap() error { return e.Err }

// Is reports whether target is tracker.ErrNoTicket.
func (e *NotFoundError) Is(target error) bool { return target == tracker.ErrNoTicket }

// bdNoSuchTicket is the error 'bd show --json' gives, on stdout as it exits 1, for an ID it has no
// ticket for (bd 1.3: "Issue <id> not found" on stderr).
const bdNoSuchTicket = "no issues found matching the provided IDs"

// noSuchTicket reports whether 'bd show --json' said it has no ticket by the ID it was given.
func noSuchTicket(raw []byte) bool {
	var e struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(bytes.TrimSpace(raw), &e) == nil && e.Error == bdNoSuchTicket
}

// Status returns the ticket's status. If it cannot be read, it is StatusUnknown and the error says why.
func (b Tracker) Status(ctx context.Context, id string) (tracker.Status, error) {
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
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "update", id, "--append-notes", note)
	return err
}

// Defer sets the ticket aside, with the reason.
func (b Tracker) Defer(ctx context.Context, id, reason string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "defer", id, "--reason="+reason)
	return err
}

// Describe returns 'bd show' for the ticket, as a person reads it. For an ID bd has no ticket by, it
// says so rather than describe the ticket bd would show for it.
func (b Tracker) Describe(ctx context.Context, id string) string {
	if err := b.exists(ctx, id); err != nil {
		return fmt.Sprintf("(bd show %s failed: %v)", id, err)
	}
	out, err := b.bd(ctx, command.ReadLimit, "show", id)
	if err != nil {
		return fmt.Sprintf("(bd show %s failed: %v)", id, err)
	}
	return out
}

// Reopen puts the ticket back in the queue.
func (b Tracker) Reopen(ctx context.Context, id string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "update", id, "--status", "open")
	return err
}

// Undefer brings a deferred ticket back (bd undefer), in progress, as its worker's again.
func (b Tracker) Undefer(ctx context.Context, id string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	if _, err := b.bd(ctx, command.WriteLimit, "undefer", id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "update", id, "--status", "in_progress")
	return err
}

// Close closes the ticket, with the reason.
func (b Tracker) Close(ctx context.Context, id, reason string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "close", id, "--reason="+reason)
	return err
}

// AddLabel adds the label to the ticket.
func (b Tracker) AddLabel(ctx context.Context, id, label string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "label", "add", id, label)
	return err
}

// RemoveLabel removes the label from the ticket.
func (b Tracker) RemoveLabel(ctx context.Context, id, label string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "label", "remove", id, label)
	return err
}

// SetMetadata sets one key of the ticket's metadata, keeping the others.
func (b Tracker) SetMetadata(ctx context.Context, id, key, value string) error {
	if err := b.exists(ctx, id); err != nil {
		return err
	}
	_, err := b.bd(ctx, command.WriteLimit, "update", id, "--set-metadata", key+"="+value)
	return err
}

// NewTicket is a ticket to file with Create.
type NewTicket struct {
	Title, Description, Acceptance string
	Type                           string   // bd's issue type: epic, feature, task, bug or chore
	Priority                       int      // 0 (critical) to 4 (backlog)
	Parent                         string   // "" for none
	BlockedBy                      []string // the tickets it waits for
	Files                          []string
	Metadata                       map[string]string // more of its metadata, besides its files
	Labels                         []string
	// Checked says the caller has made sure bd has Parent and BlockedBy by those very IDs (it filed
	// them, or Show gave them): Create doesn't show them again.
	Checked bool
}

// FileBug files a P2 bug carrying label and returns its ID.
func (b Tracker) FileBug(ctx context.Context, title, description, label string) (string, error) {
	return b.Create(ctx, NewTicket{Title: title, Description: description, Type: "bug", Priority: 2,
		Labels: []string{label}})
}

// Create files the ticket and returns its ID. Files go in the metadata under tracker.FilesKey,
// which footprint scheduling reads, with the rest of its Metadata. When bd's output can't be read,
// the error carries bd's stderr. When bd has no ticket by the parent's ID or one it waits for, it
// files nothing: bd would link the ticket to one whose ID starts with it. A Checked ticket's links
// aren't looked up.
func (b Tracker) Create(ctx context.Context, t NewTicket) (string, error) {
	linked := t.BlockedBy
	if t.Parent != "" {
		linked = append([]string{t.Parent}, linked...)
	}
	if t.Checked {
		linked = nil
	}
	if err := b.exists(ctx, linked...); err != nil {
		return "", err
	}
	args := []string{"create", "--json", "--title=" + t.Title, "--description=" + t.Description,
		"--type=" + t.Type, "--priority=" + fmt.Sprint(t.Priority)}
	if t.Acceptance != "" {
		args = append(args, "--acceptance="+t.Acceptance)
	}
	if t.Parent != "" {
		args = append(args, "--parent="+t.Parent)
	}
	for _, id := range t.BlockedBy {
		args = append(args, "--deps=blocked-by:"+id)
	}
	if len(t.Labels) > 0 {
		args = append(args, "--labels="+strings.Join(t.Labels, ","))
	}
	if len(t.Files) > 0 || len(t.Metadata) > 0 {
		meta := map[string]any{}
		for k, v := range t.Metadata {
			meta[k] = v
		}
		if len(t.Files) > 0 {
			meta[tracker.FilesKey] = t.Files
		}
		b, err := json.Marshal(meta)
		if err != nil {
			return "", err
		}
		args = append(args, "--metadata="+string(b))
	}
	out, runErr := b.bd(ctx, command.WriteLimit, args...)
	if runErr != nil {
		return "", runErr
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(bdjson.Unwrap([]byte(out)), &created); err != nil || created.ID == "" {
		return "", fmt.Errorf("'bd create --json' gave no ID: %q", strings.TrimSpace(out))
	}
	return created.ID, nil
}
