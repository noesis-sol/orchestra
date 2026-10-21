package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// People read the README to set a machine up, orchestra reads the requires list; written apart, they
// drift. So the README's section on what to install is the list as Markdown (RequiresMarkdown), between
// two markers: orchestra init writes it where the README has none and keeps it in step, 'orchestra
// requires --readme' writes it once the list changes, and a run's start says when it differs.
const (
	// ReadmeName is the README the section is kept in, at the repository's top.
	ReadmeName = "README.md"
	// RequiresStart and RequiresEnd are the lines that open and close the section, each on a line of
	// its own.
	RequiresStart = "<!-- orchestra:requires -->"
	RequiresEnd   = "<!-- /orchestra:requires -->"
)

// requiresStartLine and requiresEndLine find the markers on lines of their own.
var (
	requiresStartLine = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(RequiresStart) + `[ \t]*\r?$`)
	requiresEndLine   = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(RequiresEnd) + `[ \t]*\r?$`)
)

// platformNames are RequirePlatforms as people name them.
var platformNames = map[string]string{"darwin": "macOS", "linux": "Linux"}

// RequiresMarkdown is the README's section for the requirements in list, its markers included: a
// heading, then each requirement with how it is checked and how it is installed, or what a person
// must do; for an empty list, that there is nothing to install.
func RequiresMarkdown(list []Requirement) string {
	var b strings.Builder
	b.WriteString(RequiresStart + "\n")
	b.WriteString("<!-- Generated from \"requires\" in " + SettingsFile + ": change the list, then run " +
		"orchestra requires --readme. -->\n")
	b.WriteString("## What to install\n\n")
	if len(list) == 0 {
		b.WriteString("Nothing yet: the project's requires list, `\"requires\"` in `" + SettingsFile +
			"`, has no entries.\n")
		b.WriteString(RequiresEnd + "\n")
		return b.String()
	}
	b.WriteString("What this project needs of the machine, from `\"requires\"` in `" + SettingsFile + "`. " +
		"`orchestra requires` checks each one and installs what it can.\n\n")
	for _, r := range list {
		b.WriteString("- **" + r.Name + "**")
		if len(r.Platforms) > 0 {
			names := make([]string, len(r.Platforms))
			for i, p := range r.Platforms {
				names[i] = platformNames[p]
			}
			b.WriteString(" (only on " + strings.Join(names, ", ") + ")")
		}
		b.WriteString("\n" + mdItem("check", r.Check))
		if r.Install != "" {
			b.WriteString(mdItem("install", r.Install))
		} else {
			b.WriteString("  - by hand: " + strings.Join(strings.Fields(r.Manual), " ") + "\n")
		}
	}
	b.WriteString(RequiresEnd + "\n")
	return b.String()
}

// mdItem is a nested list item, label then the command cmd: a code span for one line, a fenced block
// under it for more.
func mdItem(label, cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if !strings.Contains(cmd, "\n") {
		fence := strings.Repeat("`", longestRun(cmd, '`')+1)
		pad := ""
		if strings.HasPrefix(cmd, "`") || strings.HasSuffix(cmd, "`") {
			pad = " "
		}
		return "  - " + label + ": " + fence + pad + cmd + pad + fence + "\n"
	}
	fence := strings.Repeat("`", max(3, longestRun(cmd, '`')+1))
	lines := strings.Split(cmd, "\n")
	for i, l := range lines {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			l = "    " + l
		}
		lines[i] = l
	}
	return "  - " + label + ":\n\n    " + fence + "sh\n" + strings.Join(lines, "\n") + "\n    " + fence + "\n\n"
}

