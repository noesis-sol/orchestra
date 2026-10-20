package tracker

import (
	"encoding/json"
	"testing"
)

// MetadataField reads a key from the metadata as bd gives it: an object, or one encoded as a string.
func TestMetadataFieldReadsAnObjectOrOneInAString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, meta, key, want string
	}{
		{"an object", `{"fork": "abc1234", "files": ["a.go"]}`, "fork", `"abc1234"`},
		{"an object in a string", `"{\"fork\": \"abc1234\"}"`, "fork", `"abc1234"`},
		{"a list", `{"files": ["a.go", "b.go"]}`, "files", `["a.go", "b.go"]`},
		{"no such key", `{"files": ["a.go"]}`, "fork", ""},
		{"no metadata", ``, "fork", ""},
		{"null", `null`, "fork", ""},
		{"not an object", `["fork"]`, "fork", ""},
		{"a string that isn't an object", `"fork"`, "fork", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Ticket{Metadata: json.RawMessage(tc.meta)}).MetadataField(tc.key); string(got) != tc.want {
				t.Errorf("MetadataField(%q) of %s = %s, want %s", tc.key, tc.meta, got, tc.want)
			}
		})
	}
}
