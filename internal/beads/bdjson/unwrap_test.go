package bdjson

import "testing"

// Unwrap reads bd's output bare or in its envelope, and only an object with both schema_version
// and data is the envelope.
func TestUnwrapTakesTheEnvelopeOnlyWithItsVersion(t *testing.T) {
	for raw, want := range map[string]string{
		`{"schema_version":1,"data":[{"id":"k-1"}]}`:       `[{"id":"k-1"}]`,
		` {"data":{"count":3},"schema_version":2} ` + "\n": `{"count":3}`,
		`{"schema_version":1,"data":null}`:                 `null`,
		`[{"id":"k-1"}]`:                                   `[{"id":"k-1"}]`,
		`{"id":"k-1","data":"its own"}`:                    `{"id":"k-1","data":"its own"}`,
		`{"schema_version":1}`:                             `{"schema_version":1}`,
		`not json`:                                         `not json`,
		"\n":                                               ``,
	} {
		if got := string(Unwrap([]byte(raw))); got != want {
			t.Errorf("Unwrap(%q) = %q, want %q", raw, got, want)
		}
	}
}
