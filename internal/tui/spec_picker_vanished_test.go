package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Files removed or renamed after the picker read its folder are drawn as the folder is now, rather than
// panicking in bubbles' picker, which has no FileInfo for them; the cursor goes back to the first entry.
func TestSpecPickerRereadsAFolderWhoseFilesWent(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(pressCtrlO, pressDown, pressDown, pressDown) // My Specs, docs, links, then README.md
	if !strings.Contains(d.view(), "> README.md") {
		t.Fatalf("the cursor isn't on README.md:\n%s", d.view())
	}
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, "main.go"), filepath.Join(repo, "moved.go")); err != nil {
		t.Fatal(err)
	}
	view := d.view()
	for _, want := range []string{"> My Specs", "bin.dat", "moved.go"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker lacks %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"README.md", "main.go"} {
		if strings.Contains(view, gone) {
			t.Errorf("the picker still shows %q:\n%s", gone, view)
		}
	}
	if err := d.field.Error(); err != nil {
		t.Errorf("reading the folder again: the error is %v", err)
	}
	// Through the form, as a key draws the picker: the key moves the cursor in the list drawn before, which
	// then goes back to the first entry of the folder read again, and the picker goes on from there.
	if err := os.Remove(filepath.Join(repo, "bin.dat")); err != nil {
		t.Fatal(err)
	}
	d.press(pressDown)
	if view := d.view(); !strings.Contains(view, "> My Specs") || strings.Contains(view, "bin.dat") {
		t.Errorf("with bin.dat removed, ↓ didn't read the folder again:\n%s", view)
	}
	d.press(pressDown, pressEnter) // docs
	if got, want := d.field.picker.CurrentDirectory, filepath.Join(repo, "docs"); got != want {
		t.Errorf("after reading the folder again, Enter on docs: the picker is in %s, want %s:\n%s", got, want, d.view())
	}
}

// A folder removed while the picker shows it gives way to the nearest one above it, and the field says
// why.
func TestSpecPickerLeavesAFolderThatWent(t *testing.T) {
	repo, _ := specRepo(t)
	d := newDrivenSpec(repo)
	d.press(typing("My Specs/"), pressCtrlO)
	gone := filepath.Join(repo, "My Specs")
	if got := d.field.picker.CurrentDirectory; got != gone {
		t.Fatalf("the picker opened in %s, want %s", got, gone)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	view := d.view()
	if got := d.field.picker.CurrentDirectory; got != repo || !strings.Contains(view, "> docs") {
		t.Errorf("with its folder removed, the picker is in %s, want %s:\n%s", got, repo, view)
	}
	if err := d.field.Error(); err == nil || err.Error() != "cannot open "+gone+": no such file or directory" {
		t.Errorf("with its folder removed, the error is %v", err)
	}
	d.press(pressDown) // the error goes with the next key
	if err := d.field.Error(); err != nil {
		t.Errorf("after moving on, the error is still %v", err)
	}
}
