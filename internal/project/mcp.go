package project

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/mcp"
)

// MCPConfigName is the file in a worktree's .orchestra/run/ holding the definitions of the MCP
// servers its worker gets.
const MCPConfigName = "mcp.json"

// ConfigRoots are the directories Claude Code may key the repository's local-scope MCP servers
// by: repo, then its main checkout when repo is a linked worktree. mcp.Discover takes them.
func ConfigRoots(ctx context.Context, repo string) []string {
	roots := []string{repo}
	common, err := git.Git{}.CommonDir(ctx, repo)
	if err == nil && filepath.Base(common) == ".git" {
		if main := filepath.Dir(common); !mcp.SamePath(main, repo) {
			roots = append(roots, main)
		}
	}
	return roots
}

// AvailableServers names the servers that can be given to workers.
func AvailableServers(servers []mcp.Server) []string {
	var names []string
	for _, s := range servers {
		if s.Available() {
			names = append(names, s.Name)
		}
	}
	return names
}

// Connectors names the claude.ai connectors among servers, without the "claude.ai " prefix.
func Connectors(servers []mcp.Server) []string {
	var names []string
	for _, s := range servers {
		if !s.Available() {
			names = append(names, strings.TrimPrefix(s.Name, "claude.ai "))
		}
	}
	return names
}

// MCPStep sums up the MCP servers workers get, for init's summary: each chosen server with where
// it is defined, a chosen one this machine doesn't define (saved anyway: a teammate may define
// it), and the claude.ai connectors workers can't have.
func MCPStep(c Choice) Step {
	kind, detail := StepDone, ""
	switch {
	case c.MCP == nil:
		kind, detail = StepCaution, "not chosen: workers load every MCP server Claude Code finds on this machine"
		if c.MCPUnasked {
			detail += " (not asked: no terminal; --mcp sets them)"
		}
	case len(*c.MCP) == 0:
		detail = "none: workers get no MCP servers"
		if len(AvailableServers(c.Servers)) == 0 {
			detail += " (Claude Code defines none for this project; claude mcp add defines one)"
		}
	default:
		var got, missing []string
		for _, name := range *c.MCP {
			switch s, ok := mcp.Find(c.Servers, name); {
			case !ok && name == mcp.Chrome:
				missing = append(missing, name+" isn't set up on this machine (set it up: claude --chrome)")
			case !ok:
				missing = append(missing, fmt.Sprintf("%s isn't defined on this machine (define it: claude mcp add %s …)",
					name, name))
			case !s.Available():
				missing = append(missing, name+" is a claude.ai connector, which workers can't get")
			case s.Scope == mcp.ScopeBuiltIn:
				got = append(got, name+" (built into Claude Code)")
			default:
				got = append(got, fmt.Sprintf("%s (%s, %s)", name, s.Scope, s.Type))
			}
		}
		detail = "workers get " + strings.Join(got, ", ")
		if len(got) == 0 {
			detail = "chosen: " + strings.Join(*c.MCP, ", ")
		}
		if len(missing) > 0 {
			kind = StepCaution
			detail += " · " + strings.Join(missing, "; ") + "; saved anyway, as a teammate may define it"
		}
	}
	if connectors := Connectors(c.Servers); len(connectors) > 0 {
		detail += " · not available to workers (claude.ai connectors): " + strings.Join(connectors, ", ")
	}
	if c.ServersErr != nil {
		kind = StepCaution
		detail += " · couldn't read Claude Code's MCP config: " + c.ServersErr.Error()
	}
	return Step{Kind: kind, Label: "MCP servers", Detail: detail}
}

// ResolveMCP finds the definitions of the MCP servers named in mcp_servers among servers, as
// mcp.Discover lists them on this machine, with Chrome as a built-in server: --chrome gives it. A
// name that isn't defined here, or names a claude.ai connector, is an error naming each such server
// and how to fix it: workers would otherwise start without a server their project chose for them.
func ResolveMCP(names []string, servers []mcp.Server) ([]mcp.Server, error) {
	chosen := []mcp.Server{}
	var missing, connectors []string
	for _, name := range names {
		switch s, ok := mcp.Find(servers, name); {
		case name == mcp.Chrome: // given with --chrome, whether or not Claude Code has it set up yet
			chosen = append(chosen, mcp.Server{Name: name, Scope: mcp.ScopeBuiltIn})
		case !ok:
			missing = append(missing, name)
		case !s.Available():
			connectors = append(connectors, name)
		default:
			chosen = append(chosen, s)
		}
	}
	var problems []string
	for _, name := range missing {
		problems = append(problems, fmt.Sprintf("%s isn't defined on this machine (define it: claude mcp add %s …)",
			name, name))
	}
	for _, name := range connectors {
		problems = append(problems, name+" is a claude.ai connector, which workers can't get")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf(
			"MCP servers for workers (mcp_servers in %s/%s): %s; or choose theirs again with: orchestra init",
			Dir, SettingsName, strings.Join(problems, "; "))
	}
	return chosen, nil
}

// WriteMCPConfig puts servers' definitions in the worktree at .orchestra/run/mcp.json, as Claude
// Code's --mcp-config takes them, and returns its path. The definitions may hold secrets, so the
// file is readable by its owner only, and EnsureRunExcluded keeps it out of git.
func WriteMCPConfig(wt string, servers []mcp.Server) (string, error) {
	defs := map[string]json.RawMessage{}
	for _, s := range servers {
		if s.Scope != mcp.ScopeBuiltIn { // Chrome: given with --chrome, not defined
			defs[s.Name] = s.Definition
		}
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": defs}, "", "  ")
	if err != nil {
		return "", err
	}
	root, err := OpenRun(wt)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(MCPConfigName)
	if err := WriteRun(root, wt, rel, b, 0o600); err != nil {
		return "", err
	}
	return filepath.Join(wt, rel), nil
}
