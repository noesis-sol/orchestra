package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// askConcurrency asks how many tickets to run at the same time, defaulting to 1 on an empty
// answer, and to 1 after three answers it can't use.
func askConcurrency(in io.Reader, out io.Writer) int {
	fmt.Fprintf(out, "\nHow many tickets should run at the same time by default?\n"+
		"Each gets its own worker and runs the project's checks, so more than 1 needs checks that\n"+
		"cope with running side by side (separate simulators, ports, databases). A run can\n"+
		"override it with --concurrent.\n")
	r := bufio.NewReader(in)
	for try := 0; try < 3; try++ {
		fmt.Fprintf(out, "Tickets at the same time (1-%d) [1]: ", maxConcurrency)
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			if err != nil && try > 0 {
				break // no more input
			}
			return 1
		}
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= maxConcurrency {
			return n
		}
		fmt.Fprintf(out, "Please answer with a number from 1 to %d.\n", maxConcurrency)
		if err != nil {
			break
		}
	}
	fmt.Fprintln(out, "Using 1.")
	return 1
}

// configureSettings writes .orchestra/settings.json for 'orchestra init'. Existing settings are
// kept unless --check or --concurrent changes them; without either and without settings, it asks
// for the concurrency when it can (interactive), and uses 1 otherwise.
func configureSettings(repo, check string, concurrency int, interactive bool, in io.Reader, out io.Writer) ([]string, error) {
	s, had, err := loadSettings(repo)
	if err != nil {
		return nil, err
	}
	if check != "" {
		s.Check = check
	}
	var note string
	switch {
	case concurrency > 0:
		if concurrency > maxConcurrency {
			return nil, fmt.Errorf("--concurrent must be between 1 and %d", maxConcurrency)
		}
		s.Concurrency = concurrency
	case had && s.Concurrency > 0:
	case interactive:
		s.Concurrency = askConcurrency(in, out)
	default:
		s.Concurrency = 1
		note = " (not asked: no terminal; --concurrent sets it)"
	}
	if err := saveSettings(repo, s); err != nil {
		return nil, err
	}
	check = s.Check
	if check == "" {
		check = "none, so a rebased ticket merges unchecked; --check sets it"
	}
	verb := "saved"
	if had {
		verb = "updated"
	}
	return []string{fmt.Sprintf("✓ %s %s/%s: %d at the same time%s, check: %s", verb, orchDir, settingsName, s.Concurrency, note, check)}, nil
}
