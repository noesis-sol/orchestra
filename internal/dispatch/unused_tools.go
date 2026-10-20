package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/agentkind"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// unusedTools are the tools Claude Code describes in every request of a worker that orchestra's
// workers never call: no worker of 484 sessions did. Their descriptions, Artifact's above all, made up
// about a third of what each of a worker's calls opened with. A worker keeps the rest: Bash, Read,
// Edit and Write; Agent, for its verifier; AskUserQuestion, how orchestra sees it ask; ToolSearch, for
// its MCP servers' tools; and any tool a later Claude Code adds.
var unusedTools = []string{"Artifact", "ListAgents", "ReportFindings", "ScheduleWakeup", "SendFeedback", "Workflow"}

// unusedToolArgs is the --disallowedTools that keeps unusedTools out of Claude workers' requests,
// when help, their claude's --help, lists it: a denied tool isn't described to the model. It denies
// Skill too, which takes the listing of the user's skills with it, unless the project has skills of
// its own or enables plugins (projectHasSkills), whose skills a worker reaches only through Skill.
// "" help (unread, or workers of another kind) gives none, as an older claude would refuse a flag it
// doesn't know.
func (o *Loop) unusedToolArgs(help string) []string {
	if !organ.ListsFlag(help, "--disallowedTools") {
		return nil
	}
	tools, skills := unusedTools, "the project's skills kept"
	if !projectHasSkills(o.cfg.Repo) {
		tools = append(slices.Clip(tools), "Skill")
		skills = "Skill and the skill listing, as the project has no skills or plugins"
	}
	o.info("  workers start without the tools they never use: %s (%s)", strings.Join(unusedTools, ", "), skills)
	// One argument: --disallowedTools takes every argument up to the next flag, which could be a prompt.
	return []string{"--disallowedTools=" + strings.Join(tools, ",")}
}

// projectHasSkills tells whether the project in repo has skills for Claude Code: anything in its
// .claude/skills, or in .claude/commands, whose commands Claude Code lists and runs as skills, or a
// plugin its committed Claude settings enable (projectEnablesPlugins). It is read once, as the run begins.
func projectHasSkills(repo string) bool {
	for _, dir := range []string{agentkind.Claude.SkillsDir, ".claude/commands"} {
		entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(dir)))
		if err == nil && len(entries) > 0 {
			return true
		}
	}
	return projectEnablesPlugins(repo)
}

// projectEnablesPlugins tells whether the project's committed Claude settings, .claude/settings.json,
// enable a plugin: an entry of enabledPlugins not set to false. Each counts as having skills, as
// finding a plugin's own would mean finding its marketplace; settings.local.json isn't committed, so
// a worker's worktree doesn't have it. Settings missing or unreadable enable none.
func projectEnablesPlugins(repo string) bool {
	data, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	var settings struct {
		EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return false
	}
	for _, v := range settings.EnabledPlugins {
		if string(v) != "false" {
			return true
		}
	}
	return false
}
