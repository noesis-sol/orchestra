package project

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/noesis-sol/orchestra/skills"
)

// setupPaths are the files Apply may write in the repository, from its top, with the folders they
// are in: the worker prompt where it is and where earlier versions kept it, the .gitignore, the
// runners, the settings, the README, .gitattributes, the verifier, and the create-check-suite
// skill's files where the choice installs it. An applier that writes another file adds it here, or a
// failure after it would leave it written (TestSetupPathsHoldWhatApplyWrites). Git's info/exclude,
// outside the repository, isn't among them: its entry for .orchestra/run/ is right however init ends.
func setupPaths(c Choice) []string {
	files := []string{Dir + "/" + promptName, Dir + "/.gitignore", legacyPrompt, Dir + "/" + SettingsName,
		FastRunner, FullRunner, ReadmeName, attributesName, VerifierPath}
	if dir := SkillsDir(c.Agent); dir != "" && c.FilesTestWork() {
		dir = path.Join(dir, skills.CreateCheckSuiteName)
		_ = fs.WalkDir(skills.CreateCheckSuite(), ".", func(name string, _ fs.DirEntry, err error) error {
			if err == nil { // an embedded folder: it can't fail
				files = append(files, path.Join(dir, name))
			}
			return nil
		})
	}
	var paths []string
	for _, f := range files {
		for p := f; p != "."; p = path.Dir(p) {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// setupFile is what was at one of setupPaths before Apply: nothing, a folder, or a file with its
// content and mode. Anything else, a socket or the like, is left as it is.
type setupFile struct {
	rel       string
	there     bool
	dir, file bool
	data      []byte
	mode      fs.FileMode
}

// snapshot is what was at each of setupPaths in the repository at repo, for restore to put back.
type snapshot struct {
	repo  string
	files []setupFile
}

// takeSnapshot reads what is at each of the choice's setupPaths in the repository at repo. It fails
// where it can't tell, as Apply then can't promise to put it back.
func takeSnapshot(repo string, c Choice) (snapshot, error) {
	s := snapshot{repo: repo}
	for _, rel := range setupPaths(c) {
		f := setupFile{rel: rel}
		fi, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist), isNotDir(err):
		case err != nil:
			return snapshot{}, err
		case fi.IsDir():
			f.there, f.dir = true, true
		case fi.Mode().IsRegular():
			f.there, f.file, f.mode = true, true, fi.Mode().Perm()
			if f.data, err = os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
				return snapshot{}, err
			}
		default:
			f.there = true
		}
		s.files = append(s.files, f)
	}
	return s, nil
}

// isNotDir reports whether err is a path's folder being a file, as os.Stat says of a/b where a is
// one: nothing is at the path.
func isNotDir(err error) bool { return errors.Is(err, syscall.ENOTDIR) }

// lookup is what the snapshot has at rel; ok is false where rel isn't one of its paths.
func (s snapshot) lookup(rel string) (setupFile, bool) {
	for _, f := range s.files {
		if f.rel == rel {
			return f, true
		}
	}
	return setupFile{}, false
}

// restore puts back what the snapshot holds: a file as it was, its content and mode; a file or
// folder that wasn't there removed, a folder only once nothing is left in it. A worker prompt moved
// from .claude/ goes back the way it came, git's index with it. The deepest paths go first, so a
// folder is emptied before it is removed. It goes on past a path it can't put back, and returns the
// errors joined.
func (s snapshot) restore(ctx context.Context) error {
	var errs []error
	legacy, _ := s.lookup(legacyPrompt)
	prompt := filepath.Join(s.repo, Dir, promptName)
	if legacy.file && !fileExists(filepath.Join(s.repo, legacyPrompt)) && fileExists(prompt) {
		errs = append(errs, movePrompt(ctx, s.repo, prompt, filepath.Join(s.repo, legacyPrompt)))
	}
	files := slices.Clone(s.files)
	slices.SortStableFunc(files, func(a, b setupFile) int {
		return strings.Count(b.rel, "/") - strings.Count(a.rel, "/")
	})
	for _, f := range files {
		errs = append(errs, s.put(f))
	}
	return errors.Join(errs...)
}

// put puts f back where it was.
func (s snapshot) put(f setupFile) error {
	p := filepath.Join(s.repo, filepath.FromSlash(f.rel))
	fi, err := os.Lstat(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !isNotDir(err) {
		return err
	}
	now := err == nil
	switch {
	case !f.there && now && fi.IsDir():
		_ = os.Remove(p) // a folder with something else in it stays, with that in it
		return nil
	case !f.there && now:
		return os.Remove(p)
	case !f.file:
		return nil // a folder, which is there still, or something Apply doesn't write
	}
	if now && fi.IsDir() {
		_ = os.Remove(p) // where the skill's folder replaced the file; writeFile says why where it is still there
	}
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, f.data) {
		if st, err := os.Stat(p); err == nil && st.Mode().Perm() == f.mode {
			return nil
		}
	}
	if err := writeFile(p, f.data, f.mode); err != nil {
		return err
	}
	return os.Chmod(p, f.mode) // a file rewritten keeps the mode Apply gave it
}
