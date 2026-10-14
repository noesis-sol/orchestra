package organ

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A report that is empty, or cut off at the model's token limit, is an error, not the report.
func TestReviewRefusesAnEmptyOrCutOffReport(t *testing.T) {
	for name, c := range map[string]struct{ out, want string }{
		"empty":   {`{"type":"result","subtype":"success","is_error":false,"result":"","stop_reason":"end_turn"}`, "empty report"},
		"blank":   {`{"type":"result","subtype":"success","is_error":false,"result":" \n","stop_reason":"end_turn"}`, "empty report"},
		"cut off": {`{"type":"result","subtype":"success","is_error":false,"result":"## Finished\n- orch","stop_reason":"max_tokens"}`, "cut the report off"},
	} {
		bin := answers(t, c.out, 0)
		report, err := Client{Bin: bin}.Review(context.Background(), "evidence")
		if err == nil || !strings.Contains(err.Error(), c.want) || report != "" {
			t.Errorf("%s: got %q, %v; want an error with %q", name, report, err, c.want)
		}
	}
	bin := answers(t, `{"type":"result","subtype":"success","is_error":false,"result":"The run ended.","stop_reason":"end_turn"}`, 0)
	if report, err := (Client{Bin: bin}).Review(context.Background(), "evidence"); err != nil || report != "The run ended." {
		t.Errorf("a whole report: got %q, %v", report, err)
	}
}

// Without the schema, an organ's JSON is read out of a fence or the prose around it.
func TestOrganAnswersReadJSONWrappedInText(t *testing.T) {
	answer := `{"cause":"problem","confidence":"low","summary":"s","recommendation":"r"}`
	for name, text := range map[string]string{
		"bare":            answer,
		"json fence":      "```json\n" + answer + "\n```",
		"plain fence":     "```\n" + answer + "\n```\n",
		"fence in prose":  "Here is the verdict:\n\n```json\n" + answer + "\n```\n\nHope this helps.",
		"prose":           "Verdict: " + answer + " (that is all).",
		"braces in prose": "{Note} first, then " + answer,
	} {
		if v, err := parseTriage(Result{Result: text}); err != nil || v.Cause != "problem" {
			t.Errorf("%s: got %+v, %v", name, v, err)
		}
	}
	fenced, _ := json.Marshal("```json\n" + goodPlan + "\n```")
	r := cliOutput(t, `{"type":"result","is_error":false,"result":`+string(fenced)+`}`)
	if p, err := parsePlan(r, planTracked); err != nil || p.Epic.Title != "JSON output" {
		t.Errorf("fenced plan: got %+v, %v", p, err)
	}
	for _, text := range []string{"no JSON here", "```json\n{\"cause\":\n```", ""} {
		if v, err := parseTriage(Result{Result: text}); err == nil || !strings.Contains(err.Error(), "unreadable triage") {
			t.Errorf("%q: got %+v, %v; want an unreadable triage", text, v, err)
		}
	}
}

// scripts/check.sh is found however the scout's command runs it, so it isn't added a second time.
func TestRunsCheckScriptInEveryForm(t *testing.T) {
	for command, want := range map[string]bool{
		"$PWD/scripts/check.sh":                  true,
		"${PWD}/scripts/check.sh":                true,
		"$(pwd)/scripts/check.sh":                true,
		`bash -c "scripts/check.sh"`:             true,
		"sh -c 'scripts/check.sh'":               true,
		"scripts/check.sh && make lint":          true,
		"make build;scripts/check.sh":            true,
		"(cd . && ./scripts/check.sh)":           true,
		"/repo/scripts/check.sh":                 true,
		"other/scripts/check.sh":                 false,
		"./other/scripts/check.sh":               false,
		`bash -c "scripts/check.sh.bak && lint"`: false,
		"scripts/check-fast.sh":                  false,
	} {
		if got := runsCheckScript(command); got != want {
			t.Errorf("runsCheckScript(%q) = %v, want %v", command, got, want)
		}
	}
	suites := checkScriptFirst([]Suite{{Name: "vet", Command: "go vet ./..."},
		{Name: "check", Command: "scripts/check.sh && make lint"}}, true)
	if len(suites) != 2 || suites[0].Command != "scripts/check.sh && make lint" || suites[0].Tier != TierFast {
		t.Errorf("got %+v, want the scout's check suite first and no second one", suites)
	}
}
