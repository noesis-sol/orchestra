package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSetupFiles(t *testing.T) {
	list := func(globs ...string) *[]string { return &globs }
	cases := []struct {
		setting *[]string
		want    string
		fails   bool
	}{
		{nil, strings.Join(DefaultSetupFiles, " "), false}, {list(), "", false},
		{list("package-lock.json", "web/*.lock"), "package-lock.json web/*.lock", false},
		{list(""), "", true}, {list(" "), "", true}, {list("["), "", true},
	}
	for _, c := range cases {
		got, err := ResolveSetupFiles(Settings{SetupFiles: c.setting})
		if (err != nil) != c.fails || (!c.fails && strings.Join(got, " ") != c.want) {
			t.Errorf("ResolveSetupFiles(%v) = %q, %v", c.setting, got, err)
		}
	}
	// The default is copied, so a run can't change it.
	got, _ := ResolveSetupFiles(Settings{})
	got[0] = "x"
	if DefaultSetupFiles[0] != "package.json" {
		t.Error("the default changed")
	}
}

// init keeps the setup it doesn't ask about.
func TestInitKeepsTheSetup(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	globs := []string{"package-lock.json"}
	if err := SaveSettings(repo, Settings{CheckFast: "make check", Concurrency: 1, Setup: "npm ci",
		SetupFiles: &globs}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySettings(repo, Choice{Concurrent: 2}); err != nil {
		t.Fatal(err)
	}
	s, _, err := LoadSettings(repo)
	if err != nil || s.Setup != "npm ci" || s.SetupFiles == nil || strings.Join(*s.SetupFiles, " ") != "package-lock.json" {
		t.Errorf("after init: %+v, %v", s, err)
	}
}
