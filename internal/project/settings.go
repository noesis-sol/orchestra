package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Settings is .orchestra/settings.json: how 'orchestra init' set the project up. It is committed,
// so everyone running orchestra on the project shares it.
type Settings struct {
	// CheckFast is the project's merge check, normally FastRunner (lint, build, the fast suites).
	// orchestra runs it again on a finished ticket that had to be rebased onto work merged while it ran.
	CheckFast string `json:"check_fast,omitempty"`
	// CheckFull is every check, normally FullRunner: CheckFast, then the slower suites. orchestra runs
	// it once at the end of a run.
	CheckFull string `json:"check_full,omitempty"`
	// Check and CheckTimeout are the names of CheckFast and CheckFastTimeout before there were two
	// checks. LoadSettings reads them as those, and SaveSettings writes them under the new names.
	Check        string `json:"check,omitempty"`
	CheckTimeout string `json:"check_timeout,omitempty"`
	// Concurrency is how many tickets run at the same time unless --concurrent says otherwise.
	Concurrency int `json:"concurrent"`
	// TicketLimit is how long a ticket's worker may go on, from dispatch, before the run stops for
	// it, as a duration such as "2h", unless --ticket-limit says otherwise. Empty or "0": no limit.
	TicketLimit string `json:"ticket_limit,omitempty"`
	// CheckFastTimeout is how long CheckFast may run on a rebased ticket before orchestra stops it
	// and sets the ticket aside, as a duration such as "5m", unless --check-timeout says otherwise.
	// Empty: DefaultCheckTimeout.
	CheckFastTimeout string `json:"check_fast_timeout,omitempty"`
	// CheckFullTimeout is how long CheckFull may run, unless --check-full-timeout says otherwise.
	// Empty: DefaultCheckFullTimeout.
	CheckFullTimeout string `json:"check_full_timeout,omitempty"`
	// Setup installs the project's dependencies in a worktree, such as "npm ci". orchestra runs it before
	// CheckFast on a finished ticket whose rebase changed one of SetupFiles, so the check doesn't run
	// against dependencies installed for the code before the rebase. Empty: never run.
	Setup string `json:"setup,omitempty"`
	// SetupFiles are the files whose change in a rebase has Setup run: globs matched against a file's
	// name, or its path from the repository's top when the glob has a slash. Absent:
	// DefaultSetupFiles.
	SetupFiles *[]string `json:"setup_files,omitempty"`
	// ExcludeTypes are the issue types never taken from bd ready, such as epics, whose children
	// are the work. Absent: DefaultExcludeTypes; [] takes every type.
	ExcludeTypes *[]string `json:"exclude_types,omitempty"`
	// Footprint false starts tickets side by side even when they name the same files or functions.
	// Absent or true: a ticket whose footprint overlaps a running ticket's waits for a later slot.
	Footprint *bool `json:"footprint,omitempty"`
	// ResolveConflicts false sets a finished ticket whose branch conflicts with work merged while it
	// ran aside for review at once. Absent or true: its worker is asked to resolve the rebase first,
	// when there is a check command to check the resolution with.
	ResolveConflicts *bool `json:"resolve_conflicts,omitempty"`
	// ResolveTimeout is how long the worker may take to resolve it, as a duration such as "20m".
	// Empty: DefaultResolveTimeout.
	ResolveTimeout string `json:"resolve_timeout,omitempty"`
	// CheckHandBacks is how many times a finished ticket whose check fails on its branch rebased onto
	// work merged while it ran is handed back to its worker to fix, before it is set aside for review.
	// Absent: DefaultCheckHandBacks; 0 sets it aside at once.
	CheckHandBacks *int `json:"check_hand_backs,omitempty"`
	// EnvironmentHold is when the run holds because its workers keep failing at once, whichever
	// ticket they have. Absent: DefaultEnvironmentHold.
	EnvironmentHold *EnvironmentHold `json:"environment_hold,omitempty"`
	// MCPServers names the MCP servers workers get, defined in each machine's Claude Code config
	// (never their definitions, which may hold secrets). Absent: not chosen, so workers get every
	// server Claude Code finds; [] gives them none.
	MCPServers *[]string `json:"mcp_servers,omitempty"`
	// WorkerEffort is the effort Claude workers are started at (claude --effort), unless
	// --worker-effort says otherwise. Empty: Claude Code's own default.
	WorkerEffort string `json:"worker_effort,omitempty"`
	// OrganEffort is the effort of every organ, unless --organ-effort says otherwise. Empty: each
	// organ's own, low for triage and the predictor and medium for the run report.
	OrganEffort string `json:"organ_effort,omitempty"`
}

