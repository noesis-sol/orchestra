package project

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/mcp"
)

// ConfigRoots are the directories Claude Code may key the repository's local-scope MCP servers
// by: repo, then its main checkout when repo is a linked worktree. mcp.Discover takes them.
func ConfigRoots(ctx context.Context, repo string) []string {
	roots := []string{repo}
	common, err := command.Output(ctx, command.ReadLimit, repo,
		"git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if common = strings.TrimSpace(common); err == nil && filepath.Base(common) == ".git" {
		if main := filepath.Dir(common); !sameDir(main, repo) {
			roots = append(roots, main)
		}
	}
	return roots
}

// sameDir reports whether a and b are the same directory, following symbolic links.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return filepath.Clean(a) == filepath.Clean(b) || errA == nil && errB == nil && ra == rb
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
			case !ok:
				missing = append(missing, fmt.Sprintf("%s isn't defined on this machine (define it: claude mcp add %s …)",
					name, name))
			case !s.Available():
				missing = append(missing, name+" is a claude.ai connector, which workers can't get")
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
