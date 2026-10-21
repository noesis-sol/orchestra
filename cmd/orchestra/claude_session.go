package main

import (
	"slices"

	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// claudeSession is how orchestra starts one of its own Claude Code sessions (the spec's breakdown, its
// refinement, a feature's interview): in the main checkout, at an effort, with the project's chosen MCP
// servers and no others, and Chrome as workers get it. Built once from the run's options, it gives every
// session's command line, so an option added to them is added here.
type claudeSession struct {
	repo   string // the main checkout, where claude runs
	effort string // claude's --effort; "" for Claude Code's default
	// servers are the MCP servers the session gets, and no others: none but the project's chosen ones
	servers []mcp.Server
	chrome  []string // --chrome or --no-chrome, as workers get Chrome (dispatch.Config.SessionChrome)
	// tools are the flags that limit the session's tools and set their rules (answerToolArgs); none
	// for Claude Code's own.
	tools []string
}

// claudeSession is the run's way of starting its own Claude Code sessions, at effort ("" for Claude
// Code's default).
func (o options) claudeSession(effort string) claudeSession {
	return claudeSession{repo: o.Repo, effort: effort, servers: o.SessionMCP(), chrome: o.SessionChrome()}
}

// mcpArgs are the arguments that start the session with its MCP servers and no others (project.MCPArgs),
// and with Chrome as its chrome, --chrome or --no-chrome, has it, as workers do.
func (s claudeSession) mcpArgs() ([]string, error) {
	args, err := project.MCPArgs(s.repo, s.servers)
	if err != nil {
		return nil, err
	}
	return append(args, s.chrome...), nil
}

// args is an interactive session's command line, past claude: the instructions in prompt, its effort
// (none for "", Claude Code's default), its tools, its MCP servers and Chrome (mcpArgs), then extra.
func (s claudeSession) args(prompt string, extra ...string) ([]string, error) {
	mcpArgs, err := s.mcpArgs()
	if err != nil {
		return nil, err
	}
	var effort []string
	if s.effort != "" {
		effort = []string{"--effort", s.effort}
	}
	return slices.Concat([]string{"--append-system-prompt-file", prompt}, effort, s.tools, mcpArgs, extra), nil
}
