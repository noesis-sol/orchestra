package project

import "testing"

func TestInstallTimeOutNamesTheLimit(t *testing.T) {
	if got, want := errInstallTimedOut.Error(), "timed out after 15m"; got != want {
		t.Errorf("an install at its time limit reads %q, want %q", got, want)
	}
}
