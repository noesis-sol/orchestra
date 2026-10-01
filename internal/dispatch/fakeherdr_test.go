package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// ---- Herdr ---------------------------------------------------------------------------

type fakePane struct{ ticket, wt, tab string }

type fakeAgent struct {
	name, kind, pane, status string
	prompted                 bool
	late                     bool // Herdr doesn't see it yet: adoption gives up, and the next pane read misses it
}

var errRefused = errors.New("herdr: unknown argument")

// fakeHerdr is the terminal: tabs, the agents in their panes, and the workers they run. An agent
// runs the next behaviour given for its ticket once it has its prompt.
type fakeHerdr struct {
	t     *testing.T
	beads *fakeBeads

	mu         sync.Mutex
	tabs       int
	panes      map[string]fakePane
	agents     []*fakeAgent // gone ones are removed
	closed     []string     // tabs closed
	pasted     []string     // tickets whose prompt was pasted
	starts     []string     // tickets StartAgent was asked to start a worker for
	behaviours map[string][]behaviour
	running    sync.WaitGroup

	launchFails  map[string]bool        // LaunchInPane fails for these tickets
	promptFails  map[string]bool        // pasting the prompt never submits it
	startUnnamed map[string]bool        // the first StartAgent times out, leaving the agent unnamed in its pane
	launchSlow   map[string]bool        // the worker LaunchInPane starts appears only after the adoption gives up
	launchLost   map[string]bool        // LaunchInPane succeeds, but no worker ever appears
	showsAs      map[string]string      // the status these tickets' workers show from their prompt on, instead of working
	refuseArgs   bool                   // StartAgent takes no arguments
	agentName    func(id string) string // names a ticket's worker; nil keeps the ID
}

func newFakeHerdr(t *testing.T, beads *fakeBeads) *fakeHerdr {
	return &fakeHerdr{t: t, beads: beads, panes: map[string]fakePane{}, behaviours: map[string][]behaviour{},
		launchFails: map[string]bool{}, promptFails: map[string]bool{}, startUnnamed: map[string]bool{},
		launchSlow: map[string]bool{}, launchLost: map[string]bool{}, showsAs: map[string]string{}}
}

// agent returns the agent named name, or nil. The caller holds mu.
func (h *fakeHerdr) agent(name string) *fakeAgent {
	for _, a := range h.agents {
		if a.name != "" && a.name == name {
			return a
		}
	}
	return nil
}

// inPane returns the agent in pane, or nil. The caller holds mu.
func (h *fakeHerdr) inPane(pane string) *fakeAgent {
	for _, a := range h.agents {
		if a.pane == pane {
			return a
		}
	}
	return nil
}

// prompt starts the agent on its ticket's next behaviour. The caller holds mu.
func (h *fakeHerdr) prompt(a *fakeAgent) {
	p := h.panes[a.pane]
	a.prompted, a.status = true, "working"
	if st, ok := h.showsAs[p.ticket]; ok {
		a.status = st
	}
	queue := h.behaviours[p.ticket]
	if len(queue) == 0 {
		h.t.Errorf("no behaviour left for a worker on %s", p.ticket)
		a.status = "idle"
		return
	}
	b := queue[0]
	h.behaviours[p.ticket] = queue[1:]
	h.running.Add(1)
	go func() {
		defer h.running.Done()
		st := b(&fakeWorker{t: h.t, id: p.ticket, wt: p.wt, beads: h.beads})
		h.mu.Lock()
		a.status = st
		h.mu.Unlock()
	}()
}

func (h *fakeHerdr) tabsClosed() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.closed...)
}

func (h *fakeHerdr) startsFor() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.starts...)
}

func (h *fakeHerdr) pastedTo() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.pasted...)
}

// Tabs

func (h *fakeHerdr) CreateTab(workspace, cwd, label string) (string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tabs++
	tab, pane := fmt.Sprintf("tab%d", h.tabs), fmt.Sprintf("pane%d", h.tabs)
	h.panes[pane] = fakePane{ticket: label, wt: cwd, tab: tab}
	return tab, pane, nil
}

func (h *fakeHerdr) CloseTab(tab string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = append(h.closed, tab)
	var left []*fakeAgent
	for _, a := range h.agents {
		if h.panes[a.pane].tab != tab {
			left = append(left, a)
		}
	}
	h.agents = left
}

// Starter

