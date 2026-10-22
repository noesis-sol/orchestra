package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A fast-forward of Base before a run's first ticket (the run's start, the overnight pre-flight, a spec
// run's fetch: see fetchBase and FetchBase) may bring in a settings.json and a worker prompt other than
// those the run read. No ticket has started yet, so every one is set up, checked and told by those of
// the Base it builds on: TakeFastForward takes them, whichever of the three fast-forwarded, so that
// each takes the same and says the same lines.

// FastForwardGit is what TakeFastForward asks of git: the files the fast-forward changed, and what
// they hold after it.
type FastForwardGit interface {
	ChangedFiles(ctx context.Context, repo, from, to string) ([]string, error) // the files that differ
	// the content of the file at path, from the repository's top, in commit rev; git's error when it has none
	FileAt(ctx context.Context, repo, rev, path string) (string, error)
}

// TakeFastForward has a run take into c and prompt what the fast-forward of c.Base by (the fast-forward
// to origin/main), from commit before to commit now, brought in before its first ticket, and returns
// the lines that say so, EvInfo and EvWarn events, their Time unset. From a settings.json it changed,
// it takes setup and setup_files, requires, and check_fast, check_full and check_own, each on its own:
// one it would refuse is warned of and stays as it was, the others taken all the same. A settings.json
// git can't read at now, or that doesn't parse, is warned of, and every setting stays as it was; one
// the fast-forward left as it was, as in a repository that has none, is taken of nothing. Of the worker
// prompt (c.PromptFile) it takes one the fast-forward changed, inside the repository, with its
// TICKET_ID placeholder; one it can't read, or without the placeholder, is warned of. When git can't
// say what the fast-forward changed, both are warned of and stay as they were. With before or now ""
// it takes nothing.
func TakeFastForward(ctx context.Context, g FastForwardGit, c *Config, prompt *string, by, before, now string) []Event {
	if before == "" || now == "" {
		return nil
	}
	t := fastForwardTake{c: c, by: by}
	changed, err := g.ChangedFiles(ctx, c.Repo, before, now)
	if err != nil {
		t.unknown(err)
		return t.lines
	}
	if slices.Contains(changed, project.SettingsFile) {
		text, err := g.FileAt(ctx, c.Repo, now, project.SettingsFile)
		var s project.Settings
		if err == nil {
			s, err = project.ParseSettings([]byte(text))
		}
		if err != nil {
			t.warn("  SETTINGS_UNREAD: %s changed %s on %s, but it could not be read%s; the setup, the "+
				"requirements and the check commands stay as they were", by, project.SettingsFile, c.Base, because(err))
		} else {
			t.setup(s)
			t.requires(s)
			t.checks(s)
		}
	}
	t.prompt(ctx, g, changed, prompt, now)
	return t.lines
}

// fastForwardTake is what TakeFastForward has taken so far, and the lines that say so.
type fastForwardTake struct {
	c     *Config
	by    string // what moved Base, for the lines
	lines []Event
}

func (t *fastForwardTake) info(format string, a ...any) {
	t.lines = append(t.lines, Event{Kind: EvInfo, Text: fmt.Sprintf(format, a...)})
}

func (t *fastForwardTake) warn(format string, a ...any) {
	t.lines = append(t.lines, Event{Kind: EvWarn, Text: fmt.Sprintf(format, a...)})
}

// unknown warns that git couldn't say what the fast-forward changed (err): the settings, and the worker
// prompt if it is in the repository, stay as they were.
func (t *fastForwardTake) unknown(err error) {
	c := t.c
	t.warn("  SETTINGS_UNREAD: git could not say whether %s changed %s on %s%s; the setup, the requirements and "+
		"the check commands stay as they were", t.by, project.SettingsFile, c.Base, because(err))
	if rel, ok := t.promptPath(); ok {
		t.warn("  PROMPT_UNREAD: git could not say whether %s changed %s on %s%s; the run's workers get the "+
			"worker prompt it read before", t.by, rel, c.Base, because(err))
	}
}

