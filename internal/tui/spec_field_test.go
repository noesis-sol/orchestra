package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// specRepo is a repository's folder with specs and other files in it, and a home folder, which HOME
// names for the test. In the repository:
//
//	.config/          .hidden.md     README.md     bin.dat (not text)   empty.md (whitespace)
//	My Specs/app.md   docs/notes.go  docs/spec.md  docs/story.txt       links/to-docs -> ../docs   main.go
//
// and in the home folder: Documents/ and home-spec.md.
func specRepo(t *testing.T) (repo, home string) {
	t.Helper()
	repo, home = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	files := map[string]string{
		".config/settings": "x", ".hidden.md": "# Hidden", "README.md": "# Read me", "bin.dat": "\x00\x01\x02",
		"empty.md": " \n\t\n", "My Specs/app.md": "# App", "docs/notes.go": "package docs",
		"docs/spec.md": "# Spec", "docs/story.txt": "A story", "main.go": "package main",
	}
	for name, text := range files {
		write(t, filepath.Join(repo, name), text)
	}
	if err := os.Mkdir(filepath.Join(repo, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "docs"), filepath.Join(repo, "links", "to-docs")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, "Documents", "plan.md"), "# Plan")
	write(t, filepath.Join(home, "home-spec.md"), "# Home spec")
	return repo, home
}

// write writes text to the file p, making its folder.
func write(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSpecPathResolvesTypedAndPastedPaths(t *testing.T) {
	repo, home := specRepo(t)
	for typed, want := range map[string]string{
		"":                       "",
		"   ":                    "",
		"docs/spec.md":           filepath.Join(repo, "docs", "spec.md"),
		"  docs/spec.md \n":      filepath.Join(repo, "docs", "spec.md"),
		"'docs/spec.md'":         filepath.Join(repo, "docs", "spec.md"),
		`"My Specs/app.md"`:      filepath.Join(repo, "My Specs", "app.md"),
		`My\ Specs/app.md`:       filepath.Join(repo, "My Specs", "app.md"), // dropped on the terminal
		"./docs/../README.md":    filepath.Join(repo, "README.md"),
		"../elsewhere/x.md":      filepath.Join(filepath.Dir(repo), "elsewhere", "x.md"),
		"~":                      home,
		"~/":                     home,
		"~/home-spec.md":         filepath.Join(home, "home-spec.md"),
		"~elsewhere/x.md":        filepath.Join(repo, "~elsewhere", "x.md"),
		filepath.Join(home, "x"): filepath.Join(home, "x"),
	} {
		if got := resolveSpecPath(repo, home, typed); got != want {
			t.Errorf("resolveSpecPath(%q) = %q, want %q", typed, got, want)
		}
	}
	if got, want := resolveSpecPath(repo, "", "~/x.md"), filepath.Join(repo, "~", "x.md"); got != want {
		t.Errorf("with no home folder, ~/x.md resolves to %q, want %q", got, want)
	}
}

func TestSpecSuggestionsFollowTheTypedPrefix(t *testing.T) {
	repo, home := specRepo(t)
	for typed, want := range map[string][]string{
		"":               {"My Specs/", "README.md", "docs/", "empty.md", "links/"},
		"d":              {"docs/"},
		"docs/":          {"docs/spec.md", "docs/story.txt"},
		"docs/st":        {"docs/story.txt"},
		"docs/x":         nil,
		"  docs/s":       {"  docs/spec.md", "  docs/story.txt"},
		".":              {".config/", ".hidden.md"},
		".h":             {".hidden.md"},
		"docs/../R":      {"docs/../README.md"},
		"links/":         {"links/to-docs/"},
		"links/to-docs/": {"links/to-docs/spec.md", "links/to-docs/story.txt"},
		"~":              {"~/"},
		"~/":             {"~/Documents/", "~/home-spec.md"},
		"~/Doc":          {"~/Documents/"},
		"nowhere/":       nil,
		"README.md/":     nil,
	} {
		if got := specSuggestions(repo, home, typed); !slices.Equal(got, want) {
			t.Errorf("specSuggestions(%q) = %q, want %q", typed, got, want)
		}
	}
}

func TestSpecFieldChecksThePath(t *testing.T) {
	repo, _ := specRepo(t)
	var path string
	f := newSpecField(repo, &path)
	for typed, want := range map[string]string{
		"README.md":      "",
		"~/home-spec.md": "",
		"":               errNoSpecPath.Error(),
		"nope.md":        "no such file: nope.md",
		" ~/nope.md ":    "no such file: ~/nope.md",
		"docs":           "docs is a folder",
		"bin.dat":        "bin.dat doesn't look like text",
		"empty.md":       "empty.md is empty",
		"README.md/x":    "cannot read README.md/x: not a directory",
	} {
		var got string
		if err := f.check(typed); err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("check(%q) = %q, want %q", typed, got, want)
		}
	}
	if runtime.GOOS != "windows" {
		if err := f.check(os.DevNull); err == nil || err.Error() != os.DevNull+" isn't a file" {
			t.Errorf("check(%q) = %v, want it to say it isn't a file", os.DevNull, err)
		}
	}
	if os.Geteuid() != 0 { // root reads the file all the same
		write(t, filepath.Join(repo, "locked.md"), "# Locked")
		if err := os.Chmod(filepath.Join(repo, "locked.md"), 0); err != nil {
			t.Fatal(err)
		}
		if err := f.check("locked.md"); err == nil || err.Error() != "cannot read locked.md: permission denied" {
			t.Errorf("check(locked.md) = %v, want it to say it can't be read", err)
		}
	}
}

