package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pressLeft is ←, which takes the picker up a folder.
var pressLeft = tea.KeyMsg{Type: tea.KeyLeft}

// Up a folder that lost entries while the picker was in one inside it, the cursor is on the folder it came
// from, rather than at its old place past the end of the list, where Enter panicked in bubbles' picker.
func TestSpecPickerBackIntoAShrunkFolder(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(pressCtrlO, pressDown, pressDown, pressEnter) // My Specs, docs, then links
	if got, want := d.field.picker.CurrentDirectory, filepath.Join(repo, "links"); got != want {
		t.Fatalf("Enter on links: the picker is in %s, want %s:\n%s", got, want, d.view())
	}
	for _, name := range []string{"My Specs", "docs", "README.md", "bin.dat", "empty.md", "main.go"} {
		if err := os.RemoveAll(filepath.Join(repo, name)); err != nil {
			t.Fatal(err)
		}
	}
	d.press(pressLeft)
	if got := d.field.picker.CurrentDirectory; got != repo || !strings.Contains(d.view(), "> links") {
		t.Fatalf("← in links: the picker is in %s, want %s with the cursor on links:\n%s", got, repo, d.view())
	}
	d.press(pressEnter)
	if got, want := d.field.picker.CurrentDirectory, filepath.Join(repo, "links"); got != want {
		t.Errorf("Enter on links again: the picker is in %s, want %s:\n%s", got, want, d.view())
	}
}

// Up a folder, and up again, the cursor is on the folder the picker came from each time, even where the
// folder above gained an entry before it.
func TestSpecPickerBackPutsTheCursorOnTheFolderItLeft(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(pressCtrlO, pressDown, pressDown, pressEnter, pressEnter) // links, then to-docs in it
	if got, want := d.field.picker.CurrentDirectory, filepath.Join(repo, "links", "to-docs"); got != want {
		t.Fatalf("Enter on links, then on to-docs: the picker is in %s, want %s:\n%s", got, want, d.view())
	}
	write(t, filepath.Join(repo, "added", "x.md"), "# X") // listed after My Specs, before links
	d.press(pressLeft)
	if got, want := d.field.picker.CurrentDirectory, filepath.Join(repo, "links"); got != want ||
		!strings.Contains(d.view(), "> to-docs") {
		t.Errorf("← in to-docs: the picker is in %s, want %s with the cursor on to-docs:\n%s", got, want, d.view())
	}
	d.press(pressLeft)
	if got := d.field.picker.CurrentDirectory; got != repo || !strings.Contains(d.view(), "> links") {
		t.Errorf("← in links: the picker is in %s, want %s with the cursor on links:\n%s", got, repo, d.view())
	}
	// The cursor moves on from there.
	d.press(pressDown)
	if view := d.view(); !strings.Contains(view, "> README.md") {
		t.Errorf("↓ from links: the cursor isn't on README.md:\n%s", view)
	}
}

// A folder the picker didn't list, a hidden one it was opened in, leaves the cursor on the first entry of
// the folder above it; as does one that went.
func TestSpecPickerBackFromAnUnlistedFolder(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(typing(".config/"), pressCtrlO, pressLeft)
	if got := d.field.picker.CurrentDirectory; got != repo || !strings.Contains(d.view(), "> My Specs") {
		t.Errorf("← in .config: the picker is in %s, want %s with the cursor on My Specs:\n%s", got, repo, d.view())
	}
	if got := pickerIndex(repo, "nowhere"); got != -1 {
		t.Errorf("an entry the folder lacks is listed at %d, want -1", got)
	}
	if got := pickerIndex(filepath.Join(repo, "nowhere"), "x"); got != -1 {
		t.Errorf("an entry of a folder that isn't there is listed at %d, want -1", got)
	}
}

// pickerIndex lists a folder as bubbles' picker does: folders first, then files, each by name; hidden
// entries out; a link to a folder among the files.
func TestSpecPickerIndexListsAsThePickerDoes(t *testing.T) {
	repo, _ := specRepo(t)
	if err := os.Symlink("docs", filepath.Join(repo, "a-link")); err != nil {
		t.Fatal(err)
	}
	listed := []string{"My Specs", "docs", "links", "README.md", "a-link", "bin.dat", "empty.md", "main.go"}
	for i, name := range listed {
		if got := pickerIndex(repo, name); got != i {
			t.Errorf("%s is listed at %d, want %d", name, got, i)
		}
	}
	for _, hidden := range []string{".config", ".hidden.md"} {
		if got := pickerIndex(repo, hidden); got != -1 {
			t.Errorf("hidden %s is listed at %d, want -1", hidden, got)
		}
	}
	// As the picker shows it: an entry's line starts with the cursor or the space for it.
	d := newDrivenSpec(repo)
	d.press(pressCtrlO)
	var shown []string
	for l := range strings.SplitSeq(d.view(), "\n") {
		if !strings.HasPrefix(l, "┃ > ") && !strings.HasPrefix(l, "┃   ") {
			continue
		}
		if name, _, _ := strings.Cut(strings.TrimSpace(l[len("┃ > "):]), " →"); name != "" {
			shown = append(shown, name)
		}
	}
	if strings.Join(shown, "|") != strings.Join(listed, "|") {
		t.Errorf("the picker shows %q, want %q:\n%s", shown, listed, d.view())
	}
}
