package dispatch

import "testing"

// A reason is said the first time, again only once it changes, and after forget whatever it is; no
// reason is never said, and clears the last one.
func TestSayOnceSaysAReasonAgainOnlyWhenItChanges(t *testing.T) {
	t.Parallel()
	steps := []struct {
		id, why string
		forget  bool
		want    bool
	}{
		{"a", "waits for b", false, true},
		{"a", "waits for b", false, false},
		{"b", "waits for b", false, true}, // another ticket's reason is its own
		{"a", "waits for c", false, true},
		{"a", "", false, false},
		{"a", "waits for c", false, true}, // said again: it stopped waiting meanwhile
		{"a", "", true, false},
		{"a", "waits for c", false, true}, // forgotten
	}
	var s sayOnce
	for i, st := range steps {
		if st.forget {
			s.forget(st.id)
			continue
		}
		if got := s.changed(st.id, st.why); got != st.want {
			t.Errorf("step %d: changed(%q, %q) = %v, want %v", i, st.id, st.why, got, st.want)
		}
		if got := s.last(st.id); got != st.why {
			t.Errorf("step %d: last(%q) = %q, want %q", i, st.id, got, st.why)
		}
	}
}