// drivenSpec is the spec field in a form as the start question has it (the Charm theme, Esc and
// Ctrl+C cancelling), driven with key messages; the commands they return aren't run.
type drivenSpec struct {
	form  *huh.Form
	field *specField
	keys  *huh.KeyMap
	path  *string
}

// newDrivenSpec is a spec field for repo in its form, focused, on a window of 80 by 40.
func newDrivenSpec(repo string) drivenSpec {
	d := drivenSpec{path: new(string), keys: huh.NewDefaultKeyMap()}
	d.keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	d.field = newSpecField(repo, d.path)
	d.form = huh.NewForm(huh.NewGroup(d.field)).WithTheme(huh.ThemeCharm()).WithKeyMap(d.keys)
	d.form.Init() // focuses the field
	d.form.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	return d
}

// The keys the spec field is driven with.
var (
	pressCtrlO = tea.KeyMsg{Type: tea.KeyCtrlO}
	pressEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	pressEnter = tea.KeyMsg{Type: tea.KeyEnter}
	pressDown  = tea.KeyMsg{Type: tea.KeyDown}
)

// typing is s typed.
func typing(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// press hands the form keys, one after another.
func (d drivenSpec) press(keys ...tea.KeyMsg) {
	for _, k := range keys {
		d.form.Update(k)
	}
}

// view is what the field draws, as plain text.
func (d drivenSpec) view() string { return ansi.Strip(d.field.View()) }

// The field names Ctrl+O in a hint of its own, below the input, while it has the focus; the help line
// offers Tab once there is something to complete.
func TestSpecFieldHintsAtBrowsing(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	lines := strings.Split(d.view(), "\n")
	if i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "┃ >") }); i < 0 ||
		i+1 == len(lines) || strings.TrimSpace(lines[i+1]) != "┃ ctrl+o Browse…" {
		t.Errorf("the field has no Browse… hint below the input:\n%s", d.view())
	}
	help := ansi.Strip(d.form.View())
	if strings.Contains(help, "tab complete") || strings.Contains(help, "ctrl+o browse") {
		t.Errorf("with nothing typed, the help line offers Tab or repeats the hint:\n%s", help)
	}
	d.press(typing("do"))
	d.field.Update(specSuggestionsMsg{field: d.field, typed: "do", list: specSuggestions(repo, "", "do")})
	if help := ansi.Strip(d.form.View()); !strings.Contains(help, "tab complete") {
		t.Errorf("with docs/ to complete, the help line doesn't offer Tab:\n%s", help)
	}
	d.field.Blur()
	if view := d.view(); strings.Contains(view, "Browse…") {
		t.Errorf("the field left still hints at browsing:\n%s", view)
	}
}

func TestSpecPickerOpensInTheTypedFolder(t *testing.T) {
	repo, home := specRepo(t)
	for typed, want := range map[string]string{
		"":               repo,
		"docs":           filepath.Join(repo, "docs"),
		"docs/":          filepath.Join(repo, "docs"),
		"docs/s":         filepath.Join(repo, "docs"),
		"docs/spec.md":   filepath.Join(repo, "docs"),
		"nowhere/x.md":   repo,
		"~/":             home,
		"~/Documents/pl": filepath.Join(home, "Documents"),
	} {
		d := newDrivenSpec(repo)
		d.press(typing(typed), pressCtrlO)
		if d.field.picker == nil {
			t.Fatalf("%q: Ctrl+O didn't open the picker:\n%s", typed, d.view())
		}
		if got := d.field.picker.CurrentDirectory; got != want {
			t.Errorf("%q: the picker opened in %s, want %s", typed, got, want)
		}
	}
}

