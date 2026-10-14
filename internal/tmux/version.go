package tmux

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// MinVersion is the oldest tmux orchestra works with: 3.0 brought set-option -p, which sets the
// options orchestra keeps on a worker's pane, and respawn-pane -e, which starts the worker with its
// environment.
const MinVersion = "3.0"

// versionPattern is the major and minor version in what tmux -V answers: tmux 3.4, tmux 3.3a, tmux next-3.9.
var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// TooOld returns tmux's version, as tmux -V gives it, when it is older than MinVersion, and "" when
// it is new enough or doesn't say: tmux -V failing, or a build without a version number (tmux
// master), is given the benefit of the doubt.
func TooOld(ctx context.Context) string {
	out, err := command.Output(ctx, command.ReadLimit, "", "tmux", "-V")
	if err != nil || !tooOld(out) {
		return ""
	}
	return strings.TrimSpace(out)
}

// tooOld reports whether tmux -V's answer is a version older than MinVersion, 3.0.
func tooOld(out string) bool {
	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return false
	}
	major, err := strconv.Atoi(m[1])
	return err == nil && major < 3
}

// KeepsNames reports whether tmux keeps a session name holding '.' or ':' as it is given, as tmux
// 3.8 and later do; before, the session is the one SessionName names. tmux -V failing, or a build
// without a version number (tmux master), is taken for a new tmux.
func KeepsNames(ctx context.Context) bool {
	out, err := command.Output(ctx, command.ReadLimit, "", "tmux", "-V")
	return err != nil || keepsNames(out)
}

// keepsNames reports whether tmux -V's answer is a version that keeps names as they are given: 3.8
// or later, or none.
func keepsNames(out string) bool {
	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return true
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return true
	}
	minor, err := strconv.Atoi(m[2])
	return err != nil || major > 3 || major == 3 && minor >= 8
}
