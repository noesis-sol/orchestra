package organ

import (
	"encoding/json"
	"strings"
	"testing"
)

// cliOutput reads the claude CLI's JSON output as ask does.
func cliOutput(t *testing.T, out string) Result {
	t.Helper()
	var r Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// Every organ's answer falls back to the JSON in result when structured_output is absent or null.
func TestOrganAnswersFallBackToTheResult(t *testing.T) {
	for name, c := range map[string]struct {
		answer, want string
		parse        func(Result) (string, error)
	}{
		"triage": {`{"cause":"problem","confidence":"low","summary":"s","recommendation":"r"}`, "problem",
			func(r Result) (string, error) { v, err := parseTriage(r); return v.Cause, err }},
		"prediction": {`{"files":["a.go"]}`, "a.go",
			func(r Result) (string, error) {
				f, err := parsePrediction(r, []string{"a.go"})
				return strings.Join(f, ","), err
			}},
	} {
		result, _ := json.Marshal(c.answer)
		for how, structured := range map[string]string{"absent": "", "null": `,"structured_output":null`} {
			r := cliOutput(t, `{"type":"result","is_error":false,"result":`+string(result)+structured+`}`)
			if got, err := c.parse(r); err != nil || got != c.want {
				t.Errorf("%s with structured_output %s: got %q, %v; want %q", name, how, got, err, c.want)
			}
		}
	}
}

// A null structured_output with nothing in result is unreadable, not an answer of zero values.
func TestANullAnswerIsUnreadable(t *testing.T) {
	r := cliOutput(t, `{"type":"result","is_error":false,"result":"","structured_output":null}`)
	if v, err := parseTriage(r); err == nil || !strings.Contains(err.Error(), "unreadable triage") {
		t.Errorf("got %+v, %v; want an unreadable triage", v, err)
	}
}

// Without the schema, a triage verdict's confidence is still one of its values.
func TestTriageNeedsAKnownConfidence(t *testing.T) {
	for _, confidence := range []string{`"certain"`, `""`, `null`, `"High"`} {
		answer := `{"cause":"problem","confidence":` + confidence + `,"summary":"s","recommendation":"r"}`
		if v, err := parseTriage(Result{Result: answer}); err == nil || !strings.Contains(err.Error(), "unknown triage confidence") {
			t.Errorf("confidence %s: got %+v, %v; want an unknown confidence", confidence, v, err)
		}
	}
	answer := `{"cause":"problem","summary":"s","recommendation":"r"}`
	if v, err := parseTriage(Result{Result: answer}); err == nil {
		t.Errorf("no confidence: got %+v; want an error", v)
	}
	for _, confidence := range []string{"high", "medium", "low"} {
		answer := `{"cause":"problem","confidence":"` + confidence + `","summary":"s","recommendation":"r"}`
		if v, err := parseTriage(Result{Result: answer}); err != nil || v.Confidence != confidence {
			t.Errorf("confidence %s: got %+v, %v", confidence, v, err)
		}
	}
}