func TestSpecPickerShowsTheFolderAndItsFiles(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.form.Update(tea.WindowSizeMsg{Width: 400, Height: 60}) // wide enough for the folder's path
	d.press(pressCtrlO)
	view := d.view()
	for _, want := range []string{repo + "/", "> My Specs", "README.md", "docs", "links", "bin.dat", "main.go"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker lacks %q:\n%s", want, view)
		}
	}
	for _, hidden := range []string{".config", ".hidden.md", "Browse…"} {
		if strings.Contains(view, hidden) {
			t.Errorf("the picker shows %q:\n%s", hidden, view)
		}
	}
	if help := ansi.Strip(d.form.View()); !strings.Contains(help, "esc close") {
		t.Errorf("the help line doesn't offer Esc to close the picker:\n%s", help)
	}
	// The input's title, description and value, the folder, then the picker's ten lines.
	if lines, want := lipgloss.Height(view), lipgloss.Height(d.field.input.View())+1+specPickerHeight; lines != want {
		t.Errorf("the field is %d lines tall with the picker open, want %d:\n%s", lines, want, view)
	}
}

func TestSpecPickerChoosesASpecFile(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(typing("docs/"), pressCtrlO, pressDown, pressEnter) // notes.go, then spec.md
	if d.field.picker != nil {
		t.Fatalf("the picker is still open after choosing spec.md:\n%s", d.view())
	}
	if d.field.typed != "docs/spec.md" || *d.path != filepath.Join(repo, "docs", "spec.md") {
		t.Errorf("the field holds %q, resolved to %q; want docs/spec.md", d.field.typed, *d.path)
	}
	if d.form.State != huh.StateNormal || !strings.Contains(d.view(), "> docs/spec.md") {
		t.Errorf("after choosing, the form is in state %v, the field:\n%s", d.form.State, d.view())
	}
	// Outside the repository, the path is from ~.
	d = newDrivenSpec(repo)
	d.press(typing("~/"), pressCtrlO, pressDown, pressEnter) // Documents, then home-spec.md
	if d.field.typed != "~/home-spec.md" {
		t.Errorf("chosen from the home folder, the field holds %q, want ~/home-spec.md", d.field.typed)
	}
}

func TestSpecPickerRefusesOtherFiles(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(typing("docs/"), pressCtrlO, pressEnter) // notes.go
	if d.field.picker == nil {
		t.Fatal("the picker closed on choosing notes.go")
	}
	if err := d.field.Error(); err == nil || err.Error() != ".md, .markdown and .txt files only" {
		t.Errorf("choosing notes.go: the error is %v, want the files it offers", err)
	}
	if d.field.typed != "docs/" {
		t.Errorf("choosing notes.go changed the path to %q", d.field.typed)
	}
	d.press(pressDown) // the error goes with the next key
	if err := d.field.Error(); err != nil {
		t.Errorf("after moving on, the error is still %v", err)
	}
}

// The picker goes into a folder and back up, and stays where it is, saying why, rather than go into one
// it can't read.
func TestSpecPickerGoesIntoFoldersItCanRead(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(pressCtrlO, pressDown, pressEnter) // My Specs, then docs
	if got, want := d.field.picker.CurrentDirectory, filepath.Join(repo, "docs"); got != want ||
		!strings.Contains(d.view(), "story.txt") {
		t.Fatalf("Enter on docs: the picker is in %s, want %s:\n%s", got, want, d.view())
	}
	d.press(tea.KeyMsg{Type: tea.KeyLeft})
	if got := d.field.picker.CurrentDirectory; got != repo || !strings.Contains(d.view(), "README.md") {
		t.Fatalf("← in docs: the picker is in %s, want %s:\n%s", got, repo, d.view())
	}
	if os.Geteuid() == 0 { // root reads the folder all the same
		return
	}
	locked := filepath.Join(repo, "locked")
	if err := os.Mkdir(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) }) // for the temporary folder's removal
	d = newDrivenSpec(repo)
	d.press(pressCtrlO, pressDown, pressDown, pressDown, pressEnter) // My Specs, docs, links, then locked
	if got := d.field.picker.CurrentDirectory; got != repo || !strings.Contains(d.view(), "README.md") {
		t.Errorf("Enter on a folder it can't read: the picker is in %s, want %s:\n%s", got, repo, d.view())
	}
	if err := d.field.Error(); err == nil || err.Error() != "cannot open "+locked+": permission denied" {
		t.Errorf("Enter on a folder it can't read: the error is %v", err)
	}
}

