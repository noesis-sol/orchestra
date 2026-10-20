package tmux

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// tmux before 3.0 is too old; a version tmux -V doesn't give is not taken for one.
func TestTooOld(t *testing.T) {
	for out, want := range map[string]bool{
		"tmux 1.8\n": true, "tmux 2.9a\n": true, "tmux 3.0\n": false, "tmux 3.0a\n": false, "tmux 3.4\n": false,
		"tmux next-3.9\n": false, "tmux 10.1\n": false, "tmux master\n": false, "": false,
	} {
		if got := tooOld(out); got != want {
			t.Errorf("tooOld(%q) = %v, want %v", out, got, want)
		}
	}
}

// TooOld asks tmux -V, and names a version too old as tmux gives it.
func TestTooOldAsksTmux(t *testing.T) {
	dir := t.TempDir()
	faketool.Write(t, dir, "tmux", "#!/bin/sh\n[ \"$1\" = -V ] && echo 'tmux 2.9a'\n")
	t.Setenv("PATH", faketool.Path(t, dir))
	if got := TooOld(bg); got != "tmux 2.9a" {
		t.Errorf("tmux 2.9a: %q", got)
	}
	dir = t.TempDir()
	faketool.Write(t, dir, "tmux", "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", faketool.Path(t, dir))
	if got := TooOld(bg); got != "" {
		t.Errorf("tmux -V failing: %q", got)
	}
}
