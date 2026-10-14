//go:build unix

package project

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink that leads to no file yet is followed as os.WriteFile follows it: the file it names is
// made, through a chain of links, relative or not, and the links are kept.
func TestWriteFileFollowsADanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(shared, "settings.json")
	middle := filepath.Join(dir, "middle")
	p := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, middle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("middle", p); err != nil { // relative
		t.Fatal(err)
	}
	if err := writeFile(p, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := read(t, target); got != "new\n" {
		t.Errorf("the file the links lead to has %q, want %q", got, "new\n")
	}
	for _, link := range []string{p, middle} {
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a link (%v)", link, err)
		}
	}
}
