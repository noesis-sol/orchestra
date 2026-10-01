package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// In-memory stand-ins for Beads and Herdr, so a whole run can be driven by a test. Git is real,
// in a temporary repository, or in memory (fakegit_test.go).

// ---- Beads ---------------------------------------------------------------------------

type fakeLink struct{ on, typ string }

// fakeBeads is a small tracker: tickets with a status, a priority, labels and links. Ready
// follows bd ready: open tickets whose blockers are closed, leaving out epics and questions.
type fakeBeads struct {
	mu      sync.Mutex
	tickets map[string]*Ticket
	links   map[string][]fakeLink
	order   []string
	notes   map[string][]string
}

func newFakeBeads() *fakeBeads {
	return &fakeBeads{tickets: map[string]*Ticket{}, links: map[string][]fakeLink{}, notes: map[string][]string{}}
}

// filedBefore is when the tickets a test adds were filed: before any run, on the real clock or a
// synctest bubble's, which starts at 2000-01-01.
const filedBefore = "1999-01-01T00:00:00Z"

func (b *fakeBeads) add(id, title string, prio int, labels ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addLocked(id, title, prio, labels...)
}

// addLocked adds a ticket. The caller holds mu: holding it across several makes them one change,
// which a read of bd ready sees all of or none of.
func (b *fakeBeads) addLocked(id, title string, prio int, labels ...string) {
	b.tickets[id] = &Ticket{ID: id, Title: title, Status: "open", IssueType: "task", Priority: &prio, Labels: labels, CreatedAt: filedBefore}
	b.order = append(b.order, id)
}

// sub adds a subticket of parent, as bd create --parent does: filed now, linked parent-child.
func (b *fakeBeads) sub(id, parent, title string, prio int, labels ...string) {
	b.add(id, title, prio, labels...)
	b.mu.Lock()
	b.tickets[id].Parent = parent
	b.tickets[id].CreatedAt = time.Now().UTC().Format(time.RFC3339)
	b.mu.Unlock()
	b.link(id, parent, "parent-child")
}

// under reports whether id descends from root. The caller holds mu.
func (b *fakeBeads) under(id, root string) bool {
	for p := b.tickets[id].Parent; p != ""; p = b.tickets[p].Parent {
		if p == root {
			return true
		}
	}
	return false
}

// link makes id depend on on.
func (b *fakeBeads) link(id, on, typ string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.links[id] = append(b.links[id], fakeLink{on, typ})
}

func (b *fakeBeads) set(id, status string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickets[id].Status = status
}

func (b *fakeBeads) notesOf(id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.notes[id], "\n")
}

