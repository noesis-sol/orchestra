package claude

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/noesis-sol/orchestra/internal/project"
)

// binaryName is the run's own orchestra binary in the main checkout's .orchestra/run/, the one its
// workers' hooks run (RunBinary).
const binaryName = "orchestra-hooks"

// RunBinary puts the binary running, orchestra's, in repo's .orchestra/run/ for the run starting there,
// and returns its path, for the run's Reporter (WithBinary): orchestra update renames a new release
// over orchestra's own path, which would have the run's workers' hooks run the new release from then
// on, writing records for the run's readers in what may be another format. The file is a hard link
// to the binary, or a copy where it can't be one (on another file system), which a rename over
// orchestra's path leaves as it is. A run's start replaces the one an earlier run left, so the workers
// it adopts write for its readers too.
func RunBinary(repo string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding orchestra's binary for the workers' hooks: %w", err)
	}
	return placeBinary(repo, exe, os.Link)
}

// placeBinary is RunBinary of the binary exe, linked with link.
func placeBinary(repo, exe string, link func(oldname, newname string) error) (string, error) {
	if file, err := filepath.EvalSymlinks(exe); err == nil { // the file, not a link to it, as Homebrew's
		exe = file
	}
	root, err := project.OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // closed after the rename that puts the file in place
	rel, tmp := project.RunPath(binaryName), project.RunPath(binaryName+"."+strconv.Itoa(os.Getpid())+".tmp")
	if err := project.RemoveRun(root, repo, tmp); err != nil { // a run of this process ID that stopped here
		return "", err
	}
	if err := link(exe, filepath.Join(repo, tmp)); err != nil {
		if err := copyBinary(root, exe, tmp); err != nil {
			_ = root.Remove(tmp) // what was copied of it, if anything: the error says what failed
			return "", project.RunError(repo, tmp, err)
		}
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp) // the link or copy that couldn't be put in place
		return "", project.RunError(repo, rel, err)
	}
	return filepath.Join(repo, rel), nil
}

// copyBinary copies the binary exe to the new file rel in root, executable.
func copyBinary(root *os.Root, exe, rel string) error {
	in, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only: nothing to lose
	out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// WithBinary is r with the workers' hooks running bin, the run's own binary (RunBinary), rather than
// the path orchestra runs from.
func (r Reporter) WithBinary(bin string) Reporter {
	r.bin = bin
	return r
}
