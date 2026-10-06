package organ

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// answers is a fake claude that prints out, as claude prints its result message, then exits with code.
func answers(t *testing.T, out string, code int) string {
	t.Helper()
	bin, _ := fakeScript(t, "cat > /dev/null\ncat <<'JSON'\n"+out+"\nJSON\nexit "+strconv.Itoa(code)+"\n")
	return bin
}

// spendOf runs ask on a Client whose Spent records what it is told.
func spendOf(t *testing.T, bin string, ask func(Client) error) ([]Spend, error) {
	t.Helper()
	var spent []Spend
	err := ask(Client{Bin: bin, Spent: func(s Spend) { spent = append(spent, s) }})
	return spent, err
}

// A successful result gives its subtype, stop reason, cost, turns and session, and Spent hears of them.
func TestResultReadsTheCallsAccounting(t *testing.T) {
	bin := answers(t, `{"type":"result","subtype":"success","is_error":false,"result":"ok","stop_reason":"end_turn",`+
		`"total_cost_usd":0.0125,"num_turns":2,"session_id":"s-1","usage":{"input_tokens":3},`+
		`"modelUsage":{"claude-haiku-4-5":{"costUSD":0.0125}}}`, 0)
	var r Result
	spent, err := spendOf(t, bin, func(g Client) (err error) {
		r, err = g.Ask(context.Background(), "triage", time.Minute, "low", "s", "i", "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Subtype != SubtypeSuccess || r.StopReason != StopEndTurn || r.CostUSD != 0.0125 || r.Turns != 2 ||
		r.SessionID != "s-1" || r.Result != "ok" {
		t.Errorf("result %+v", r)
	}
	if len(spent) != 1 || spent[0].Organ != "triage" || spent[0].CostUSD != 0.0125 || spent[0].Turns != 2 ||
		spent[0].Session != "s-1" || spent[0].Subtype != SubtypeSuccess {
		t.Fatalf("spent %+v", spent)
	}
	if s := spent[0].String(); !strings.HasPrefix(s, "triage: $0.0125 in 2 turns, ") ||
		!strings.HasSuffix(s, ", session s-1") {
		t.Errorf("spend reads %q", s)
	}
}

// An older claude's result, with is_error and result alone, still works: its accounting reads as zero.
func TestResultFromAnOlderCLI(t *testing.T) {
	bin := answers(t, `{"type":"result","is_error":false,"result":"ok"}`, 0)
	var r Result
	spent, err := spendOf(t, bin, func(g Client) (err error) {
		r, err = g.Ask(context.Background(), "review", time.Minute, "low", "s", "i", "")
		return err
	})
	if err != nil || r.Result != "ok" || r.Subtype != "" || r.CostUSD != 0 || r.Turns != 0 {
		t.Fatalf("got %+v, %v", r, err)
	}
	if len(spent) != 1 || !strings.HasPrefix(spent[0].String(), "review: $0.0000 in 0 turns, ") {
		t.Errorf("spent %+v", spent)
	}
}

// Each error subtype is a *CallError naming it, with what claude said of it, whether claude exits 1 after
// it, as it does, or 0; Spent hears of the cost all the same.
func TestErrorSubtypesAreTypedErrors(t *testing.T) {
	for _, c := range []struct {
		subtype Subtype
		errors  string
		want    string
	}{
		{SubtypeMaxTurns, `["Reached maximum number of turns (1)"]`,
			"reached its turn limit: Reached maximum number of turns (1)"},
		{SubtypeMaxBudget, `["Reached maximum budget ($0.000001)"]`,
			"reached its budget limit: Reached maximum budget ($0.000001)"},
		{SubtypeDuringExecution, `["API Error: 500", "retries exhausted"]`,
			"failed during the call: API Error: 500; retries exhausted"},
		{SubtypeSchemaRetries, `[]`, "gave no answer that matches the schema"},
		{"error_something_new", `["?"]`, "reported an error (error_something_new): ?"},
	} {
		for _, code := range []int{1, 0} {
			// As claude 2.1.291 prints it: no result, its errors in errors.
			bin := answers(t, `{"type":"result","subtype":"`+string(c.subtype)+`","is_error":true,`+
				`"stop_reason":"end_turn","total_cost_usd":0.0008,"num_turns":1,"session_id":"s-2",`+
				`"errors":`+c.errors+`}`, code)
			spent, err := spendOf(t, bin, func(g Client) error {
				_, err := g.Ask(context.Background(), "screen", time.Minute, "low", "s", "i", "")
				return err
			})
			e, ok := errors.AsType[*CallError](err)
			if !ok {
				t.Errorf("%s, exit %d: error %v (%T), want a *CallError", c.subtype, code, err, err)
				continue
			}
			if e.Subtype != c.subtype || e.Refused() || err.Error() != bin+" "+c.want {
				t.Errorf("%s, exit %d: error %q (%+v), want %q", c.subtype, code, err, e, bin+" "+c.want)
			}
			if len(spent) != 1 || spent[0].CostUSD != 0.0008 || spent[0].Subtype != c.subtype ||
				!strings.Contains(spent[0].String(), ", "+string(c.subtype)+", session s-2") {
				t.Errorf("%s, exit %d: spent %+v", c.subtype, code, spent)
			}
		}
	}
}

// A refusal is a *CallError that says so, even reported as a success, and an organ's error is one too.
func TestARefusalIsATypedError(t *testing.T) {
	for _, out := range []string{
		`{"type":"result","subtype":"success","is_error":false,"result":"I can't help with that.",` +
			`"stop_reason":"refusal","total_cost_usd":0.001,"num_turns":1}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"stop_reason":"refusal",` +
			`"errors":["I can't help with that."]}`,
	} {
		bin := answers(t, out, 0)
		_, err := Client{Bin: bin}.Screen(context.Background(), Request{Text: "x", Repo: "r"})
		e, ok := errors.AsType[*CallError](err)
		if !ok || !e.Refused() || err.Error() != bin+" refused to answer: I can't help with that." {
			t.Errorf("error %v (%T), want a refusal", err, err)
		}
	}
}

// An error result claude gives no subtype or errors for says what its result says, as before.
func TestAnErrorWithoutASubtypeSaysItsResult(t *testing.T) {
	bin := answers(t, `{"type":"result","is_error":true,"result":"usage limit reached"}`, 1)
	_, err := Client{Bin: bin}.Ask(context.Background(), "plan", time.Minute, "low", "s", "i", "")
	if _, ok := errors.AsType[*CallError](err); !ok || err.Error() != bin+" reported an error: usage limit reached" {
		t.Errorf("error %v (%T)", err, err)
	}
	bin = answers(t, `{"type":"result","is_error":true}`, 1)
	_, err = Client{Bin: bin}.Ask(context.Background(), "plan", time.Minute, "low", "s", "i", "")
	if err == nil || err.Error() != bin+" reported an error" {
		t.Errorf("error %v, want no empty tail", err)
	}
}

// A claude that exits 1 with no result message fails as the command it is, its stderr said.
func TestAFailureWithoutAResultIsTheCommands(t *testing.T) {
	bin, _ := fakeScript(t, "cat > /dev/null\necho '{\"type\":\"assistant\"}'\necho 'boom' >&2\nexit 1\n")
	spent, err := spendOf(t, bin, func(g Client) error {
		_, err := g.Ask(context.Background(), "plan", time.Minute, "low", "s", "i", "")
		return err
	})
	if _, ok := errors.AsType[*CallError](err); ok || err == nil || err.Error() != bin+": exit status 1: boom" {
		t.Errorf("error %v (%T), want the command's", err, err)
	}
	if len(spent) != 0 {
		t.Errorf("spent %+v, want nothing: claude said no cost", spent)
	}
}

// The scout's error names the error subtype through its *ScoutError.
func TestTheScoutsErrorKeepsTheCallError(t *testing.T) {
	bin := answers(t, `{"type":"result","subtype":"error_max_budget_usd","is_error":true,`+
		`"errors":["Reached maximum budget ($0.5)"]}`, 1)
	_, err := Client{Bin: bin}.scout(context.Background(), time.Minute, t.TempDir())
	if failure := scoutFailureOf(t, err); failure != ScoutOverBudget {
		t.Errorf("failure %v, want ScoutOverBudget", failure)
	}
	if e, ok := errors.AsType[*CallError](err); !ok || e.Subtype != SubtypeMaxBudget {
		t.Errorf("error %v, want the budget's *CallError inside", err)
	}
}
