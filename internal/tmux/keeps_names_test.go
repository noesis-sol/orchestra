package tmux

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// tmux 3.8 and later keep a session name holding '.' or ':'; a version tmux -V doesn't give is taken
// for one that does.
func TestKeepsNames(t *testing.T) {
	for out, want := range map[string]bool{
		"tmux 3.0\n": false, "tmux 3.3a\n": false, "tmux 3.6\n": false, "tmux 3.7b\n": false, "tmux 3.8\n": true,
		"tmux next-3.8\n": true, "tmux 3.10\n": true, "tmux 4.0\n": true, "tmux master\n": true, "": true,
	} {
		if got := keepsNames(out); got != want {
			t.Errorf("keepsNames(%q) = %v, want %v", out, got, want)
		}
	}
}

// KeepsNames asks tmux -V, and takes tmux -V failing for a new tmux.
func TestKeepsNamesAsksTmux(t *testing.T) {
	dir := t.TempDir()
	faketool.Write(t, dir, "tmux", "#!/bin/sh\n[ \"$1\" = -V ] && echo 'tmux 3.4'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if KeepsNames(bg) {
		t.Error("tmux 3.4: keeps names")
	}
	dir = t.TempDir()
	faketool.Write(t, dir, "tmux", "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if !KeepsNames(bg) {
		t.Error("tmux -V failing: doesn't keep names")
	}
}