func (h *fakeHerdr) LaunchInPane(pane, kind string, args []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.launchFails[h.panes[pane].ticket] {
		return errors.New("herdr pane run: failed")
	}
	if h.launchLost[h.panes[pane].ticket] {
		return nil
	}
	a := &fakeAgent{kind: kind, pane: pane, late: h.launchSlow[h.panes[pane].ticket]}
	h.agents = append(h.agents, a)
	h.prompt(a) // the prompt is its launch argument
	return nil
}

func (h *fakeHerdr) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.starts = append(h.starts, h.panes[pane].ticket)
	if err := errLongName(name); err != nil {
		return err
	}
	if h.refuseArgs && len(args) > 0 {
		return errRefused
	}
	if ticket := h.panes[pane].ticket; h.startUnnamed[ticket] {
		delete(h.startUnnamed, ticket)
		h.agents = append(h.agents, &fakeAgent{kind: kind, pane: pane, status: "idle"})
		return errors.New("herdr agent start: agent_not_ready")
	}
	if h.agent(name) != nil || h.inPane(pane) != nil {
		return fmt.Errorf("herdr agent start: %s or %s is taken", name, pane)
	}
	h.agents = append(h.agents, &fakeAgent{name: name, kind: kind, pane: pane, status: "idle"})
	return nil
}

func (h *fakeHerdr) IsArgumentRefused(err error) bool                { return errors.Is(err, errRefused) }
func (h *fakeHerdr) IsNameRefused(err error) bool                    { return false }
func (h *fakeHerdr) WaitReady(ctx context.Context, name string) bool { return true }

// Namer

// AgentName keeps the ticket ID unless agentName is set: most scenarios' IDs are names Herdr takes
// as they are.
func (h *fakeHerdr) AgentName(id string) string {
	if h.agentName != nil {
		return h.agentName(id)
	}
	return id
}

// errLongName is Herdr refusing a name over its 32-character limit.
func errLongName(name string) error {
	if len(name) > 32 {
		return fmt.Errorf(`herdr: {"error":{"code":"invalid_agent_name","message":"%s is longer than 32 characters"}}`, name)
	}
	return nil
}

func (h *fakeHerdr) AdoptAgent(ctx context.Context, pane, kind, name string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := errLongName(name); err != nil {
		return "", err
	}
	a := h.inPane(pane)
	if a == nil || a.late {
		return "", fmt.Errorf("no %s agent appeared in pane %s within a minute", kind, pane)
	}
	if a.name != "" || h.agent(name) != nil {
		return "", fmt.Errorf("herdr: no unnamed agent in %s to name %s", pane, name)
	}
	a.name = name
	return name, nil
}

func (h *fakeHerdr) PaneAgent(pane string) (string, string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.inPane(pane); a != nil && !a.late {
		return a.name, a.kind, a.status
	} else if a != nil {
		a.late = false // there next time
	}
	return "", "", "gone"
}

// RenameAgent renames the agent called name, or the one in the pane called name.
func (h *fakeHerdr) RenameAgent(name, to string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := errLongName(to); err != nil {
		return err
	}
	a := h.agent(name)
	if a == nil {
		a = h.inPane(name)
	}
	if a == nil || h.agent(to) != nil {
		return fmt.Errorf("herdr agent rename %s %s: refused", name, to)
	}
	a.name = to
	return nil
}

func (h *fakeHerdr) FreeName(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := 1; ; i++ {
		if name := fmt.Sprintf("%s-%d", id, i); h.agent(name) == nil {
			return name
		}
	}
}

// Agents

func (h *fakeHerdr) Status(name string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.agent(name); a != nil {
		return a.status, nil
	}
	return "gone", nil
}

func (h *fakeHerdr) Screen(name, status string) string { return "" }

func (h *fakeHerdr) Prompt(ctx context.Context, name, prompt string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agent(name)
	if a == nil {
		return fmt.Errorf("herdr agent prompt: no agent %s", name)
	}
	h.pasted = append(h.pasted, h.panes[a.pane].ticket)
	if h.promptFails[h.panes[a.pane].ticket] {
		return errors.New("herdr agent prompt: the agent did not start")
	}
	h.prompt(a)
	return nil
}

func (h *fakeHerdr) SendKeys(name string, keys ...string) error { return nil }

func (h *fakeHerdr) WaitStarted(ctx context.Context, name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agent(name)
	return a != nil && a.prompted
}
