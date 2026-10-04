package dispatch

import (
	"fmt"
	"strings"

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
// servers and no others, whose definitions it writes to .orchestra/run/mcp.json there: they never
// go on the command line, which shows in the worker's tab and in ps. There are none when the
// project hasn't chosen, or for another kind of agent.
func (o *Loop) mcpArgs(wt string) ([]string, error) {
	c := o.cfg
	switch {
	case c.MCP == nil || !c.ClaudeWorkers():
		return nil, nil
	case len(*c.MCP) == 0:
		return []string{"--strict-mcp-config"}, nil
	}
	path, err := project.WriteMCPConfig(wt, *c.MCP)
	if err != nil {
		return nil, fmt.Errorf("cannot write its MCP servers to %s/%s/%s: %w", project.Dir, project.RunName,
			project.MCPConfigName, err)
	}
	// --mcp-config takes any number of files, up to the next flag: --strict-mcp-config ends the
	// list, so the arguments after these, such as the prompt, aren't read as more files.
	return []string{"--mcp-config", path, "--strict-mcp-config"}, nil
}
