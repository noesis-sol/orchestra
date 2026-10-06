package organ

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// budgetHelp is the part of Claude Code 2.1.291's --help that lists --max-budget-usd.
const budgetHelp = `Options:
  --max-budget-usd <amount>             Maximum dollar amount to spend on API
                                        calls (only works with --print)
  -p, --print                           Print response and exit (useful for
`

// A claude that lists --max-budget-usd gets the scout's budget; one that doesn't, or whose --help fails,
// gets no budget, which it would reject.
func TestScoutHasABudgetWhereClaudeListsIt(t *testing.T) {
	for name, c := range map[string]struct {
		help   string
		exit   int
		budget bool
	}{
		"lists it":    {budgetHelp, 0, true},
		"with others": {newerHelp + budgetHelp, 0, true},
		"older":       {olderHelp, 0, false},
		"confinement": {newerHelp, 0, false},
		"help fails":  {budgetHelp, 1, false},
		"mentions it": {"  -p, --print   Print response, see --max-budget-usd\n", 0, false},
	} {
		args := scoutArgs(t, c.help, c.exit)
		if got := strings.Contains(args, "\n[--max-budget-usd]\n[2.00]\n"); got != c.budget {
			t.Errorf("%s: --max-budget-usd 2.00 passed = %v, want %v:\n%s", name, got, c.budget, args)
		}
		if !c.budget && strings.Contains(args, "--max-budget-usd") {
			t.Errorf("%s: a budget passed to a claude that doesn't list it:\n%s", name, args)
		}
	}
}

// The other organs make no tool calls to spend on and get no budget.
func TestOtherOrgansHaveNoBudget(t *testing.T) {
	bin, record := fakeHelpClaude(t, budgetHelp, 0)
	if _, err := (Client{Bin: bin}).Ask(context.Background(), "test", hung, "low", "S", "E", ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "--max-budget-usd") {
		t.Errorf("an organ without tools got the scout's budget:\n%s", b)
	}
}

// A scout claude stops at its budget is a ScoutError of its own kind, whose message says so and keeps
// claude's words.
func TestAScoutStoppedAtItsBudgetSaysSo(t *testing.T) {
	bin := answers(t, `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"total_cost_usd":2.04,`+
		`"errors":["Reached maximum budget ($2)"]}`, 1)
	_, err := Client{Bin: bin}.scout(context.Background(), time.Minute, t.TempDir())
	if f := scoutFailureOf(t, err); f != ScoutOverBudget {
		t.Errorf("failure %v, want ScoutOverBudget", f)
	}
	want := "the scout was stopped at its budget of $2.00: " + bin + " reached its budget limit: " +
		"Reached maximum budget ($2)"
	if err.Error() != want {
		t.Errorf("error %q, want %q", err, want)
	}
	if e, ok := errors.AsType[*CallError](err); !ok || e.Subtype != SubtypeMaxBudget {
		t.Errorf("error %v, want the budget's *CallError inside", err)
	}
}

// Another error result is still a plain failure.
func TestOnlyTheBudgetIsOverBudget(t *testing.T) {
	bin := answers(t, `{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["max turns"]}`, 1)
	_, err := Client{Bin: bin}.scout(context.Background(), time.Minute, t.TempDir())
	if f := scoutFailureOf(t, err); f != ScoutFailed {
		t.Errorf("failure %v, want ScoutFailed", f)
	}
}
