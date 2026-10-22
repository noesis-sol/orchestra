package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noesis-sol/orchestra/internal/git"
)

// A spec from outside the repository is copied to SpecsDir under its file name, so two specs of one
// name from different folders would share one copy, and with it one root epic: the second would
// update the first's tree for the whole of itself. The commit that adds or changes a copy records
// where it was copied from, a SpecSourceTrailer line in its message, and a spec from another place
// that would take that copy's name is refused (CheckSpecSource).

// SpecSourceTrailer is the trailer of the commits of a spec's copy that says where it was copied from,
// as SpecSource gives it.
const SpecSourceTrailer = "Spec-Source"

// SpecSource is where file, a spec outside the repository, is copied from, as its copy's commits
// record it: its path, absolute with its symlinks resolved, ~ standing for the user's home folder.
func SpecSource(file string) string {
	full := resolved(file)
	if home, err := os.UserHomeDir(); err == nil {
		if r, err := filepath.Rel(resolved(home), full); err == nil && filepath.IsLocal(r) {
			return "~/" + filepath.ToSlash(r)
		}
	}
	return full
}

// SpecTakenError is the error for a spec from outside the repository, File, whose copy would take
// the path of another's, Path, copied from Source.
type SpecTakenError struct {
	File   string // the spec, as given
	Path   string // its copy's path in the repository, slash-separated (see SpecPath)
	Source string // where the copy at Path was copied from, as its commit records it
}

func (e *SpecTakenError) Error() string {
	return fmt.Sprintf("%s holds the copy of another spec, %s, and a spec from outside the repository is kept "+
		"under its file name: give %s another file name, which its copy takes, or, if it is %s moved, copy it "+
		"over %s and give orchestra --spec that path", e.Path, e.Source, e.File, e.Source, e.Path)
}

// CheckSpecSource checks that file, a spec, can be kept in the repository whose main checkout is repo,
// at SpecPath: it returns *SpecTakenError for one outside the repository whose copy would take the
// path of a spec copied from somewhere else. A spec inside the repository, a new name, or a copy whose
// commits record no source (one made before orchestra recorded them, which file then takes) passes. It
// returns git's error when git can't say.
func CheckSpecSource(ctx context.Context, repo, file string) error {
	rel, inside := SpecPath(repo, file)
	if inside {
		return nil
	}
	was, err := git.Git{}.LastTrailer(ctx, repo, rel, SpecSourceTrailer)
	switch {
	case err != nil:
		return fmt.Errorf("cannot tell where the spec at %s was copied from: %w", rel, err)
	case was != "" && was != SpecSource(file):
		return &SpecTakenError{File: file, Path: rel, Source: was}
	}
	return nil
}

// sourceTrailer is the paragraph that ends the message of a commit of the copy of file, a spec from
// outside the repository: where it was copied from.
func sourceTrailer(file string) string {
	return "\n" + SpecSourceTrailer + ": " + SpecSource(file) + "\n"
}
