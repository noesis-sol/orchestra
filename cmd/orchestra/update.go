package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/update"
)

// exitUpdateAvailable is orchestra update --check's exit code when the latest release would replace
// this build.
const exitUpdateAvailable = 1

// updateSynopsis is orchestra update's flags, as its usage line gives them (see subcommand).
const updateSynopsis = "[--check] [--force]"

// runUpdateHere is orchestra update on the binary running, of this build's version. It returns the exit code.
func runUpdateHere(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "orchestra update: cannot find this binary:", err)
		return dispatch.ExitSetup
	}
	return runUpdate(ctx, args, buildVersion(), exe, update.Updater{}, stdout, stderr)
}

// runUpdate replaces the binary at exe, orchestra of version current, with the latest release's, which
// u reads and installs. It returns the exit code.
//
// A run going doesn't hold it off: nothing runs the binary during a run (workers' hooks are shell, the
// organs are claude), and Install renames the new one over exe, so a run keeps the build it started
// with and the next starts the new one.
func runUpdate(ctx context.Context, args []string, current, exe string, u update.Updater,
	stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "only say whether a newer release is out; change nothing")
	force := fs.Bool("force", false, "replace a build from a clone (dev, a pseudo-version) or a newer one too")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usageLines("Usage: ", "update", updateSynopsis)+"\n"+
			"Replace this orchestra with the latest GitHub Release's binary, its SHA-256 checked against the\n"+
			"release's checksums.txt. A build from a clone is replaced only with --force. A run going\n"+
			"keeps the build it started with; the next run starts the new one.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 updated or nothing to do (--check: none newer), "+
			"1 --check: a newer release is out,\n2 refused (flags, a build from a clone, "+
			"a folder it can't write in), 4 the update failed.\n")
	}
	if code, ok := parseArgs(fs, args, "update"); !ok {
		return code
	}

	rel, err := u.Latest(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra update:", err)
		return dispatch.ExitTool
	}
	v := verdictOf(current, rel.Tag)
	if *check {
		switch {
		case v.latest:
			fmt.Fprintf(stdout, "orchestra %s is the latest release.\n", current)
		case v.newer:
			fmt.Fprintf(stdout, "orchestra %s is newer than the latest release, %s.\n", current, rel.Tag)
		case !v.clone:
			fmt.Fprintf(stdout, "orchestra %s; the latest release is %s (%s): orchestra update installs it.\n",
				current, rel.Tag, rel.URL)
			return exitUpdateAvailable
		default:
			fmt.Fprintf(stdout, "orchestra %s is a build from a clone; the latest release is %s (%s): "+
				"orchestra update --force installs it.\n", current, rel.Tag, rel.URL)
			if v.behind {
				return exitUpdateAvailable
			}
		}
		return dispatch.ExitOK
	}
	switch {
	case v.latest:
		fmt.Fprintf(stdout, "orchestra %s is the latest release already.\n", current)
		return dispatch.ExitOK
	case v.clone && !*force:
		fmt.Fprintf(stderr, "orchestra update: orchestra %s at %s is a build from a clone, not a release. "+
			"orchestra update --force replaces it with %s.\n", current, exe, rel.Tag)
		return dispatch.ExitSetup
	case v.newer && !*force:
		fmt.Fprintf(stdout, "orchestra %s is newer than the latest release, %s: nothing to do "+
			"(orchestra update --force installs %s).\n", current, rel.Tag, rel.Tag)
		return dispatch.ExitOK
	}

	fmt.Fprintf(stdout, "Downloading orchestra %s for %s…\n", rel.Tag, u.Platform())
	path, err := u.Install(ctx, rel, exe)
	if path == "" {
		path = exe
	}
	if notWritable, ok := errors.AsType[*update.NotWritableError](err); ok {
		fmt.Fprintf(stderr, "orchestra update: cannot replace %s: %v\n"+
			"Run it as the folder's owner instead: sudo orchestra update\n", path, notWritable)
		return dispatch.ExitSetup
	}
	if err != nil {
		fmt.Fprintf(stderr, "orchestra update: %v\n%s is unchanged.\n", err, path)
		return dispatch.ExitTool
	}
	fmt.Fprintf(stdout, "Updated orchestra %s -> %s (%s)\nRelease notes: %s\n", current, rel.Tag, path, rel.URL)
	return dispatch.ExitOK
}

// verdict is how a build stands against the latest release, which --check reports and orchestra
// update acts on.
type verdict struct {
	latest bool // it is the latest release
	newer  bool // it ranks above the latest release
	behind bool // the latest release ranks above it, or it has no version to rank (dev)
	clone  bool // it is a build from a clone (dev, a pseudo-version, +dirty), not a release
}

// verdictOf is how the build of version current stands against the latest release, tag.
func verdictOf(current, tag string) verdict {
	order, versioned := update.Compare(current, tag)
	return verdict{
		latest: current == tag,
		newer:  versioned && order > 0,
		behind: !versioned || order < 0,
		clone:  !update.IsRelease(current),
	}
}