// EnvironmentHold is settings.json's "environment_hold": Count tickets in a row whose workers
// settled within Window of dispatch without claiming the ticket or changing anything, or that
// triage blamed on the environment with high confidence, hold the run. Probe after the hold, once
// nothing runs, one worker without a ticket runs a command, and the run takes tickets again if it
// does.
type EnvironmentHold struct {
	Count  *int   `json:"count,omitempty"`  // absent: DefaultEnvironmentHoldCount; 0 turns the hold off
	Window string `json:"window,omitempty"` // a duration such as "2m"; empty: DefaultEnvironmentHoldWindow
	Probe  string `json:"probe,omitempty"`  // a duration such as "10m"; empty: DefaultEnvironmentProbe; "0": no probe
}

const (
	// SettingsName is the settings file's name in Dir.
	SettingsName = "settings.json"
	// MaxConcurrency is the most workers a run may have at once.
	MaxConcurrency = 16
	// DefaultCheckTimeout is how long the fast check may run when settings.json sets no limit.
	DefaultCheckTimeout = 30 * time.Minute
	// DefaultCheckTimeoutText is DefaultCheckTimeout as settings.json writes it.
	DefaultCheckTimeoutText = "30m"
	// DefaultCheckFullTimeout is how long the full check may run when settings.json sets no limit.
	DefaultCheckFullTimeout = 60 * time.Minute
	// DefaultCheckFullTimeoutText is DefaultCheckFullTimeout as settings.json writes it.
	DefaultCheckFullTimeoutText = "60m"
	// DefaultResolveTimeout is how long a worker may take to resolve its rebase's conflicts when
	// settings.json sets no limit.
	DefaultResolveTimeout = 20 * time.Minute
	// DefaultCheckHandBacks is how many times a failed check on a rebased ticket is handed back to its
	// worker when settings.json doesn't say.
	DefaultCheckHandBacks = 2
)

// The environment hold when settings.json doesn't set it: two tickets in a row, workers failing
// within two minutes of dispatch, and a probe ten minutes after the running tickets finish.
const (
	DefaultEnvironmentHoldCount  = 2
	DefaultEnvironmentHoldWindow = 2 * time.Minute
	DefaultEnvironmentProbe      = 10 * time.Minute
)

// DefaultExcludeTypes are the issue types kept out of a run when settings.json names none.
var DefaultExcludeTypes = []string{"epic"}

// DefaultSetupFiles are the dependency manifests and lockfiles whose change in a rebase has the setup
// command run, when settings.json names none.
var DefaultSetupFiles = []string{
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb",
	"go.mod", "go.sum",
	"Cargo.toml", "Cargo.lock",
	"pyproject.toml", "poetry.lock", "uv.lock", "Pipfile", "Pipfile.lock", "requirements*.txt",
	"Gemfile", "Gemfile.lock", "composer.json", "composer.lock", "mix.exs", "mix.lock",
	"pubspec.yaml", "pubspec.lock",
}

// SettingsPath is where the project's settings.json is.
func SettingsPath(repo string) string { return filepath.Join(repo, Dir, SettingsName) }

// LoadSettings reads the project's settings; ok is false if there are none. The old check and
// check_timeout are read as check_fast and check_fast_timeout, where those aren't set.
func LoadSettings(repo string) (s Settings, ok bool, err error) {
	b, err := os.ReadFile(SettingsPath(repo))
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, false, fmt.Errorf("%s: %w", SettingsPath(repo), err)
	}
	if s.CheckFast == "" {
		s.CheckFast = s.Check
	}
	if s.CheckFastTimeout == "" {
		s.CheckFastTimeout = s.CheckTimeout
	}
	return s, true, nil
}

// SaveSettings writes the project's settings.json, keeping any keys Settings doesn't know, and
// writing the old check and check_timeout under their new names.
func SaveSettings(repo string, s Settings) error {
	if s.CheckFast == "" {
		s.CheckFast = s.Check
	}
	if s.CheckFastTimeout == "" {
		s.CheckFastTimeout = s.CheckTimeout
	}
	s.Check, s.CheckTimeout = "", ""
	merged := map[string]json.RawMessage{}
	b, err := os.ReadFile(SettingsPath(repo))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(b, &merged); err != nil {
			return fmt.Errorf("%s: %w", SettingsPath(repo), err)
		}
	}
	for _, k := range settingsKeys() {
		delete(merged, k) // an empty omitempty field clears the key
	}
	b, err = json.Marshal(s)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &merged); err != nil {
		return err
	}
	if b, err = json.MarshalIndent(merged, "", "  "); err != nil {
		return err
	}
	return writeFile(SettingsPath(repo), append(b, '\n'), 0o644)
}

