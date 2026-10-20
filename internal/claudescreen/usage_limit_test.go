package claudescreen

import (
	"strings"
	"testing"
)

// Claude Code 2.1.301's screen at the session limit, captured on 2026-10-16 in a tmux pane 120 columns
// wide, its blank lines left out: with its automatic continue, with that off (its dialog), that dialog
// answered "Stop and wait for limit to reset", and the automatic continue's after a prompt sent while
// the limit held.
const (
	screenAutoContinue = " ▐▛███▛█   Claude Code v2.1.301\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude API\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"▎ Auto mode is now Claude Code's default permission mode.\n" +
		"▎ Auto mode lets Claude handle permission prompts automatically. Claude checks each tool call for risky actions and\n" +
		"▎ prompt injection before executing, runs the ones it assesses as lower-risk, and blocks the rest.\n" +
		"▎ https://code.claude.com/docs/en/permission-modes\n" +
		"❯ say hi\n" +
		"  ⎿ \u00a0You've hit your session limit · resets 7:41pm (Europe/Athens)\n" +
		"⏺ Usage limit reached · continuing automatically at 7:41pm · esc to cancel\n" +
		"✻ Cooked for 0s · done 4:42 PM\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"❯\u00a0\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"  ⚠ Usage limit reached · limit resets 7:41pm\n" +
		"    Continuing automatically at 7:41pm · esc to cancel\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"
	screenLimitDialog = " ▐▛███▛█   Claude Code v2.1.301\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude API\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"❯ say hi\n" +
		"  ⎿ \u00a0You've hit your session limit · resets 7:41pm (Europe/Athens)\n" +
		"✻ Brewed for 0s · done 4:42 PM\n" +
		"▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔ ◐ medium · /effort ▔\n" +
		"   What do you want to do?\n" +
		"   ❯ 1. Stop and wait for limit to reset\n" +
		"     2. Wait here, then continue automatically at 7:41pm\n" +
		"     3. Upgrade your plan\n" +
		"   Enter to confirm · Esc to cancel\n"
	screenStoppedToWait = " ▐▛███▛█   Claude Code v2.1.301\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude API\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"❯ say hi\n" +
		"  ⎿ \u00a0You've hit your session limit · resets 7:41pm (Europe/Athens)\n" +
		"✻ Brewed for 0s · done 4:42 PM\n" +
		"                                                                                                    ◐ medium · /effort\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"❯\u00a0\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"
	screenLimitedAgain = " ▐▛███▛█   Claude Code v2.1.301\n" +
		"▝▜██████▀  Sonnet 5.5 · Claude API\n" +
		" ▝▝   ▝▝   ~/work\n" +
		"▎ Auto mode is now Claude Code's default permission mode.\n" +
		"▎ Auto mode lets Claude handle permission prompts automatically. Claude checks each tool call for risky actions and\n" +
		"▎ prompt injection before executing, runs the ones it assesses as lower-risk, and blocks the rest.\n" +
		"▎ https://code.claude.com/docs/en/permission-modes\n" +
		"❯ say hi\n" +
		"  ⎿ \u00a0You've hit your session limit · resets 7:41pm (Europe/Athens)\n" +
		"⏺ Usage limit reached · continuing automatically at 7:41pm · esc to cancel\n" +
		"✻ Cooked for 0s · done 4:42 PM\n" +
		"❯ continue\n" +
		"⏺ Usage limit reached again · continuing automatically at 7:41pm · esc to cancel\n" +
		"  ⎿ \u00a0You've hit your session limit · resets 7:41pm (Europe/Athens)\n" +
		"✻ Worked for 0s · done 4:48 PM\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"❯\u00a0\n" +
		"────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n" +
		"  ⚠ Usage limit reached · limit resets 7:41pm\n" +
		"    Continuing automatically at 7:41pm · esc to cancel\n" +
		"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"
)

// The screen shows the usage limit while Claude Code's words of it are its last: whether it waits to
// continue by itself, asks what to do, or was told to stop and wait; not once the session went on.
func TestShowsUsageLimit(t *testing.T) {
	t.Parallel()
	wentOn := strings.Replace(screenStoppedToWait, "✻ Brewed for 0s · done 4:42 PM\n", "✻ Brewed for 0s · done 4:42 PM\n"+
		"❯ Your claude.ai usage limit has reset. Continue the task you were working on when the limit was reached; "+
		"do not repeat work that is already complete.\n"+
		"⏺ Picking the ticket up where I left it.\n"+
		"✻ Worked for 2m 3s · done 7:44 PM\n", 1)
	otherLimit := strings.Replace(screenStoppedToWait, "You've hit your session limit · resets 7:41pm (Europe/Athens)",
		"API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited", 1)
	for name, c := range map[string]struct {
		screen string
		want   bool
	}{
		"continuing by itself":                 {screenAutoContinue, true},
		"asking what to do":                    {screenLimitDialog, true},
		"stopped to wait":                      {screenStoppedToWait, true},
		"limited again":                        {screenLimitedAgain, true},
		"gone on after the reset":              {wentOn, false},
		"refused for another limit":            {otherLimit, false},
		"no screen":                            {"", false},
		"a dialog that runs no hook, no limit": {"   Do you trust the files in this folder?\n   ❯ 1. Yes, proceed\n", false},
		"the worker's message quoting the words": {"❯ say hi\n⏺ Claude Code then says: You've hit your session limit · " +
			"resets 7pm (Europe/Athens)\n  You've hit your session limit · resets 7pm (Europe/Athens)\n", false},
	} {
		if got := ShowsUsageLimit(c.screen); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// Claude Code's notice that it continues by itself once the limit resets is the screen's sign of a turn
// that ended normally at the limit, where no words of the limit show under the prompt; not once the session
// went on.
func TestScreenShowsTheLimitAfterTheGraceNote(t *testing.T) {
	t.Parallel()
	ended := "❯ Work on the ticket\n" +
		"⏺ Your usage limit was reached partway through the ticket, so I stopped and saved the work.\n" +
		"⏺ Usage limit reached · continuing automatically at 5:30am · esc to cancel\n" +
		"✻ Worked for 21m 3s\n" +
		"────────────────────────────────────────\n" +
		"❯ \n" +
		"────────────────────────────────────────\n" +
		"  ⚠ Usage limit reached · limit resets 5:30am\n" +
		"    Continuing automatically at 5:30am · esc to cancel\n"
	wentOn := strings.Replace(ended, "✻ Worked for 21m 3s\n", "✻ Worked for 21m 3s\n"+
		"⏺ Usage limit reset · continuing automatically\n"+
		"❯ Your claude.ai usage limit has reset. Continue the task you were working on when the limit was reached; "+
		"do not repeat work that is already complete.\n"+
		"⏺ Picking the ticket up where I left it.\n", 1)
	quoted := "❯ Work on the ticket\n⏺ Claude Code may say: Usage limit reached · continuing automatically at 5:30am\n"
	for name, c := range map[string]struct {
		screen string
		want   bool
	}{
		"waiting to continue by itself": {ended, true},
		"gone on after the reset":       {wentOn, false},
		"a message quoting the notice":  {quoted, false},
	} {
		if got := ShowsUsageLimit(c.screen); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
