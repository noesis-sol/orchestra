package command

import (
	"testing"
	"time"
)

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		2 * time.Hour: "2h", 90 * time.Minute: "1h30m", 45 * time.Minute: "45m", 90 * time.Second: "1m30s",
		30 * time.Second: "30s", 50 * time.Millisecond: "50ms", time.Hour + 30*time.Second: "1h0m30s",
	} {
		if got := ShortDuration(d); got != want {
			t.Errorf("ShortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