// longestRun is the length of the longest run of c in s.
func longestRun(s string, c byte) int {
	longest, run := 0, 0
	for i := range len(s) {
		if s[i] != c {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}

// ErrReadmeMarkers is a README whose section's markers don't pair up: one without the other, or the
// end before the start. It is left as it is.
var ErrReadmeMarkers = errors.New(ReadmeName + " has " + RequiresStart + " and " + RequiresEnd +
	" out of pair: give each once, the start first, or remove both")

// readmeSection finds the section in text: start and end are its bounds, from its start marker's line
// to the end of its end marker's line; ok is false where text has neither marker.
func readmeSection(text string) (start, end int, ok bool, err error) {
	s := requiresStartLine.FindAllStringIndex(text, -1)
	e := requiresEndLine.FindAllStringIndex(text, -1)
	switch {
	case len(s) == 0 && len(e) == 0:
		return 0, 0, false, nil
	case len(s) != 1 || len(e) != 1 || e[0][0] < s[0][1]:
		return 0, 0, false, ErrReadmeMarkers
	}
	end = e[0][1]
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return s[0][0], end, true, nil
}

// withRequires is text, a README, with its section replaced by the one for list, or the section added
// at its end, after a blank line, where it has none; in the README's line endings.
func withRequires(text string, list []Requirement) (string, error) {
	section := RequiresMarkdown(list)
	if strings.Contains(text, "\r\n") {
		section = strings.ReplaceAll(section, "\n", "\r\n")
	}
	start, end, ok, err := readmeSection(text)
	switch {
	case err != nil:
		return "", err
	case ok:
		return text[:start] + section + text[end:], nil
	case text == "":
		return section, nil
	}
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	return strings.TrimRight(text, "\r\n") + nl + nl + section, nil
}

// ReadmeState is how a repository's README stands to its requires list.
type ReadmeState int

// The states a README can be in.
const (
	ReadmeInStep    ReadmeState = iota // its section is the list's
	ReadmeNone                         // there is no README.md
	ReadmeNoSection                    // it has no section
	ReadmeStale                        // its section differs from the list
)

// ReadmeRequires is how the README at repo's top stands to list. A README whose markers don't pair
// up is ErrReadmeMarkers.
func ReadmeRequires(repo string, list []Requirement) (ReadmeState, error) {
	b, err := os.ReadFile(filepath.Join(repo, ReadmeName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ReadmeNone, nil
	case err != nil:
		return ReadmeNone, err
	}
	text := string(b)
	start, end, ok, err := readmeSection(text)
	switch {
	case err != nil:
		return ReadmeNone, err
	case !ok:
		return ReadmeNoSection, nil
	case strings.ReplaceAll(text[start:end], "\r\n", "\n") != RequiresMarkdown(list):
		return ReadmeStale, nil
	}
	return ReadmeInStep, nil
}

// WriteReadmeRequires writes the section for list into the README at repo's top, replacing the one it
// has or adding one at its end, and creating README.md where there is none. It reports whether the
// README changed.
func WriteReadmeRequires(repo string, list []Requirement) (bool, error) {
	p := filepath.Join(repo, ReadmeName)
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(p); err == nil {
		perm = info.Mode().Perm()
	}
	text, err := withRequires(string(b), list)
	if err != nil || text == string(b) {
		return false, err
	}
	return true, writeFile(p, []byte(text), perm)
}

// ApplyReadme writes the section for the project's requires list into its README, as settings.json
// has the list once init has written it, and returns the step for init's summary: none where there is
// no README.md (init writes none), none where the list is empty and the README has no section, and
// none where the list is one a run would refuse, which init's check of the requirements reports.
func ApplyReadme(_ context.Context, repo string, _ Choice) ([]Step, error) {
	settings, _, err := LoadSettings(repo)
	if err != nil {
		return nil, nil // init's check of the requirements reports it
	}
	list, err := ResolveRequires(settings)
	if err != nil {
		return nil, nil // init's check of the requirements reports it
	}
	const label = "README"
	state, err := ReadmeRequires(repo, list)
	switch {
	case errors.Is(err, ErrReadmeMarkers):
		return []Step{{Kind: StepCaution, Label: label, Detail: ErrReadmeMarkers.Error() +
			"; its section on what to install is left as it is"}}, nil
	case err != nil:
		return nil, err
	case state == ReadmeNone, state == ReadmeNoSection && len(list) == 0:
		return nil, nil
	case state == ReadmeInStep:
		return []Step{{Kind: StepKept, Label: label,
			Detail: ReadmeName + "'s section on what to install is the requires list's"}}, nil
	}
	if _, err := WriteReadmeRequires(repo, list); err != nil {
		return nil, err
	}
	what := "wrote"
	if state == ReadmeStale {
		what = "updated"
	}
	return []Step{{Kind: StepDone, Label: label, Detail: what + " " + ReadmeName + "'s section on what to install " +
		"from the requires list, between " + RequiresStart + " and " + RequiresEnd, Commit: []string{ReadmeName}}}, nil
}

// ReadmeStaleLine says how the README at repo's top differs from list, for a run's start: its section
// differs, or it has none though the list has entries; "" where it is in step, there is no README.md,
// or there is nothing to say.
func ReadmeStaleLine(repo string, list []Requirement) string {
	state, err := ReadmeRequires(repo, list)
	fix := ": orchestra requires --readme writes it, to commit"
	switch {
	case errors.Is(err, ErrReadmeMarkers):
		return ErrReadmeMarkers.Error()
	case err != nil:
		return fmt.Sprintf("cannot read %s: %v", ReadmeName, err)
	case state == ReadmeStale:
		return ReadmeName + "'s section on what to install differs from the requires list in " + SettingsFile + fix
	case state == ReadmeNoSection && len(list) > 0:
		return ReadmeName + " has no section on what to install from the requires list in " + SettingsFile + fix
	}
	return ""
}
