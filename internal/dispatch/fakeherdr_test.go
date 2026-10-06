package dispatch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"
)

// ---- Herdr ---------------------------------------------------------------------------

type fakePane struct{ ticket, wt, tab string }

type fakeAgent struct {
	name, kind, pane string
	status           AgentState
	prompted         bool
	late             bool // Herdr doesn't see it yet: adoption gives up, and the next pane read misses it
}

var errRefused = errors.New("herdr: unknown argument")

// fakeHerdr is the terminal: tabs, the agents in their panes, and the workers they run. An agent
// runs the next behaviour given for its ticket once it has its prompt.
type fakeHerdr struct {
	t     *testing.T
	beads *fakeBeads
	git   *fakeGit // the repository workers commit to, if in memory

	mu         sync.Mutex
	tabs       int
	panes      map[string]fakePane
	agents     []*fakeAgent          // gone ones are removed
	closed     []string              // tabs closed
	labels     map[string]string     // the open tabs' labels, by tab
	pasted     []string              // tickets whose prompt was pasted
	texts      map[string][]string   // the prompts pasted, by ticket
	starts     []string              // tickets StartAgent was asked to start a worker for
	args       map[string][][]string // the arguments of each LaunchInPane and StartAgent, by ticket
	behaviours map[string][]behaviour
	running    sync.WaitGroup

	labelFails   bool                   // TabLabel fails, as when Herdr is busy
	launchFails  map[string]bool        // LaunchInPane fails for these tickets
	promptFails  map[string]bool        // pasting the prompt never submits it
	startUnnamed map[string]bool        // the first StartAgent times out, leaving the agent unnamed in its pane
	launchSlow   map[string]bool        // the worker LaunchInPane starts appears only after the adoption gives up
	onAdopt      func(id string)        // called with the ticket's ID as AdoptAgent begins, where a test presses Ctrl+C; nil: none
	launchLost   map[string]bool        // LaunchInPane succeeds, but no worker ever appears
	showsAs      map[string]AgentState  // the status these tickets' workers show from their prompt on, instead of working
	statusHangs  map[string]bool        // reading these tickets' workers' status, once they have their prompt, hangs until cancelled
	onPrompt     func(id string)        // told of each prompt pasted, with the ticket's ID; nil: none
	promptTakes  time.Duration          // how long Prompt takes to return once the worker has started, as a slow Herdr would
	refuseArgs   bool                   // StartAgent takes no arguments
	refusePaths  bool                   // StartAgent takes no arguments but plain flags, such as --no-chrome
	agentName    func(id string) string // names a ticket's worker; nil keeps the ID
}

func newFakeHerdr(t *testing.T, beads *fakeBeads) *fakeHerdr {
	return &fakeHerdr{t: t, beads: beads, panes: map[string]fakePane{}, labels: map[string]string{}, behaviours: map[string][]behaviour{}, args: map[string][][]string{},
		launchFails: map[string]bool{}, promptFails: map[string]bool{}, startUnnamed: map[string]bool{},
		launchSlow: map[string]bool{}, launchLost: map[string]bool{}, showsAs: map[string]AgentState{}, statusHangs: map[string]bool{}}
}

// agent returns the agent named name, or nil; one whose worker has gone holds no name. The caller
// holds mu.
func (h *fakeHerdr) agent(name string) *fakeAgent {
	for _, a := range h.agents {
		if a.name != "" && a.name == name && a.status != StateGone {
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
	h.running.Go(func() {
		shows := func(st AgentState) {
			h.mu.Lock()
			a.status = st
			h.mu.Unlock()
		}
		st := b(&fakeWorker{t: h.t, id: p.ticket, wt: p.wt, beads: h.beads, git: h.git, shows: shows})
		h.mu.Lock()
		a.status = st
		h.mu.Unlock()
	})
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

// argsFor is the arguments each worker for ticket id was started with, in order, each led by how:
// "launch" (LaunchInPane) or "start" (StartAgent).
func (h *fakeHerdr) argsFor(id string) [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]string(nil), h.args[id]...)
}

func (h *fakeHerdr) pastedTo() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.pasted...)
}

// pastedText returns the prompts pasted to ticket id's workers, in order.
func (h *fakeHerdr) pastedText(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.texts[id]...)
}

// Tabs

func (h *fakeHerdr) CreateTab(ctx context.Context, workspace, cwd, label string) (string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tabs++
	tab, pane := fmt.Sprintf("tab%d", h.tabs), fmt.Sprintf("pane%d", h.tabs)
	h.panes[pane] = fakePane{ticket: label, wt: cwd, tab: tab}
	h.labels[tab] = label
	return tab, pane, nil
}

