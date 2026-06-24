package api

import (
	"math/rand"
	"testing"
	"time"
)

func TestBackoffDelayAttempt0(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		d := backoffDelay(0, rng)
		if d < 850*time.Millisecond || d > 1150*time.Millisecond {
			t.Errorf("attempt 0: delay %v outside [850ms, 1150ms]", d)
		}
	}
}

func TestBackoffDelayExponential(t *testing.T) {
	rng := rand.New(rand.NewSource(0))
	bases := []time.Duration{1, 2, 4, 8, 16}
	for attempt, mult := range bases {
		base := mult * time.Second
		low := time.Duration(float64(base) * 0.85)
		high := time.Duration(float64(base) * 1.15)
		d := backoffDelay(attempt, rng)
		if d < low || d > high {
			t.Errorf("attempt %d (base %v): delay %v outside [%v, %v]", attempt, base, d, low, high)
		}
	}
}

func TestBackoffDelayCapAt30s(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		d := backoffDelay(100, rng)
		// 30s ± 15%
		if d < 25500*time.Millisecond || d > 34500*time.Millisecond {
			t.Errorf("attempt 100: delay %v outside [25.5s, 34.5s]", d)
		}
	}
}