// settingsKeys are the JSON keys of Settings' fields.
func settingsKeys() []string {
	t := reflect.TypeFor[Settings]()
	var keys []string
	for field := range t.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		keys = append(keys, name)
	}
	return keys
}

// ResolveConcurrency picks a run's concurrency: --concurrent (or ORCHESTRA_CONCURRENT), else the
// project's setting, else 1.
func ResolveConcurrency(flagValue int, s Settings) (int, error) {
	n := flagValue
	if n == 0 {
		n = s.Concurrency
	}
	if n == 0 {
		n = 1
	}
	if n < 1 || n > MaxConcurrency {
		return 0, fmt.Errorf("--concurrent must be between 1 and %d (got %d)", MaxConcurrency, n)
	}
	return n, nil
}

// ResolveTicketLimit picks a run's ticket limit: --ticket-limit (or TICKET_LIMIT) when given, else
// the project's setting, else none (0).
func ResolveTicketLimit(flagValue time.Duration, given bool, s Settings) (time.Duration, error) {
	if given {
		if flagValue < 0 {
			return 0, fmt.Errorf("--ticket-limit must not be negative (got %s)", flagValue)
		}
		return flagValue, nil
	}
	if s.TicketLimit == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s.TicketLimit)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s: ticket_limit must be a duration such as 2h, or 0 for none (got '%s')",
			SettingsPath("."), s.TicketLimit)
	}
	return d, nil
}

// ResolveCheckTimeout picks how long a run's fast check may run: --check-timeout (or
// ORCHESTRA_CHECK_TIMEOUT) when given, else the project's setting, else DefaultCheckTimeout.
func ResolveCheckTimeout(flagValue time.Duration, given bool, s Settings) (time.Duration, error) {
	key := "check_fast_timeout"
	if s.CheckTimeout != "" && s.CheckFastTimeout == s.CheckTimeout {
		key = "check_timeout" // the settings' old name for it
	}
	return resolveTimeout(flagValue, given, "check-timeout", key, s.CheckFastTimeout, DefaultCheckTimeout)
}

// ResolveCheckFullTimeout picks how long a run's full check may run: --check-full-timeout (or
// ORCHESTRA_CHECK_FULL_TIMEOUT) when given, else the project's setting, else DefaultCheckFullTimeout.
func ResolveCheckFullTimeout(flagValue time.Duration, given bool, s Settings) (time.Duration, error) {
	return resolveTimeout(flagValue, given, "check-full-timeout", "check_full_timeout", s.CheckFullTimeout,
		DefaultCheckFullTimeout)
}

// resolveTimeout picks a check's time limit: the flag's when given, else the setting key's, else def.
func resolveTimeout(
	flagValue time.Duration, given bool, flagName, key, setting string, def time.Duration,
) (time.Duration, error) {
	if given {
		if flagValue <= 0 {
			return 0, fmt.Errorf("--%s must be a positive duration such as 5m (got %s)", flagName, flagValue)
		}
		return flagValue, nil
	}
	if setting == "" {
		return def, nil
	}
	d, err := ParseCheckTimeout(setting)
	if err != nil {
		return 0, fmt.Errorf("%s: %s %w", SettingsPath("."), key, err)
	}
	return d, nil
}

// ParseCheckTimeout reads a check time limit such as "5m", which must be positive.
func ParseCheckTimeout(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("must be a positive duration such as 5m (got '%s')", v)
	}
	return d, nil
}

// ResolveConflictResolution picks whether a run hands a finished ticket's rebase conflicts back to
// its worker, and for how long: --resolve-conflicts when given, else the project's setting, else on;
// the time limit is the project's, else DefaultResolveTimeout.
func ResolveConflictResolution(flagValue, given bool, s Settings) (on bool, limit time.Duration, err error) {
	on = flagValue
	if !given {
		on = s.ResolveConflicts == nil || *s.ResolveConflicts
	}
	if s.ResolveTimeout == "" {
		return on, DefaultResolveTimeout, nil
	}
	if limit, err = time.ParseDuration(s.ResolveTimeout); err != nil || limit <= 0 {
		return false, 0, fmt.Errorf("%s: resolve_timeout must be a positive duration such as 20m (got '%s')",
			SettingsPath("."), s.ResolveTimeout)
	}
	return on, limit, nil
}

