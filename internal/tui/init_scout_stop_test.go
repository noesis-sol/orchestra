package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// With TERM=dumb the accessible form waits for the scout before its next question. A stop (Ctrl+C,
// which orchestra turns into the end of init's context) stops the scout too, as it runs claude in its
// own process group, which the terminal's SIGINT doesn't reach: init ends at once rather than when
// the scout's time is up.
func TestAccessibleInitStoppedDuringTheScoutEndsAtOnce(t *testing.T) {
	started := make(chan struct{})
	scout := func(ctx context.Context) (organ.Scouting, error) {
		close(started)
		<-ctx.Done() // only its context stops it
		return organ.Scouting{}, context.Cause(ctx)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var out strings.Builder
		done <- AskInit(ctx, strings.NewReader(""), &out, "dumb", &project.Choice{},
			Ask{Questions: project.Questions{Check: true}, Scout: scout})
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("AskInit = %v; want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskInit still waits for the scout after its context ended")
	}
}
