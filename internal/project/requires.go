package project

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Requirement is one entry of settings.json's "requires": something the project's machine must have
// (a tool, a service running, a permission), which no ticket installs. orchestra runs its Check at
// each run's start and in orchestra init; where it fails, it runs its Install, and checks again.
// One with Manual instead is left to a person, who is told it.
type Requirement struct {
	// Name names it in the lines that say it is missing, and its install's output file
	// (RequireLogName): letters, digits and . _ + -, such as "jq" or "postgres-16".
	Name string `json:"name"`
	// Check is a shell command, run in the main checkout, that exits 0 when the requirement is met,
	// such as "command -v jq".
	Check string `json:"check"`
	// Install is a shell command that meets it, such as "brew install jq": run without a terminal and
	// without sudo, so one that would need either is refused (see ResolveRequires).
	Install string `json:"install,omitempty"`
	// Manual is what a person must do instead, where orchestra can't: a GUI installer, a licence, a
	// permission to grant, a service to start.
	Manual string `json:"manual,omitempty"`
	// Platforms are the operating systems it applies on, among RequirePlatforms; none: all of them.
	Platforms []string `json:"platforms,omitempty"`
}

// RequirePlatforms are the operating systems a Requirement can be limited to, as Go names them.
var RequirePlatforms = []string{"darwin", "linux"}

// AppliesOn reports whether r applies on the operating system goos (runtime.GOOS).
func (r Requirement) AppliesOn(goos string) bool {
	return len(r.Platforms) == 0 || slices.Contains(r.Platforms, goos)
}

// RequireLogName is the file in the main checkout's .orchestra/run/ that holds the whole output of
// the requirement name's last install.
func RequireLogName(name string) string { return "requires-" + name + ".log" }

// requireName is what a Requirement's Name may be: it names a file.
var requireName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// asksPerson are the commands an install can't start with: each asks for a password on a terminal,
// and an install runs without one.
var asksPerson = []string{"sudo", "su", "doas", "pkexec"}

// ResolveRequires reads the project's requirements from its settings, refusing any that a run
// couldn't check or install: one without a name, or with a name another has, one without a check,
// one with neither an install nor a manual step or with both, an install that runs sudo (or another
// command that asks a person for a password) and a platform outside RequirePlatforms.
func ResolveRequires(s Settings) ([]Requirement, error) {
	seen := map[string]bool{}
	list := []Requirement{}
	for i, r := range s.Requires {
		if err := requirementErr(r, seen); err != nil {
			what := fmt.Sprintf("entry %d", i+1)
			if r.Name != "" {
				what = fmt.Sprintf("entry %d (%s)", i+1, r.Name)
			}
			return nil, settingErr("requires", fmt.Errorf("%s %w", what, err))
		}
		seen[r.Name] = true
		list = append(list, r)
	}
	return list, nil
}

// requirementErr says what is wrong with r, the names in seen taken already, after the words naming
// it; nil when nothing is.
func requirementErr(r Requirement, seen map[string]bool) error {
	switch {
	case !requireName.MatchString(r.Name):
		return fmt.Errorf("needs a name of letters, digits and . _ + -, such as \"jq\" (got %q)", r.Name)
	case seen[r.Name]:
		return fmt.Errorf("has the name of an entry before it")
	case strings.TrimSpace(r.Check) == "":
		return fmt.Errorf("needs a check: a command that exits 0 when it is met, such as \"command -v %s\"", r.Name)
	case strings.TrimSpace(r.Install) == "" && strings.TrimSpace(r.Manual) == "":
		return fmt.Errorf("needs an install (a command that meets it) or manual (what a person must do)")
	case r.Install != "" && r.Manual != "":
		return fmt.Errorf("has both an install and manual: give the one that meets it")
	}
	if cmd := personAsked(r.Install); cmd != "" {
		return fmt.Errorf("install runs %s, which asks for a password: an install runs without a terminal. "+
			"Give it as manual instead, or install without %s (got '%s')", cmd, cmd, r.Install)
	}
	for _, p := range r.Platforms {
		if !slices.Contains(RequirePlatforms, p) {
			return fmt.Errorf("has platforms that must be among %s (got '%s')", strings.Join(RequirePlatforms, ", "), p)
		}
	}
	return nil
}

// shellBreaks split a command line into the commands it runs one after another, or piped.
var shellBreaks = regexp.MustCompile(`&&|\|\||[;|&\n()]`)

// personAsked is the command among asksPerson that one of the commands in the command line install
// starts with, past any variables it sets and env, exec or command; "" for none.
func personAsked(install string) string {
	for _, part := range shellBreaks.Split(install, -1) {
		for word := range strings.FieldsSeq(part) {
			if strings.Contains(word, "=") || word == "env" || word == "exec" || word == "command" {
				continue
			}
			if slices.Contains(asksPerson, word) {
				return word
			}
			break
		}
	}
	return ""
}

// Equal reports whether r and other are the same requirement, field by field.
func (r Requirement) Equal(other Requirement) bool {
	return r.Name == other.Name && r.Check == other.Check && r.Install == other.Install &&
		r.Manual == other.Manual && slices.Equal(r.Platforms, other.Platforms)
}
