package organ

import (
	"context"
	"testing"
)

// An ok verdict's reason is never shown, so an ok without one passes; reject and unclear without one
// are still errors, since their reason is what the user is told.
func TestScreenNeedsAReasonOnlyToStop(t *testing.T) {
	for _, output := range []string{
		`{"is_error":false,"structured_output":{"verdict":"ok","reason":""}}`,
		`{"is_error":false,"structured_output":{"verdict":"ok","reason":"  "}}`,
		`{"is_error":false,"structured_output":{"verdict":"ok"}}`,
	} {
		bin, _ := fakeClaude(t, output)
		s, err := Client{Bin: bin}.Screen(context.Background(), Request{Text: "add a flag", Repo: "r"})
		if err != nil || s.Verdict != ScreenOK || s.Reason != "" {
			t.Errorf("%s: got %+v, %v", output, s, err)
		}
	}
	for _, v := range []ScreenVerdict{ScreenReject, ScreenUnclear} {
		for _, reason := range []string{`,"reason":""`, `,"reason":" "`, ``} {
			output := `{"is_error":false,"structured_output":{"verdict":"` + string(v) + `"` + reason + `}}`
			bin, _ := fakeClaude(t, output)
			if s, err := (Client{Bin: bin}).Screen(context.Background(), Request{Text: "t"}); err == nil {
				t.Errorf("%s: want an error, got %+v", output, s)
			}
		}
	}
}
