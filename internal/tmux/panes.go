package tmux

import (
	"context"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// paneInfo is a pane as tmux lists it, with the options orchestra gave it.
type paneInfo struct {
	id, window   string
	dead         bool   // its process exited (remain-on-exit keeps it open)
	launched     bool   // its process is the one orchestra launched (pidOption), not one another respawned
	agent, kind  string // the agent's name and kind; kind "" if no agent was launched there
	records      string // where its hooks write their records; "" if it was launched without them
	repo         string // the repository of the run that named its agent; "" if none, or an older orchestra did
	command, dir string // its foreground process's name, and that process's working directory
}

// paneFormat lists a pane for panes, its fields in the order panes reads them.
const paneFormat = "#{pane_id}\t#{window_id}\t#{pane_dead}\t#{==:#{pane_pid},#{" + pidOption + "}}\t#{" +
	agentOption + "}\t#{" + kindOption + "}\t#{" + recordsOption + "}\t#{" + repoOption + "}\t#{pane_current_command}\t" +
	"#{pane_current_path}"

// panes lists every pane tmux has, in every session: none when no tmux server runs.
func (t Terminal) panes(ctx context.Context) ([]paneInfo, error) {
	out, err := t.run(ctx, command.ReadLimit, "list-panes", "", "-a", "-F", paneFormat)
	if NoServer(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []paneInfo
	for line := range lines(out) {
		f := strings.SplitN(line, "\t", 10)
		if len(f) != 10 || !isID(f[0], '%') || !isID(f[1], '@') || f[2] != "0" && f[2] != "1" ||
			f[3] != "0" && f[3] != "1" {
			return nil, odd("list-panes", "", out)
		}
		ps = append(ps, paneInfo{id: f[0], window: f[1], dead: f[2] == "1", launched: f[3] == "1", agent: f[4],
			kind: f[5], records: f[6], repo: f[7], command: f[8], dir: f[9]})
	}
	return ps, nil
}

// find returns the first of ps that match is true of.
func find(ps []paneInfo, match func(paneInfo) bool) (paneInfo, bool) {
	if i := slices.IndexFunc(ps, match); i >= 0 {
		return ps[i], true
	}
	return paneInfo{}, false
}

// ours reports whether the pane's agent name is one of t's repository's: the pane was named for it,
// or by an older orchestra, which marked none, as a run it was stopped in left it.
func (t Terminal) ours(p paneInfo) bool { return p.repo == t.Repo || p.repo == "" }

// named returns the pane of ps whose agent is named name in t's repository, and false if none is: one
// named for the repository before one an orchestra that marked none named.
func (t Terminal) named(ps []paneInfo, name string) (paneInfo, bool) {
	if p, ok := find(ps, func(p paneInfo) bool { return p.kind != "" && p.agent == name && p.repo == t.Repo }); ok {
		return p, true
	}
	return find(ps, func(p paneInfo) bool { return p.kind != "" && p.agent == name && t.ours(p) })
}

// agentPane returns the pane of the agent named name, and false if no pane of t's repository holds an
// agent so named.
func (t Terminal) agentPane(ctx context.Context, name string) (paneInfo, bool, error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return paneInfo{}, false, err
	}
	p, ok := t.named(ps, name)
	return p, ok, nil
}

// setName names the agent in the pane name, for t's repository, taking the name from any other pane
// of the repository's that holds it, as Herdr's agent names are unique. Another repository's panes
// keep theirs.
func (t Terminal) setName(ctx context.Context, pane, name string) error {
	ps, err := t.panes(ctx)
	if err != nil {
		return err
	}
	var args []string
	for _, p := range ps {
		if p.agent == name && p.id != pane && t.ours(p) {
			args = append(args, "-p", "-u", "-t", p.id, agentOption, ";", "set-option")
		}
	}
	args = append(args, "-p", "-t", pane, agentOption, name)
	if t.Repo != "" {
		args = append(args, ";", "set-option", "-p", "-t", pane, repoOption, t.Repo)
	}
	_, err = t.run(ctx, command.ReadLimit, "set-option", pane, args...)
	return err
}
