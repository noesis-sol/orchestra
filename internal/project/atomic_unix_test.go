//go:build unix

package project

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modeOf(t *testing.T, p string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// readOnly makes dir read-only until the test ends.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes in a read-only folder")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // before t.TempDir removes it
}

// The file is replaced whole and keeps its mode, with no temporary file left beside it.
func TestWriteFileReplacesTheFileKeepingItsMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := writeFile(p, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, mode := read(t, p), modeOf(t, p); got != "new\n" || mode != 0o600 {
		t.Errorf("new file: %q, mode %v; want %q, mode %v", got, mode, "new\n", fs.FileMode(0o600))
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(p, []byte("newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, mode := read(t, p), modeOf(t, p); got != "newer\n" || mode != 0o640 {
		t.Errorf("replaced file: %q, mode %v; want %q, mode %v", got, mode, "newer\n", fs.FileMode(0o640))
	}
	if files := listing(t, dir); len(files) != 1 {
		t.Errorf("want the file alone, got %v", files)
	}
}

// A symlink is followed, as os.WriteFile follows it: the file it leads to is replaced, and the link
// kept.
func TestWriteFileFollowsALink(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "exclude")
	if err := os.WriteFile(target, []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "exclude")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(link, []byte("# mine\nnew\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v, %v", fi, err)
	}
	if got, mode := read(t, target), modeOf(t, target); got != "# mine\nnew\n" || mode != 0o600 {
		t.Errorf("the linked file: %q, mode %v", got, mode)
	}
	if files := listing(t, elsewhere); len(files) != 1 {
		t.Errorf("want the linked file alone, got %v", files)
	}
}

// A write that fails, in a read-only folder or at the rename, leaves the file as it was, and no
// temporary file behind.
func TestAFailedWriteLeavesTheFileAsItWas(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "exclude")
	if err := os.WriteFile(p, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readOnly(t, dir)
	err := writeFile(p, []byte("lost\n"), 0o644)
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), p) {
		t.Errorf("want a permission error naming %s, got %v", p, err)
	}
	if got := read(t, p); got != "# mine\n" {
		t.Errorf("the file was changed to %q", got)
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "folder") // can't be renamed over
	if err := os.MkdirAll(filepath.Join(folder, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(folder, []byte("lost\n"), 0o644); err == nil {
		t.Error("wrote over a folder")
	}
	if files := listing(t, dir); len(files) != 1 || files[p] != "# mine\n" {
		t.Errorf("want the file alone, as it was, got %v", files)
	}
}

// The settings, git's info/exclude and .gitattributes are left as they were when they can't be
// written.
func TestFailedWritesKeepTheProjectsFiles(t *testing.T) {
	repo, _ := gitRepo(t)
	settings := SettingsPath(repo)
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = `{"check": "make check", "future": true}` + "\n"
	if err := os.WriteFile(settings, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	readOnly(t, filepath.Dir(settings))
	if err := SaveSettings(repo, Settings{Concurrency: 2}); err == nil {
		t.Error("SaveSettings: no error in a read-only folder")
	}
	if got := read(t, settings); got != mine {
		t.Errorf("settings.json changed to %q", got)
	}

	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# mine\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readOnly(t, filepath.Dir(exclude))
	if err := EnsureRunExcluded(context.Background(), repo); err == nil {
		t.Error("EnsureRunExcluded: no error in a read-only folder")
	}
	if got := read(t, exclude); got != "# mine\n*.log\n" {
		t.Errorf("info/exclude changed to %q", got)
	}

	if err := os.WriteFile(filepath.Join(repo, changelogName), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	attrs := filepath.Join(repo, attributesName)
	if err := os.WriteFile(attrs, []byte("*.png binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readOnly(t, repo)
	if _, _, err := ApplyUnion(context.Background(), repo, Choice{Union: true}); err == nil {
		t.Error("ApplyUnion: no error in a read-only folder")
	}
	if got := read(t, attrs); got != "*.png binary\n" {
		t.Errorf(".gitattributes changed to %q", got)
	}
}

// The run state replaces a symlink in its place, even one that stays in the checkout, rather than
// write where it leads.
func TestStateReplacesALinkInItsPlace(t *testing.T) {
	repo := t.TempDir()
	target := filepath.Join(repo, "elsewhere.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plant(t, repo, RunPath(StateName), target)
	if err := SaveState(repo, RunState{Workers: []LeftWorker{{Ticket: "x-1", Left: "PAUSED"}}}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(repo, RunPath(StateName))); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("want a file in place of the link: %v, %v", fi, err)
	}
	if got := read(t, target); got != "{}\n" {
		t.Errorf("written through the link: %q", got)
	}
}