// Esc closes the picker rather than cancelling the question, which Esc then cancels.
func TestSpecPickerEscClosesItAndKeepsTheQuestion(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(typing("docs/"), pressCtrlO)
	if key.Matches(pressEsc, d.keys.Quit) {
		t.Error("with the picker open, Esc still quits the form")
	}
	d.press(pressEsc)
	switch {
	case d.form.State != huh.StateNormal:
		t.Fatalf("Esc with the picker open ended the form: state %v", d.form.State)
	case d.field.picker != nil:
		t.Fatalf("Esc didn't close the picker:\n%s", d.view())
	case d.field.typed != "docs/":
		t.Errorf("closing the picker changed the path to %q", d.field.typed)
	case !key.Matches(pressEsc, d.keys.Quit) || !key.Matches(tea.KeyMsg{Type: tea.KeyCtrlC}, d.keys.Quit):
		t.Errorf("the form's Quit is %q once the picker closed, want Esc and Ctrl+C", d.keys.Quit.Keys())
	}
	d.press(pressEsc)
	if d.form.State != huh.StateAborted {
		t.Errorf("Esc with the picker closed: state %v, want the form cancelled", d.form.State)
	}
}

// sizedForm is a form on a terminal of 80 by 40 that tells the form its size after each key, as a real
// one does when the form asks for it (tea.WindowSize) as a field grows: the fake terminal can't say.
type sizedForm struct{ *huh.Form }

func (s sizedForm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := s.Form.Update(msg)
	if _, ok := msg.(tea.KeyMsg); ok {
		_, sized := s.Form.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
		cmd = tea.Batch(cmd, sized)
	}
	return s, cmd
}

// onSpec is what the screen shows once the spec field has the focus.
const onSpec = "┃ Spec file"

// askSpecPath asks for a spec's path on a fake terminal, in a form as the start question's, with the
// steps' keys typed. It returns the path resolved and ErrCancelled when the form was cancelled.
func askSpecPath(t *testing.T, repo string, steps ...keysOn) (string, error) {
	t.Helper()
	var path string
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		keys := huh.NewDefaultKeyMap()
		keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
		form := huh.NewForm(huh.NewGroup(newSpecField(repo, &path))).WithTheme(huh.ThemeCharm()).WithKeyMap(keys)
		form.SubmitCmd, form.CancelCmd = tea.Quit, tea.Quit
		_, err := tea.NewProgram(sizedForm{form}, tea.WithContext(t.Context()), tea.WithInput(in),
			tea.WithOutput(out), tea.WithoutSignalHandler()).Run()
		if err == nil && form.State == huh.StateAborted {
			err = ErrCancelled
		}
		return err
	})
	term.typeSteps(t, steps)
	return path, term.end(t)
}

// Terminal keys.
const (
	keyTab, keyEnter, keyEsc, keyDown, keyCtrlN, keyCtrlO, keyCtrlU = "\t", "\r", "\x1b", "\x1b[B", "\x0e", "\x0f", "\x15"
)

func TestSpecFieldTabCompletesFoldersAndSpecFiles(t *testing.T) {
	repo, _ := specRepo(t)
	for _, tc := range []struct {
		name  string
		steps []keysOn
		want  string
	}{
		{"a folder, then a file in it", []keysOn{
			{onSpec, "do"}, {"docs/", keyTab}, {"docs/spec.md", keyTab + keyEnter},
		}, "docs/spec.md"},
		{"the next suggestion with ↓", []keysOn{
			{onSpec, "docs/s"}, {"docs/spec.md", keyDown}, {"docs/story.txt", keyTab + keyEnter},
		}, "docs/story.txt"},
		{"the next suggestion with Ctrl+N", []keysOn{
			{onSpec, "docs/s"}, {"docs/spec.md", keyCtrlN}, {"docs/story.txt", keyTab + keyEnter},
		}, "docs/story.txt"},
		{"a hidden file once a dot is typed", []keysOn{
			{onSpec, ".h"}, {".hidden.md", keyTab + keyEnter},
		}, ".hidden.md"},
	} {
		got, err := askSpecPath(t, repo, tc.steps...)
		if want := filepath.Join(repo, tc.want); err != nil || got != want {
			t.Errorf("%s: the path is %q, %v; want %q", tc.name, got, err, want)
		}
	}
}

