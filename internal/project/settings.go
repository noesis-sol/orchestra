package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// ExcludeTypes are the issue types never taken from bd ready, such as epics, whose children
	// are the work. Absent: DefaultExcludeTypes; [] takes every type.
	ExcludeTypes *[]string `json:"exclude_types,omitempty"`
}

const (
	SettingsName   = "settings.json"
	MaxConcurrency = 16
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

// SaveSettings writes the project's settings.json.
func SaveSettings(repo string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SettingsPath(repo), append(b, '\n'), 0o644)
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
		return 0, fmt.Errorf("%s: ticket_limit must be a duration such as 2h, or 0 for none (got '%s')", SettingsPath("."), s.TicketLimit)
	}
	return d, nil
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
			return nil, fmt.Errorf("%s: exclude_types must be a list of issue types such as [\"epic\"] (got %q)", SettingsPath("."), t)
		}
		types = append(types, t)
	}
	return types, nil
}
