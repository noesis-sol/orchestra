package project

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// A version settings.json records above this orchestra's, a newer orchestra's on a team running several,
// stays: init --update never lowers it, though it records the version of a template it brings up to date.
func TestUpdateTemplatesKeepsANewerRecordedVersion(t *testing.T) {
	repo := templatesRepo(t, true)
	now := CurrentTemplates()
	s, _, _ := LoadSettings(repo)
	s.Templates = &Templates{WorkerPrompt: now.WorkerPrompt + 3}
	if err := SaveSettings(repo, s); err != nil {
		t.Fatal(err)
	}
	_, updates, err := UpdateTemplates(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Path != VerifierPath {
		t.Errorf("updates %+v, want the verifier's alone", updates)
	}
	s, _, err = LoadSettings(repo)
	want := Templates{WorkerPrompt: now.WorkerPrompt + 3, Verifier: now.Verifier}
	if err != nil || s.Templates == nil || *s.Templates != want {
		t.Errorf("templates %+v, %v; want %+v", s.Templates, err, want)
	}
}

const nestedUnknown = `{
  "concurency": 4,
  "concurrent": 2,
  "environment_hold": {"count": 3, "cout": 5},
  "requires": [{"name": "jq", "check": "command -v jq"}, {"name": "pg", "check": "pg_isready", "instal": "x"}],
  "templates": {"worker_prompt": 1, "planner": 2}
}`

// A key orchestra doesn't know, a typo or a newer orchestra's, is named as the run starts, at the top of
// settings.json or within a key it knows.
func TestUnknownSettingsAreNamed(t *testing.T) {
	want := []string{"concurency", "environment_hold.cout", "requires[1].instal", "templates.planner"}
	if got := UnknownSettings([]byte(nestedUnknown)); !slices.Equal(got, want) {
		t.Errorf("UnknownSettings = %q, want %q", got, want)
	}
	if got := UnknownSettings([]byte(`{"check": "make", "check_timeout": "5m", "concurrent": 1}`)); len(got) != 0 {
		t.Errorf("the old names are known: %q", got)
	}
	repo := t.TempDir()
	writeRepoFile(t, repo, Dir+"/"+SettingsName, nestedUnknown)
	notes := SettingsNotes(repo)
	if len(notes) != len(want) || !strings.HasPrefix(notes[0], ".orchestra/settings.json: concurency is no setting") {
		t.Errorf("notes %q", notes)
	}
	if s, err := ParseSettings([]byte(nestedUnknown)); err != nil || *s.Concurrency != 2 {
		t.Errorf("the settings it knows are read: %+v, %v", s, err)
	}
}

// Saving keeps the keys orchestra doesn't know within those it does, as a newer orchestra's sub-keys
// when an older init saves, and drops a known one the settings no longer hold.
func TestSaveSettingsKeepsUnknownNestedKeys(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, repo, Dir+"/"+SettingsName, nestedUnknown)
	s, _, err := LoadSettings(repo)
	if err != nil {
		t.Fatal(err)
	}
	s.EnvironmentHold.Count = nil
	s.Templates.WorkerPrompt = 7
	s.Requires[1].Manual = "start postgres"
	if err := SaveSettings(repo, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(SettingsPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := UnknownSettings(raw), UnknownSettings([]byte(nestedUnknown)); !slices.Equal(got, want) {
		t.Errorf("unknown keys after the save %q, want %q:\n%s", got, want, raw)
	}
	var saved struct {
		EnvironmentHold map[string]any   `json:"environment_hold"`
		Requires        []map[string]any `json:"requires"`
		Templates       map[string]any   `json:"templates"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.EnvironmentHold["count"]; ok || saved.Templates["worker_prompt"] != float64(7) ||
		saved.Requires[1]["manual"] != "start postgres" || saved.Requires[1]["instal"] != "x" {
		t.Errorf("the known keys follow the settings:\n%s", raw)
	}
}

// "concurrent": 0 in settings.json is out of range, as --concurrent 0 is, rather than taken as unset.
func TestConcurrentZeroInSettingsIsOutOfRange(t *testing.T) {
	s, err := ParseSettings([]byte(`{"concurrent": 0}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveConcurrency(Given[int]{}, s)
	if want := ".orchestra/settings.json: concurrent must be between 1 and 16 (got 0)"; err == nil || err.Error() != want {
		t.Errorf("ResolveConcurrency = %v, want %s", err, want)
	}
	_, err = ResolveConcurrency(Given[int]{Value: 0, Given: true, Name: "--concurrent"}, Settings{})
	if want := "--concurrent must be between 1 and 16 (got 0)"; err == nil || err.Error() != want {
		t.Errorf("the flag's: %v, want %s", err, want)
	}
	if s, _ = ParseSettings([]byte(`{}`)); s.Concurrency != nil {
		t.Errorf("absent is unset: %v", *s.Concurrency)
	}
	if n, err := ResolveConcurrency(Given[int]{}, s); n != DefaultConcurrency || err != nil {
		t.Errorf("absent: %d, %v", n, err)
	}
}
