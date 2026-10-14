package main

import (
	"crypto/ed25519"
	"path/filepath"
	"testing"
)

// A release not signed with a key the binary trusts isn't installed, and says so.
func TestUpdateRefusesAnUntrustedSignature(t *testing.T) {
	u, _ := releaseServer(t, "v0.6.0", false)
	other, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	u.Keys = []ed25519.PublicKey{other}
	exe := oldBinary(t)

	code, _, errOut := updateIn(t, "v0.5.1", exe, u)
	resolved, _ := filepath.EvalSymlinks(exe)
	want := "orchestra update: checksums.txt.sig doesn't verify checksums.txt: it isn't signed by a release key\n" +
		resolved + " is unchanged.\n"
	if code != 4 || errOut != want {
		t.Errorf("exit %d, stderr:\n%s\nwant 4,\n%s", code, errOut, want)
	}
	if got := read(t, exe); got != "the old one\n" {
		t.Errorf("the binary was replaced: %q", got)
	}
}
