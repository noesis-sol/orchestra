package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settings.json's requires list reads into Requirements, and init keeps it as it saves the settings.
func TestRequiresReadAndKept(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	text := `{"concurrent": 1, "requires": [
		{"name": "jq", "check": "command -v jq", "install": "brew install jq", "platforms": ["darwin"]},
		{"name": "docker", "check": "docker info", "manual": "start Docker Desktop"}]}`
	if err := os.WriteFile(SettingsPath(repo), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []Requirement{
		{Name: "jq", Check: "command -v jq", Install: "brew install jq", Platforms: []string{"darwin"}},
		{Name: "docker", Check: "docker info", Manual: "start Docker Desktop"},
	}
	for _, when := range []string{"as written", "once init saved the settings"} {
		s, _, err := LoadSettings(repo)
		if err != nil {
			t.Fatal(err)
		}
		list, err := ResolveRequires(s)
		if err != nil || len(list) != len(want) || !list[0].Equal(want[0]) || !list[1].Equal(want[1]) {
			t.Errorf("%s: %+v, %v; want %+v", when, list, err, want)
		}
		if _, err := settingsStep(repo, Choice{Concurrent: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := ResolveRequires(Settings{}); err != nil || len(list) != 0 {
		t.Errorf("no list: %+v, %v", list, err)
	}
}

// A requirement a run couldn't check or install is refused as the settings load, saying which and why:
// an install that runs sudo among them, which would ask for a password with no terminal to type it in.
func TestResolveRequiresRefuses(t *testing.T) {
	ok := Requirement{Name: "jq", Check: "command -v jq", Install: "brew install jq"}
	with := func(f func(r *Requirement)) Requirement {
		r := ok
		f(&r)
		return r
	}
	for _, c := range []struct {
		name string
		list []Requirement
		want string // "" for none refused
	}{
		{"fine", []Requirement{ok, with(func(r *Requirement) { r.Name, r.Install, r.Manual = "gh-auth", "", "gh auth login" })}, ""},
		{"linux alone", []Requirement{with(func(r *Requirement) { r.Platforms = []string{"linux"} })}, ""},
		{"a word with sudo in it", []Requirement{with(func(r *Requirement) { r.Install = "pip install pseudo" })}, ""},
		{"sudo", []Requirement{with(func(r *Requirement) { r.Install = "sudo apt-get install -y jq" })},
			"entry 1 (jq) install runs sudo, which asks for a password"},
		{"sudo after another command", []Requirement{with(func(r *Requirement) { r.Install = "apt-get update && sudo apt-get install jq" })},
			"install runs sudo"},
		{"sudo past a variable", []Requirement{with(func(r *Requirement) { r.Install = "DEBIAN_FRONTEND=noninteractive sudo apt install jq" })},
			"install runs sudo"},
		{"sudo in a pipe", []Requirement{with(func(r *Requirement) { r.Install = "curl -fsSL x.sh | sudo sh" })},
			"install runs sudo"},
		{"su", []Requirement{with(func(r *Requirement) { r.Install = "su -c 'make install'" })}, "install runs su"},
		{"no name", []Requirement{with(func(r *Requirement) { r.Name = "" })}, "entry 1 needs a name"},
		{"a name with a slash", []Requirement{with(func(r *Requirement) { r.Name = "../x" })}, "needs a name"},
		{"two of a name", []Requirement{ok, ok}, "entry 2 (jq) has the name of an entry before it"},
		{"no check", []Requirement{with(func(r *Requirement) { r.Check = " " })}, "needs a check"},
		{"neither", []Requirement{with(func(r *Requirement) { r.Install = "" })}, "needs an install"},
		{"both", []Requirement{with(func(r *Requirement) { r.Manual = "or download it" })}, "has both"},
		{"windows", []Requirement{with(func(r *Requirement) { r.Platforms = []string{"windows"} })},
			"platforms that must be among darwin, linux (got 'windows')"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveRequires(Settings{Requires: c.list})
			switch {
			case c.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) ||
				!strings.Contains(err.Error(), SettingsName+": requires ")):
				t.Errorf("error %v, want one naming the requires list and saying %q", err, c.want)
			}
		})
	}
}

func TestRequirementAppliesOn(t *testing.T) {
	r := Requirement{Name: "x"}
	if !r.AppliesOn("darwin") || !r.AppliesOn("linux") {
		t.Error("one without platforms applies everywhere")
	}
	r.Platforms = []string{"linux"}
	if r.AppliesOn("darwin") || !r.AppliesOn("linux") {
		t.Error("one on linux alone applies there alone")
	}
}