// ResolveCheckHandBacks picks how many times a run hands a failed check on a finished ticket's
// rebased branch back to its worker: the project's setting, else DefaultCheckHandBacks; 0 for never.
func ResolveCheckHandBacks(s Settings) (int, error) {
	if s.CheckHandBacks == nil {
		return DefaultCheckHandBacks, nil
	}
	if n := *s.CheckHandBacks; n >= 0 {
		return n, nil
	}
	return 0, fmt.Errorf("%s: check_hand_backs must be 0 (never) or more (got %d)", SettingsPath("."), *s.CheckHandBacks)
}

// ResolveExcludeTypes picks the issue types a run leaves out of bd ready: the project's setting,
// else DefaultExcludeTypes. Each must be a single type name, as bd splits the list at commas.
func ResolveExcludeTypes(s Settings) ([]string, error) {
	if s.ExcludeTypes == nil {
		return append([]string(nil), DefaultExcludeTypes...), nil
	}
	types := []string{}
	for _, t := range *s.ExcludeTypes {
		if t == "" || strings.ContainsAny(t, ", \t\n") {
			return nil, fmt.Errorf("%s: exclude_types must be a list of issue types such as [\"epic\"] (got %q)",
				SettingsPath("."), t)
		}
		types = append(types, t)
	}
	return types, nil
}

// ResolveSetupFiles picks the globs of the files whose change in a rebase has the setup command run:
// the project's setting, else DefaultSetupFiles. Each must be a glob path.Match takes.
func ResolveSetupFiles(s Settings) ([]string, error) {
	if s.SetupFiles == nil {
		return slices.Clone(DefaultSetupFiles), nil
	}
	globs := []string{}
	for _, g := range *s.SetupFiles {
		if _, err := path.Match(g, ""); err != nil || strings.TrimSpace(g) == "" {
			return nil, fmt.Errorf("%s: setup_files must be a list of file names or globs such as [\"package-lock.json\"] "+
				"(got %q)", SettingsPath("."), g)
		}
		globs = append(globs, g)
	}
	return globs, nil
}

// Efforts are the effort levels claude --effort takes.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// ResolveEffort picks an effort: the flag's (or its variable's) when given, else the project's
// setting called key, else "" (the default). Either must be one of Efforts.
func ResolveEffort(flagValue, key, setting string) (string, error) {
	switch {
	case flagValue != "":
		if !slices.Contains(Efforts, flagValue) {
			return "", fmt.Errorf("--%s must be one of %s (got '%s')",
				strings.ReplaceAll(key, "_", "-"), strings.Join(Efforts, ", "), flagValue)
		}
		return flagValue, nil
	case setting != "" && !slices.Contains(Efforts, setting):
		return "", fmt.Errorf("%s: %s must be one of %s (got '%s')",
			SettingsPath("."), key, strings.Join(Efforts, ", "), setting)
	}
	return setting, nil
}

// ResolveEnvironmentHold picks a run's environment hold from the project's settings: how many
// tickets in a row (0: off) and how soon after dispatch a worker failing counts as failing at once.
func ResolveEnvironmentHold(s Settings) (count int, window time.Duration, err error) {
	count, window = DefaultEnvironmentHoldCount, DefaultEnvironmentHoldWindow
	h := s.EnvironmentHold
	if h == nil {
		return count, window, nil
	}
	if h.Count != nil {
		if count = *h.Count; count < 0 {
			return 0, 0, fmt.Errorf("%s: environment_hold count must be 0 (off) or more (got %d)", SettingsPath("."), count)
		}
	}
	if h.Window != "" {
		if window, err = time.ParseDuration(h.Window); err != nil || window <= 0 {
			return 0, 0, fmt.Errorf("%s: environment_hold window must be a positive duration such as 2m (got '%s')",
				SettingsPath("."), h.Window)
		}
	}
	return count, window, nil
}

// ResolveEnvironmentProbe picks how long after an environment hold, once no ticket runs, the run
// probes the machine with one worker without a ticket; 0 for no probe.
func ResolveEnvironmentProbe(s Settings) (time.Duration, error) {
	h := s.EnvironmentHold
	if h == nil || h.Probe == "" {
		return DefaultEnvironmentProbe, nil
	}
	d, err := time.ParseDuration(h.Probe)
	if h.Probe == "0" {
		d, err = 0, nil
	}
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s: environment_hold probe must be a duration such as 10m, or 0 for none (got '%s')",
			SettingsPath("."), h.Probe)
	}
	return d, nil
}