func TestSpecFieldTakesTypedAndPastedPaths(t *testing.T) {
	repo, home := specRepo(t)
	paste := func(s string) string { return "\x1b[200~" + s + "\x1b[201~" }
	for _, tc := range []struct {
		name, keys, want string
	}{
		{"relative", "README.md", filepath.Join(repo, "README.md")},
		{"from ~", "~/home-spec.md", filepath.Join(home, "home-spec.md")},
		{"pasted", paste(" " + filepath.Join(repo, "My Specs", "app.md") + "\n"), filepath.Join(repo, "My Specs", "app.md")},
		{"dropped", paste(strings.ReplaceAll(filepath.Join(repo, "My Specs", "app.md"), " ", `\ `) + " "),
			filepath.Join(repo, "My Specs", "app.md")},
	} {
		got, err := askSpecPath(t, repo, keysOn{onSpec, tc.keys}, keysOn{"", keyEnter})
		if err != nil || got != tc.want {
			t.Errorf("%s: the path is %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

// Enter on a path that names no spec says why, and the question stays until it does.
func TestSpecFieldSaysWhatIsWrongWithThePath(t *testing.T) {
	repo, _ := specRepo(t)
	got, err := askSpecPath(t, repo,
		keysOn{onSpec, keyEnter}, keysOn{errNoSpecPath.Error(), "nope.md" + keyEnter},
		keysOn{"no such file: nope.md", keyCtrlU + "docs" + keyEnter},
		keysOn{"docs is a folder", keyCtrlU + "bin.dat" + keyEnter},
		keysOn{"bin.dat doesn't look like text", keyCtrlU + "empty.md" + keyEnter},
		keysOn{"empty.md is empty", keyCtrlU + "README.md" + keyEnter},
	)
	if want := filepath.Join(repo, "README.md"); err != nil || got != want {
		t.Errorf("the path is %q, %v; want %q", got, err, want)
	}
}

func TestSpecFieldBrowsesForAFile(t *testing.T) {
	repo, _ := specRepo(t)
	// At the repository: My Specs, docs, links, then the files. Into docs, past notes.go to spec.md.
	got, err := askSpecPath(t, repo,
		keysOn{onSpec, keyCtrlO}, keysOn{"esc close", keyDown}, keysOn{"", keyEnter},
		keysOn{"story.txt", keyDown}, keysOn{"", keyEnter}, keysOn{"> docs/spec.md", keyEnter},
	)
	if want := filepath.Join(repo, "docs", "spec.md"); err != nil || got != want {
		t.Errorf("the path is %q, %v; want %q", got, err, want)
	}
}

// Esc while browsing closes the picker, and the question goes on; Esc then cancels it.
func TestSpecFieldEscWhileBrowsingKeepsTheQuestion(t *testing.T) {
	repo, _ := specRepo(t)
	got, err := askSpecPath(t, repo,
		keysOn{onSpec, keyCtrlO}, keysOn{"esc close", keyEsc}, keysOn{"", "README.md"}, keysOn{"", keyEnter},
	)
	if want := filepath.Join(repo, "README.md"); err != nil || got != want {
		t.Errorf("the path is %q, %v; want %q", got, err, want)
	}
	if _, err := askSpecPath(t, repo, keysOn{onSpec, keyEsc}); !errors.Is(err, ErrCancelled) {
		t.Errorf("Esc with the picker closed: %v, want the question cancelled", err)
	}
}

// With TERM=dumb, the field asks for the path as a line, until it names a spec file.
func TestAccessibleSpecFieldAsksForATypedPath(t *testing.T) {
	repo, home := specRepo(t)
	t.Setenv("TERM", "dumb")
	ask := func(lines io.Reader) (string, string, error) {
		var path string
		f := newSpecField(repo, &path)
		huh.NewForm(huh.NewGroup(f)).WithTheme(huh.ThemeCharm()) // as the start question gives it its theme
		var out strings.Builder
		err := runAccessible(context.Background(), []huh.Field{f}, lines, &out)
		return path, ansi.Strip(out.String()), err
	}
	path, screen, err := ask(typed("", "nope.md", "docs", "bin.dat", "empty.md", " ~/home-spec.md "))
	if want := filepath.Join(home, "home-spec.md"); err != nil || path != want {
		t.Errorf("the path is %q, %v; want %q\n%s", path, err, want, screen)
	}
	for _, want := range []string{"Spec file", "type the spec's path", "no such file: nope.md", "docs is a folder",
		"bin.dat doesn't look like text", "empty.md is empty"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the accessible form lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "Ctrl+O") {
		t.Errorf("the accessible form offers Ctrl+O:\n%s", screen)
	}
	if _, screen, err := ask(typed("nope.md")); !errors.Is(err, huh.ErrUserAborted) {
		t.Errorf("at the end of the input: %v, want the form cancelled\n%s", err, screen)
	}
}
