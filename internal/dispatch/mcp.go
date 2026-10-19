package dispatch

import (
	"strings"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// mcpUnchosen warns, once a run starts, that its workers get every MCP server on the machine.
const mcpUnchosen = "workers load every MCP server Claude Code finds on this machine; choose theirs with orchestra init"

// MCPWarning is the warning a run gives once about its workers' MCP servers, or "" for none: a
// project that hasn't chosen them leaves Claude workers every server Claude Code finds.
func (c Config) MCPWarning() string {
	if c.MCP == nil && c.ClaudeWorkers() {
		return mcpUnchosen
	}
	return ""
}

// mcpLabel names the MCP servers workers get, for the START line.
func (c Config) mcpLabel() string {
	switch {
	case !c.ClaudeWorkers():
		return "not passed to " + c.AgentKind + " workers"
	case c.MCP == nil:
		return "all, not configured"
	case len(*c.MCP) == 0:
		return "none"
	}
	names := make([]string, len(*c.MCP))
	for i, s := range *c.MCP {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// sayMCP logs, once a run starts, what its workers' MCP servers need said: that a project which
// hasn't chosen them leaves workers every server, or that the chosen ones aren't passed to agents
// other than Claude.
func (o *Loop) sayMCP() {
	c := o.cfg
	switch {
	case c.MCPWarning() != "":
		o.emit(Event{Kind: EvInfo, Text: "  MCP: " + c.MCPWarning()})
	case c.MCP != nil && !c.ClaudeWorkers():
		o.info("  MCP: the servers in mcp_servers aren't passed to %s workers, only to Claude ones", c.AgentKind)
	}
}

// mcpArgs are the arguments that start a Claude worker in worktree wt with the project's MCP
// servers and no others, whose definitions it writes to .orchestra/run/mcp.json there. There are
// none when the project hasn't chosen, or for another kind of agent.
func (o *Loop) mcpArgs(wt string) ([]string, error) {
	c := o.cfg
	if c.MCP == nil || !c.ClaudeWorkers() {
		return nil, nil
	}
	return project.MCPArgs(wt, *c.MCP)
}

// SessionMCP are the MCP servers orchestra's own Claude Code sessions get (the spec's breakdown, its
// refinement, a feature's interview), and no others: a Claude worker's, or none when the project
// hasn't chosen them, or chose them for workers of another kind, of which orchestra has only the
// names. project.MCPArgs gives them.
func (c Config) SessionMCP() []mcp.Server {
	if c.MCP == nil || !c.ClaudeWorkers() {
		return nil
	}
	return *c.MCP
}

// SessionChrome are the arguments that give orchestra's own Claude Code sessions Chrome as a Claude
// worker gets it (WorkerArgs, from mcp.ChromeArgs): --chrome when the project chose it, --no-chrome
// when its chosen servers leave it out, whatever kind its workers are, and none when it hasn't chosen.
// Claude Code turns Chrome on by itself, past --strict-mcp-config, once it is set up.
func (c Config) SessionChrome() []string {
	if c.MCP == nil {
		return nil
	}
	names := make([]string, len(*c.MCP))
	for i, s := range *c.MCP {
		names[i] = s.Name
	}
	return mcp.ChromeArgs(&names)
}