func (b *fakeBeads) Ready(ctx context.Context, scope string) ([]Ticket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ready []Ticket
	for _, id := range b.order {
		t := b.tickets[id]
		if t.Status != "open" || t.IssueType == "epic" || HasLabel(*t, HumanLabel) || scope != "" && id != scope && !b.under(id, scope) {
			continue
		}
		blocked, blockers := false, 0
		for _, l := range b.links[id] {
			if l.typ == "blocks" {
				blocked = blocked || b.tickets[l.on].Status != "closed"
				blockers++
			}
		}
		if !blocked {
			r := *t
			r.DependencyCount = &blockers
			ready = append(ready, r)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool { return *ready[i].Priority < *ready[j].Priority })
	return ready, nil
}

func (b *fakeBeads) Unclosed(ctx context.Context) ([]Ticket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var open []Ticket
	for _, id := range b.order {
		if t := b.tickets[id]; t.Status != "closed" {
			open = append(open, *t)
		}
	}
	return open, nil
}

func (b *fakeBeads) Descendants(ctx context.Context, root string) ([]Ticket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var subs []Ticket
	for _, id := range b.order {
		if b.under(id, root) {
			subs = append(subs, *b.tickets[id])
		}
	}
	return subs, nil
}

func (b *fakeBeads) Show(ctx context.Context, id string) (Ticket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.tickets[id]
	if !ok {
		return Ticket{ID: id, Status: "unknown"}, fmt.Errorf("bd show %s: no such issue", id)
	}
	show := *t
	for _, l := range b.links[id] {
		d := *b.tickets[l.on]
		d.DependencyType = l.typ
		show.Dependencies = append(show.Dependencies, d)
	}
	return show, nil
}

func (b *fakeBeads) Status(ctx context.Context, id string) (string, error) {
	t, err := b.Show(ctx, id)
	return t.Status, err
}

func (b *fakeBeads) Describe(ctx context.Context, id string) string {
	t, _ := b.Show(ctx, id)
	return fmt.Sprintf("%s: %s [%s]", id, t.Title, t.Status)
}

func (b *fakeBeads) AppendNotes(ctx context.Context, id, note string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notes[id] = append(b.notes[id], note)
	return nil
}

func (b *fakeBeads) Defer(ctx context.Context, id, reason string) error {
	b.set(id, "deferred")
	return b.AppendNotes(ctx, id, "deferred: "+reason)
}

func (b *fakeBeads) Reopen(ctx context.Context, id string) error {
	b.set(id, "open")
	return nil
}

func (b *fakeBeads) Closed(ctx context.Context, label string) ([]Ticket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var closed []Ticket
	for _, id := range b.order {
		if t := b.tickets[id]; t.Status == "closed" && HasLabel(*t, label) {
			closed = append(closed, *t)
		}
	}
	return closed, nil
}

func (b *fakeBeads) AddLabel(ctx context.Context, id, label string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tickets[id]; ok && !HasLabel(*t, label) {
		t.Labels = append(t.Labels, label)
	}
	return nil
}

func (b *fakeBeads) RemoveLabel(ctx context.Context, id, label string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.tickets[id]; ok {
		var kept []string
		for _, l := range t.Labels {
			if l != label {
				kept = append(kept, l)
			}
		}
		t.Labels = kept
	}
	return nil
}

// SetMetadata sets one key of the ticket's metadata, as bd update --set-metadata does.
func (b *fakeBeads) SetMetadata(ctx context.Context, id, key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.tickets[id]
	if !ok {
		return fmt.Errorf("bd update %s: no such issue", id)
	}
	meta := map[string]any{}
	_ = json.Unmarshal(t.Metadata, &meta) // no metadata yet leaves meta empty
	meta[key] = value
	t.Metadata, _ = json.Marshal(meta)
	return nil
}

// metadata returns one key of the ticket's metadata, or "".
func (b *fakeBeads) metadata(id, key string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var meta map[string]string
	_ = json.Unmarshal(b.tickets[id].Metadata, &meta) // no metadata yet leaves meta empty
	return meta[key]
}

// ---- Workers -------------------------------------------------------------------------

// behaviour is what a worker does once it has its prompt; it returns the status its agent then
// settles in (idle, blocked, …). The agent is working until it returns.
type behaviour func(w *fakeWorker) AgentState

// fakeWorker is a worker on one ticket, in its worktree.
type fakeWorker struct {
	t     *testing.T
	id    string
	wt    string
	beads *fakeBeads
	git   *fakeGit               // git in memory, or nil for the real one
	shows func(state AgentState) // what Herdr shows it as from now on, while it goes on working
}

func (w *fakeWorker) claim()   { w.beads.set(w.id, "in_progress") }
func (w *fakeWorker) close()   { w.beads.set(w.id, "closed") }
func (w *fakeWorker) deferIt() { w.beads.set(w.id, "deferred") }

// commit adds file to the ticket's branch in a commit naming the ticket.
func (w *fakeWorker) commit(file string) {
	if w.git != nil {
		if err := w.git.commitIn(w.wt, w.id+": add "+file, file); err != nil {
			w.t.Error(err)
		}
		return
	}
	if err := os.WriteFile(filepath.Join(w.wt, file), []byte(w.id+"\n"), 0o644); err != nil {
		w.t.Error(err)
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-q", "-m", w.id + ": add " + file}} {
		if out, err := command.Output(context.Background(), 0, w.wt, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			w.t.Errorf("git %v in %s: %v\n%s", args, w.wt, err, out)
		}
	}
}

// ask files a question for the maintainer that blocks the ticket.
func (w *fakeWorker) ask(q, title string) {
	w.beads.add(q, title, 2, HumanLabel)
	w.beads.link(w.id, q, "blocks")
}

// finishes claims the ticket, commits file and closes it.
func finishes(file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		w.commit(file)
		w.close()
		return "idle"
	}
}

// ---- Reports -------------------------------------------------------------------------

// fakeReporter turns reporting on with arguments; workers report nothing.
type fakeReporter struct{}

func (fakeReporter) ReportArgs(worktree string) ([]string, error) {
	return []string{"--settings", filepath.Join(worktree, "hooks.json")}, nil
}
func (fakeReporter) LastToolUse(worktree string) (ToolUse, bool) { return ToolUse{}, false }
func (fakeReporter) EditedFiles(worktree string) []string        { return nil }

// ---- Notifications -------------------------------------------------------------------

// alerts records the notifications a Log shows.
type alerts struct {
	mu    sync.Mutex
	shown []string
}

// recordAlerts turns l's notifications on, recording them instead of showing them.
func recordAlerts(l *Log) *alerts {
	a := &alerts{}
	l.alert = func(text string) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.shown = append(a.shown, text)
	}
	return a
}

func (a *alerts) list() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.shown...)
}

func (a *alerts) has(text string) bool {
	for _, s := range a.list() {
		if s == text {
			return true
		}
	}
	return false
}

// ---- Sink ----------------------------------------------------------------------------

// runSink records events and closes held on the first HOLD.
type runSink struct {
	mu     sync.Mutex
	events []Event
	gone   []string
	held   chan struct{}
	once   sync.Once
}

func (s *runSink) Event(ev Event) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
	if ev.Kind == EvHold {
		s.once.Do(func() { close(s.held) })
	}
}

func (s *runSink) Status(st Status) {
	if st.Gone {
		s.mu.Lock()
		s.gone = append(s.gone, st.Ticket)
		s.mu.Unlock()
	}
}

// goneIDs returns the tickets whose workers have returned (their status removed), in order.
func (s *runSink) goneIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.gone...)
}

// of returns "<ticket> <text>" for each event of kind k, in order.
func (s *runSink) of(k Kind) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []string
	for _, ev := range s.events {
		if ev.Kind == k {
			l = append(l, strings.TrimSpace(ev.Ticket+" "+ev.Text))
		}
	}
	return l
}

func (s *runSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	for _, ev := range s.events {
		b.WriteString(ev.Text + "\n")
	}
	return b.String()
}
