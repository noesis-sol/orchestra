package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Settings is .orchestra/settings.json: how 'orchestra init' set the project up. It is committed,
// so everyone running orchestra on the project shares it.
type Settings struct {
	// Check is the project's check command (lint, build, tests). orchestra runs it again on a
	// finished ticket that had to be rebased onto work merged while it ran.
	Check string `json:"check,omitempty"`
	// Concurrency is how many tickets run at the same time unless --concurrent says otherwise.
	Concurrency int `json:"concurrent"`
	// TicketLimit is how long a ticket's worker may go on, from dispatch, before the run stops for
	// it, as a duration such as "2h", unless --ticket-limit says otherwise. Empty or "0": no limit.
	TicketLimit string `json:"ticket_limit,omitempty"`
	// CheckTimeout is how long the check command may run on a rebased ticket before orchestra stops
	// it and sets the ticket aside, as a duration such as "5m", unless --check-timeout says
	// otherwise. Empty: DefaultCheckTimeout.
	CheckTimeout string `json:"check_timeout,omitempty"`
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
	// DefaultCheckTimeout is how long the check command may run when settings.json sets no limit.
	DefaultCheckTimeout = 30 * time.Minute
	// DefaultCheckTimeoutText is DefaultCheckTimeout as settings.json writes it.
	DefaultCheckTimeoutText = "30m"
	// DefaultResolveTimeout is how long a worker may take to resolve its rebase's conflicts when
	// settings.json sets no limit.
	DefaultResolveTimeout = 20 * time.Minute
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

// SettingsPath is where the project's settings.json is.
func SettingsPath(repo string) string { return filepath.Join(repo, Dir, SettingsName) }

// LoadSettings reads the project's settings; ok is false if there are none.
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
	return s, true, nil
}

// SaveSettings writes the project's settings.json, keeping any keys Settings doesn't know.
func SaveSettings(repo string, s Settings) error {
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
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
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

// ResolveCheckTimeout picks how long a run's check command may run: --check-timeout (or
// ORCHESTRA_CHECK_TIMEOUT) when given, else the project's setting, else DefaultCheckTimeout.
func ResolveCheckTimeout(flagValue time.Duration, given bool, s Settings) (time.Duration, error) {
	if given {
		if flagValue <= 0 {
			return 0, fmt.Errorf("--check-timeout must be a positive duration such as 5m (got %s)", flagValue)
		}
		return flagValue, nil
	}
	if s.CheckTimeout == "" {
		return DefaultCheckTimeout, nil
	}
	d, err := ParseCheckTimeout(s.CheckTimeout)
	if err != nil {
		return 0, fmt.Errorf("%s: check_timeout %w", SettingsPath("."), err)
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
