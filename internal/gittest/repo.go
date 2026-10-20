package gittest

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A repository a test starts from is copied from a template rather than made with git: every
// process a test binary starts costs it, on macOS, a copy-on-write fault for each page it writes
// next (see capParallel), and a package's tests make hundreds of repositories. Each template is
// made by git once, the first time a test asks for it, in a folder Main removes once the tests are
// over.

// Template is a repository the tests of a package start from, made once and copied for each test
// that asks for it (Repo).
type Template struct {
	build func(dir string) error
	once  sync.Once
	dir   string // the template, once made
	err   error
}

// NewTemplate returns a template whose repository is the one Repo gives, on main with an empty
// commit, then changed by build, given the repository's folder; nil builds nothing more. A package
// makes its templates from a package variable:
//
//	var harnessRepo = gittest.NewTemplate(func(dir string) error { … })
func NewTemplate(build func(dir string) error) *Template { return &Template{build: build} }

// plain is the template Repo copies.
var plain = NewTemplate(nil)

// Repo returns a new folder, removed as t ends, holding a repository on main whose one commit,
// "init", is empty, by t <t@t>, as 'git init -b main' and 'git commit --allow-empty -m init' make
// it, but copied, so that the test starts no git for it. The commit's hash is the same in every
// repository Repo gives a package's tests.
func Repo(t testing.TB) string { return plain.Repo(t) }

// Repo returns a new folder, removed as t ends, holding a copy of the template's repository.
func (tp *Template) Repo(t testing.TB) string {
	t.Helper()
	if !inMain {
		t.Fatal("gittest: the package's TestMain doesn't run its tests through gittest.Main")
	}
	tp.once.Do(tp.make)
	if tp.err != nil {
		t.Fatal(tp.err)
	}
	repo := t.TempDir()
	if err := os.CopyFS(repo, os.DirFS(tp.dir)); err != nil {
		t.Fatal(err)
	}
	return repo
}

// The folder holding the templates, made by the first one and removed by Main.
var (
	inMain    bool   // Main runs the tests
	tempDir   string // where the folder goes: the temporary folder when the tests started
	folder    sync.Once
	templates string
	folderErr error
)

// make makes the template's repository, in a new folder in templates.
func (tp *Template) make() {
	folder.Do(func() { templates, folderErr = os.MkdirTemp(tempDir, "gittest-repos-") })
	if tp.err = folderErr; tp.err != nil {
		return
	}
	dir, err := os.MkdirTemp(templates, "repo-")
	if err != nil {
		tp.err = err
		return
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		if _, err := command.Output(context.Background(), command.WriteLimit, dir, "git",
			append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			tp.err = fmt.Errorf("gittest: making a template repository: %w", err)
			return
		}
	}
	if tp.build != nil {
		if err := tp.build(dir); err != nil {
			tp.err = fmt.Errorf("gittest: making a template repository: %w", err)
			return
		}
	}
	tp.dir = dir
}

// removeTemplates removes the templates' folder, if one was made.
func removeTemplates() {
	if templates == "" {
		return
	}
	if err := os.RemoveAll(templates); err != nil {
		fmt.Fprintln(os.Stderr, "gittest:", err) // said, not failed: a leftover temporary folder breaks no test
	}
}