// setup takes setup and setup_files from s where they differ from the run's.
func (t *fastForwardTake) setup(s project.Settings) {
	c := t.c
	files, err := project.ResolveSetupFiles(s)
	if err != nil {
		t.warn("  SETTINGS_UNREAD: %s changed the setup in %s on %s, but %v; the setup stays %s",
			t.by, project.SettingsFile, c.Base, err, setupSaid(c.Setup))
		return
	}
	if s.Setup == c.Setup && slices.Equal(files, c.SetupFiles) {
		return
	}
	c.Setup, c.SetupFiles = s.Setup, files
	t.info("  SETUP: %s changed %s on %s: the setup is %s from now on",
		t.by, project.SettingsFile, c.Base, setupSaid(s.Setup))
}

// requires takes the requirements in s where they differ from the run's: the run checks them as it
// starts, before its first ticket.
func (t *fastForwardTake) requires(s project.Settings) {
	c := t.c
	list, err := project.ResolveRequires(s)
	if err != nil {
		t.warn("  SETTINGS_UNREAD: %s changed the requirements in %s on %s, but %v; they stay as they were",
			t.by, project.SettingsFile, c.Base, err)
		return
	}
	if slices.EqualFunc(list, c.Requires, project.Requirement.Equal) {
		return
	}
	c.Requires = list
	t.info("  REQUIRES: %s changed the requirements in %s on %s: they are checked before the next "+
		"ticket starts", t.by, project.SettingsFile, c.Base)
}

// checks takes check_fast, check_full and check_own from s where they differ from the run's.
func (t *fastForwardTake) checks(s project.Settings) {
	c := t.c
	var said []string
	for _, k := range []struct {
		name, none string
		now        *string
		to         string
	}{
		{"the merge check (check_fast)", "none", &c.Check, s.CheckFast},
		{"the full check (check_full)", "none", &c.CheckFull, s.CheckFull},
		{"a worker's own check (check_own)", "the merge check", &c.CheckOwn, s.CheckOwn},
	} {
		if *k.now == k.to {
			continue
		}
		*k.now = k.to
		cmd := k.none
		if k.to != "" {
			cmd = "'" + k.to + "'"
		}
		said = append(said, k.name+" is "+cmd)
	}
	if len(said) > 0 {
		t.info("  CHECKS: %s changed %s on %s: %s from now on", t.by, project.SettingsFile, c.Base,
			strings.Join(said, ", "))
	}
}

// promptPath is the worker prompt's path (Config.PromptFile) from the repository's top, slashed; false
// for none, or one outside the repository.
func (t *fastForwardTake) promptPath() (string, bool) {
	c := t.c
	rel, err := filepath.Rel(c.Repo, c.PromptFile)
	if c.PromptFile == "" || err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// prompt takes the worker prompt (Config.PromptFile) into prompt where the fast-forward to now changed
// it (changed): every worktree is made from the new commit. A prompt outside the repository, or one
// the fast-forward left as it was, stays the one the run was given; one git can't read at now, or
// without the TICKET_ID placeholder, too, with a warning.
func (t *fastForwardTake) prompt(ctx context.Context, g FastForwardGit, changed []string, prompt *string, now string) {
	c := t.c
	rel, ok := t.promptPath()
	if !ok || !slices.Contains(changed, rel) {
		return
	}
	text, err := g.FileAt(ctx, c.Repo, now, rel)
	if err == nil && !strings.Contains(text, "TICKET_ID") {
		err = errors.New("it has no TICKET_ID placeholder")
	}
	if err != nil {
		t.warn("  PROMPT_UNREAD: %s changed %s on %s, but it could not be read%s; the run's workers get the "+
			"worker prompt it read before", t.by, rel, c.Base, because(err))
		return
	}
	if text == *prompt {
		return
	}
	*prompt = text
	t.info("  PROMPT: %s changed %s on %s: the run's workers get it as committed", t.by, rel, c.Base)
}
