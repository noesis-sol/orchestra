package load

import (
	"context"
	"testing"
	"time"
)

// Each signal has its own level: one past its threshold leaves the others green, and the reading's
// Level is the worst of them.
func TestEachSignalHasItsOwnLevel(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return start.Add(d) }
	k := Kernel{Pressure: PressureNormal, Swaps: 1000, Load1: 16, Cores: 10, Available: -1}
	s := fakeSampler(&k, nil)
	steps := []struct {
		name  string
		at    time.Duration
		set   func()
		want  Levels
		worst Level
	}{
		{"a load of 16 on 10 cores", 0, func() {}, Levels{CPU: Amber}, Amber},
		{"memory pressure warn", 5 * time.Second, func() { k.Load1, k.Pressure = 2, PressureWarn },
			Levels{Pressure: Red}, Red},
		{"swapping", 65 * time.Second, func() { k.Pressure, k.Swaps = PressureNormal, 1000+SwappingPerMinute },
			Levels{Swapping: Amber}, Amber},
		{"a load of 31 on 10 cores", 70 * time.Second, func() { k.Load1 = 31 }, Levels{CPU: Amber}, Amber},
		{"a load of 31 on 10 cores for a minute", 70*time.Second + OverLoadFor, func() {},
			Levels{CPU: Red}, Red},
		{"the load past it and pressure critical", 75*time.Second + OverLoadFor,
			func() { k.Pressure = PressureCritical }, Levels{CPU: Red, Pressure: Red}, Red},
	}
	for _, step := range steps {
		step.set()
		r := s.Read(ctx, at(step.at), nil)
		if r.Levels != step.want || r.Level != step.worst {
			t.Errorf("%s: levels %+v, level %v; want %+v, %v", step.name, r.Levels, r.Level, step.want, step.worst)
		}
	}
}
