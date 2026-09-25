package herdr

import (
	"testing"
	"time"
)

func TestBackoffDoublesAndCaps(t *testing.T) {
	b := backoff{spread: func(d time.Duration) time.Duration { return d }}
	want := []time.Duration{
		500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("step %d: got %v, want %v", i, got, w)
		}
	}
	b.Reset()
	if !b.Fresh() {
		t.Error("Fresh() = false after Reset")
	}
	if got := b.Next(); got != backoffMin {
		t.Errorf("after Reset: got %v, want %v", got, backoffMin)
	}
}

func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	d := 10 * time.Second
	for _, r := range []float64{0, 0.25, 0.5, 0.75, 0.999999} {
		got := jitter(d, r)
		if got < 8*time.Second || got > 12*time.Second {
			t.Errorf("jitter(%v, %v) = %v, want within ±20%%", d, r, got)
		}
	}
	if got := jitter(d, 0); got != 8*time.Second {
		t.Errorf("lower bound: got %v", got)
	}
	for i := 0; i < 1000; i++ {
		if got := randomJitter(d); got < 8*time.Second || got > 12*time.Second {
			t.Fatalf("randomJitter(%v) = %v, outside ±20%%", d, got)
		}
	}
}
