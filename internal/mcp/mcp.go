// Package mcp finds the MCP servers Claude Code knows for a repository, from its config files, so
// a project can choose which of them its workers get. settings.json keeps only the servers' names;
// their definitions, which may hold secrets, stay in Claude Code's files and are read on each
// machine.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The scopes a server can come from, as claude mcp list names them.
const (
	ScopeLocal    = "local"     // ~/.claude.json, projects[<repo>].mcpServers: this machine, this repository
	ScopeProject  = "project"   // <repo>/.mcp.json: committed, shared with the team
	ScopeUser     = "user"      // ~/.claude.json, mcpServers: this machine, every repository
	ScopeClaudeAI = "claude.ai" // a claude.ai connector: no definition orchestra can pass on
	ScopeBuiltIn  = "built-in"  // part of Claude Code, turned on and off with a flag: Chrome
)

// Chrome is Claude Code's Claude in Chrome integration, which drives the browser on this machine.
// It isn't defined in any config file: --chrome gives it to a worker and --no-chrome keeps it out.
const Chrome = "claude-in-chrome"

// Server is one MCP server Claude Code knows for the repository.
type Server struct {
	Name  string
	Scope string // ScopeLocal, ScopeProject, ScopeUser, ScopeClaudeAI or ScopeBuiltIn
	Type  string // stdio, http or sse; empty for a claude.ai connector or a built-in server
	// Definition is the server's entry in its config file, as Claude Code's --mcp-config takes it;
	// nil for a claude.ai connector or a built-in server.
	Definition json.RawMessage
}

// Available reports whether the server can be given to workers: claude.ai connectors can't.
func (s Server) Available() bool { return s.Scope != ScopeClaudeAI }

// ChromeArgs are the arguments that give Claude workers Chrome when names, the servers the project
// chose, include it, and keep it out when they don't: Claude Code turns Chrome on by itself, past
// --strict-mcp-config, once it is set up. None when nothing was chosen (nil): workers then get
// whatever Claude Code finds, Chrome included.
func ChromeArgs(names *[]string) []string {
	switch {
	case names == nil:
		return nil
	case slices.Contains(*names, Chrome):
		return []string{"--chrome"}
	default:
		return []string{"--no-chrome"}
	}
}

// connectorPrefix starts a claude.ai connector's name in claude mcp list and ~/.claude.json.
const connectorPrefix = "claude.ai "

// UserConfig is where Claude Code keeps its user config (~/.claude.json, or .claude.json in
// CLAUDE_CONFIG_DIR), or "" when neither is known.
func UserConfig(getenv func(string) string) string {
	if dir := getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".claude.json")
	}
	return ""
}

// userFile is the part of ~/.claude.json that names MCP servers. The keys besides the mcpServers
// maps are Claude Code's own records, undocumented, and may change shape with it: they are read
// leniently, so one orchestra no longer understands is taken as not set rather than failing the
// file.
type userFile struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Projects   map[string]struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	} `json:"projects"`
	// ClaudeAIConnectors are the claude.ai connectors this machine's Claude Code has connected to.
	ClaudeAIConnectors lenient[[]string] `json:"claudeAiMcpEverConnected"`
	// Claude Code records that Claude in Chrome is set up on this machine in any of these.
	ChromeOn        lenient[bool] `json:"claudeInChromeDefaultEnabled"`
	ChromeInstalled lenient[bool] `json:"cachedChromeExtensionInstalled"`
	ChromeOnboarded lenient[bool] `json:"hasCompletedClaudeInChromeOnboarding"`
}

// lenient is a value of type T read from a file orchestra doesn't own: a value of another shape is
// taken as not set, T's zero value.
type lenient[T any] struct{ v T }

// UnmarshalJSON reads b into l, or takes l as not set when b isn't a T. The decoder calling it has
// already found the whole file to be JSON, so only the shape can be wrong here.
func (l *lenient[T]) UnmarshalJSON(b []byte) error {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		if typeErr := (*json.UnmarshalTypeError)(nil); errors.As(err, &typeErr) {
			*l = lenient[T]{}
			return nil
		}
		return err
	}
	l.v = v
	return nil
}

// Discover lists the MCP servers Claude Code knows for the repository checked out at roots[0], by
// name: user and local scope from userConfig (local scope under any of roots, such as the main
// checkout of a worktree), project scope from roots[0]/.mcp.json, the claude.ai connectors,
// which aren't Available, and Chrome when Claude Code has it set up. A name in more than one scope
// is listed once, from the scope Claude Code uses: local, then project, then user. Missing files
// name no servers.
func Discover(userConfig string, roots ...string) ([]Server, error) {
	byName := map[string]Server{}
	add := func(scope string, defs map[string]json.RawMessage) {
		for name, def := range defs {
			if _, ok := byName[name]; !ok {
				byName[name] = Server{Name: name, Scope: scope, Type: serverType(def), Definition: def}
			}
		}
	}
	var user userFile
	if userConfig != "" {
		if err := readJSON(userConfig, &user); err != nil {
			return nil, err
		}
	}
	for key, p := range user.Projects {
		for _, root := range roots {
			if SamePath(key, root) {
				add(ScopeLocal, p.MCPServers)
				break
			}
		}
	}
	if len(roots) > 0 {
		var shared struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := readJSON(filepath.Join(roots[0], ".mcp.json"), &shared); err != nil {
			return nil, err
		}
		add(ScopeProject, shared.MCPServers)
	}
	add(ScopeUser, user.MCPServers)
	for _, c := range user.ClaudeAIConnectors.v {
		if _, ok := byName[c]; !ok && strings.HasPrefix(c, connectorPrefix) {
			byName[c] = Server{Name: c, Scope: ScopeClaudeAI}
		}
	}
	if _, ok := byName[Chrome]; !ok && (user.ChromeOn.v || user.ChromeInstalled.v || user.ChromeOnboarded.v) {
		byName[Chrome] = Server{Name: Chrome, Scope: ScopeBuiltIn}
	}
	servers := make([]Server, 0, len(byName))
	for _, s := range byName {
		servers = append(servers, s)
	}
	sort.Slice(servers, func(i, j int) bool {
		if a, b := servers[i].Available(), servers[j].Available(); a != b {
			return a
		}
		return servers[i].Name < servers[j].Name
	})
	return servers, nil
}

// Find returns the server named name, if servers has it.
func Find(servers []Server, name string) (Server, bool) {
	for _, s := range servers {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// ParseNames reads a comma-separated list of server names, as --mcp takes it: "" is none.
// Duplicates and empty entries are dropped; the result is never nil.
func ParseNames(list string) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			names, seen[n] = append(names, n), true
		}
	}
	return names
}

// serverType is a definition's transport: its "type", else stdio, as Claude Code assumes.
func serverType(def json.RawMessage) string {
	var d struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(def, &d) != nil || d.Type == "" {
		return "stdio"
	}
	return d.Type
}

// readJSON reads the JSON file at path into v; a missing file leaves v as it is.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// SamePath reports whether a and b are the same directory, following symbolic links.
func SamePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
