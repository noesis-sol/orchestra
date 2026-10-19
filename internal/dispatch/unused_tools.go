package dispatch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
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
// its own (projectHasSkills), which a worker reaches only through Skill. "" help (unread, or workers
// of another kind) gives none, as an older claude would refuse a flag it doesn't know.
func (o *Loop) unusedToolArgs(help string) []string {
	if !organ.ListsFlag(help, "--disallowedTools") {
		return nil
	}
	tools, skills := unusedTools, "the project's skills kept"
	if !projectHasSkills(o.cfg.Repo) {
		tools, skills = append(slices.Clip(tools), "Skill"), "Skill and the skill listing, as the project has no skills"
	}
	o.info("  workers start without the tools they never use: %s (%s)", strings.Join(unusedTools, ", "), skills)
	// One argument: --disallowedTools takes every argument up to the next flag, which could be a prompt.
	return []string{"--disallowedTools=" + strings.Join(tools, ",")}
}

// projectHasSkills tells whether the project in repo has skills of its own for Claude Code: anything
// in its .claude/skills, or in .claude/commands, whose commands Claude Code lists and runs as skills.
func projectHasSkills(repo string) bool {
	for _, dir := range []string{project.SkillsDir("claude"), ".claude/commands"} {
		entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(dir)))
		if err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}
