package claude

import "testing"

// Claude Code 2.1.302's screen on the grace path (see the comment above limitWords), captured on 2026-10-17
// in a tmux pane 120 columns wide, its blank lines left out and its folder shortened: a session signed in to
// a Max plan whose requests went to a fake API, which answered "list the folder" with a call of ls and then
// with the same words each time, and the headers that arm the grace note. While the turn the note was
// added to goes on, once that turn has ended, and once the limit has reset and its automatic continue has
// started a turn.
const (
	screenGraceWrappingUp = " ▐▛███▛█   Claude Code v2.1.302\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude Max\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"  Get to finished work sooner with Opus 5.5. Switch anytime with /model.\n" +
		"❯ list the folder\n" +
		"⏺ Listing the folder.\n" +
		"⏺ Listing 1 directory… (ctrl+o to expand)\n" +
		"  ⎿  $ ls\n" +
		"✶ Crafting… (5s · ↓ 20 tokens)\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"  ⚠ Usage limit reached · wrapping up · limit resets 2:50pm · clau.de/wrap-up · /upgrade to keep using Claude Code\n" +
		"  ⏸ manual mode on · esc to interrupt\n"
	screenGraceStopped = " ▐▛███▛█   Claude Code v2.1.302\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude Max\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"  Get to finished work sooner with Opus 5.5. Switch anytime with /model.\n" +
		"❯ list the folder\n" +
		"⏺ Listing the folder.\n" +
		"  Listed 1 directory (ctrl+o to expand)\n" +
		"⏺ Usage limit reached: I listed the folder, and that was all there was to do. Nothing is left.\n" +
		"⏺ Usage limit reached · continuing automatically at 2:50pm · esc to cancel\n" +
		"✻ Churned for 16s · done 11:34 AM\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"  ⚠ Usage limit reached · limit resets 2:50pm · clau.de/wrap-up\n" +
		"    Continuing automatically at 2:50pm · esc to cancel\n" +
		"  ⏸ manual mode on · ? for shortcuts\n"
	screenGraceContinued = " ▐▛███▛█   Claude Code v2.1.302\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude Max\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"  Sonnet 5.5 is here, and it's faster and excels at well-scoped tasks. Opus 5.5 is still your default. Switch with\n" +
		"  /model.\n" +
		"❯ list the folder\n" +
		"⏺ Listing the folder.\n" +
		"  Listed 1 directory (ctrl+o to expand)\n" +
		"⏺ Usage limit reached: I listed the folder, and that was all there was to do. Nothing is left.\n" +
		"⏺ Usage limit reached · continuing automatically at 11:35am · esc to cancel\n" +
		"✻ Cooked for 1s · done 11:34 AM\n" +
		"⏺ Usage limit reset · continuing automatically\n" +
		"⏺ Listing the folder.\n" +
		"  Listed 1 directory (ctrl+o to expand)\n" +
		"⏺ Usage limit reached: I listed the folder, and that was all there was to do. Nothing is left.\n" +
		"✻ Cooked for 0s · done 11:37 AM\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"  ⏸ manual mode on · ? for shortcuts\n"
)

// On the grace path the screen shows the usage limit once the worker has ended its turn and Claude Code waits
// to continue by itself, not while the worker wraps up, nor once the continue has started a turn.
func TestShowsUsageLimitAfterTheGraceNote(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		screen string
		want   bool
	}{
		"wrapping up":               {screenGraceWrappingUp, false},
		"stopped, waiting to go on": {screenGraceStopped, true},
		"gone on after the reset":   {screenGraceContinued, false},
	} {
		if got := ShowsUsageLimit(c.screen); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	for _, l := range []string{
		"⏺ Usage limit reached · continuing automatically at 2:50pm · esc to cancel",
		"⏺ Usage limit reached · continuing automatically at 11:35am · esc to cancel",
	} {
		if !showsContinue(l) {
			t.Errorf("showsContinue(%q) = false, want true", l)
		}
	}
	for _, l := range []string{
		"  ⚠ Usage limit reached · wrapping up · limit resets 2:50pm · clau.de/wrap-up · /upgrade to keep using Claude Code",
		"  ⚠ Usage limit reached · limit resets 2:50pm · clau.de/wrap-up",
		"    Continuing automatically at 2:50pm · esc to cancel",
		"⏺ Usage limit reset · continuing automatically",
		"⏺ Usage limit reached: I listed the folder, and that was all there was to do. Nothing is left.",
	} {
		if showsContinue(l) {
			t.Errorf("showsContinue(%q) = true, want false", l)
		}
	}
}
