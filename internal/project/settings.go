package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Settings is .orchestra/settings.json: how 'orchestra init' set the project up. It is committed,
// so everyone running orchestra on the project shares it.
type Settings struct {
	// Check is the project's check command (lint, build, tests). orchestra runs it again on a
	// finished ticket that had to be rebased onto work merged while it ran.
	Check string `json:"check,omitempty"`
	// Concurrency is how many tickets run at the same time unless --concurrent says otherwise.
	Concurrency int `json:"concurrent"`
}

const (
	SettingsName   = "settings.json"
	MaxConcurrency = 16
)

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
