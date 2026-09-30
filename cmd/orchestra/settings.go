package main

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
	settingsName   = "settings.json"
	maxConcurrency = 16
)

func settingsPath(repo string) string { return filepath.Join(repo, orchDir, settingsName) }

// loadSettings reads the project's settings; ok is false if there are none.
func loadSettings(repo string) (s Settings, ok bool, err error) {
	b, err := os.ReadFile(settingsPath(repo))
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, false, fmt.Errorf("%s: %w", settingsPath(repo), err)
	}
	return s, true, nil
}

func saveSettings(repo string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(repo), append(b, '\n'), 0o644)
}

// resolveConcurrency picks a run's concurrency: --concurrent (or ORCHESTRA_CONCURRENT), else the
// project's setting, else 1.
func resolveConcurrency(flagValue int, s Settings) (int, error) {
	n := flagValue
	if n == 0 {
		n = s.Concurrency
	}
	if n == 0 {
		n = 1
	}
	if n < 1 || n > maxConcurrency {
		return 0, fmt.Errorf("--concurrent must be between 1 and %d (got %d)", maxConcurrency, n)
	}
	return n, nil
}