func (h *fakeHerdr) CloseTab(ctx context.Context, tab string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = append(h.closed, tab)
	delete(h.labels, tab)
	var left []*fakeAgent
	for _, a := range h.agents {
		if h.panes[a.pane].tab != tab {
			left = append(left, a)
		}
	}
	h.agents = left
	return nil
}

func (h *fakeHerdr) TabLabel(ctx context.Context, tab string) (string, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.labelFails {
		return "", false, errors.New("herdr tab get: server busy")
	}
	label, open := h.labels[tab]
	return label, open, nil
}

// restart is Herdr starting again without restoring its last session: its tabs and agents are
// gone, and it numbers tabs from the first again.
func (h *fakeHerdr) restart() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tabs, h.agents, h.labels = 0, nil, map[string]string{}
}

// Starter

func (h *fakeHerdr) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.args[h.panes[pane].ticket] = append(h.args[h.panes[pane].ticket], append([]string{"launch"}, args...))
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
	h.args[h.panes[pane].ticket] = append(h.args[h.panes[pane].ticket], append([]string{"start"}, args...))
	if err := errLongName(name); err != nil {
		return err
	}
	if h.refuseArgs && len(args) > 0 ||
		h.refusePaths && slices.ContainsFunc(args, func(a string) bool { return !plainFlag.MatchString(a) }) {
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

// plainFlag is an argument Herdr passes through the shell as it is.
var plainFlag = regexp.MustCompile(`^--[a-z-]+$`)

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

// AdoptAgent names the agent in the pane at once, unless its context has ended: Herdr's own then
// fails before it reads the pane.
func (h *fakeHerdr) AdoptAgent(ctx context.Context, pane, kind, name string) (AgentState, error) {
	h.mu.Lock()
	onAdopt, ticket := h.onAdopt, h.panes[pane].ticket
	h.mu.Unlock()
	if onAdopt != nil {
		onAdopt(ticket)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
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
	return a.status, nil
}

func (h *fakeHerdr) PaneAgent(ctx context.Context, pane string) (string, string, AgentState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.inPane(pane); a != nil && !a.late {
		return a.name, a.kind, a.status, nil
	} else if a != nil {
		a.late = false // there next time
	}
	return "", "", StateGone, nil
}

// RenameAgent renames the agent called name, or the one in the pane called name.
func (h *fakeHerdr) RenameAgent(ctx context.Context, name, to string) error {
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

func (h *fakeHerdr) FreeName(ctx context.Context, id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := 1; ; i++ {
		if name := fmt.Sprintf("%s-%d", id, i); h.agent(name) == nil {
			return name
		}
	}
}

// Agents

func (h *fakeHerdr) Status(ctx context.Context, name string) (AgentState, error) {
	h.mu.Lock()
	if a := h.agent(name); a != nil && a.prompted && h.statusHangs[h.panes[a.pane].ticket] {
		h.mu.Unlock()
		<-ctx.Done() // as a Herdr that doesn't answer, stopped by the call's context
		return "", fmt.Errorf("herdr agent get %s: %w", name, context.Cause(ctx))
	}
	defer h.mu.Unlock()
	if a := h.agent(name); a != nil {
		return a.status, nil
	}
	return StateGone, nil
}

func (h *fakeHerdr) Screen(ctx context.Context, name string, state AgentState) string { return "" }

// Prompt starts the worker on its prompt and returns, as Herdr's does once it sees the worker
// working, after promptTakes.
func (h *fakeHerdr) Prompt(ctx context.Context, name, prompt string) error {
	if err := h.submit(name, prompt); err != nil {
		return err
	}
	if h.promptTakes > 0 && !sleep(ctx, h.promptTakes) {
		return ctx.Err()
	}
	return nil
}

// submit pastes prompt to the agent named name, and starts it on it unless the paste fails.
func (h *fakeHerdr) submit(name, prompt string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agent(name)
	if a == nil {
		return fmt.Errorf("herdr agent prompt: no agent %s", name)
	}
	h.pasted = append(h.pasted, h.panes[a.pane].ticket)
	if h.texts == nil {
		h.texts = map[string][]string{}
	}
	h.texts[h.panes[a.pane].ticket] = append(h.texts[h.panes[a.pane].ticket], prompt)
	if h.onPrompt != nil {
		h.onPrompt(h.panes[a.pane].ticket)
	}
	if h.promptFails[h.panes[a.pane].ticket] {
		return errors.New("herdr agent prompt: the agent did not start")
	}
	h.prompt(a)
	return nil
}

func (h *fakeHerdr) SendKeys(ctx context.Context, name string, keys ...string) error { return nil }

func (h *fakeHerdr) WaitStarted(ctx context.Context, name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agent(name)
	return a != nil && a.prompted
}
