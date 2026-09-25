package herdr

import (
	"math/rand/v2"
	"time"
)

// clock creates the timers the Syncer waits on. Tests swap in a fake clock
// to drive backoff, polling and debouncing deterministically.
type clock interface {
	NewTimer(d time.Duration) timer
}

// timer is the subset of *time.Timer the Syncer uses.
type timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

type realClock struct{}

func (realClock) NewTimer(d time.Duration) timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

// stopTimer stops t and discards a value it already delivered but nobody
// received, so a later Reset never fires early.
func stopTimer(t timer) {
	if !t.Stop() {
		select {
		case <-t.C():
		default:
		}
	}
}

// resetTimer re-arms t for d, dropping any stale value first.
func resetTimer(t timer, d time.Duration) {
	stopTimer(t)
	t.Reset(d)
}

// newStoppedTimer returns a timer that is not armed.
func newStoppedTimer(c clock) timer {
	t := c.NewTimer(time.Hour)
	stopTimer(t)
	return t
}

const (
	backoffMin = 500 * time.Millisecond
	backoffMax = 30 * time.Second
)

// jitter spreads d by ±20% using r in [0, 1).
func jitter(d time.Duration, r float64) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*r))
}

func randomJitter(d time.Duration) time.Duration { return jitter(d, rand.Float64()) }

// backoff yields 500ms, 1s, 2s … capped at 30s, each passed through spread
// (±20% jitter in production).
type backoff struct {
	next   time.Duration
	spread func(time.Duration) time.Duration
}

// Next returns the delay before the next attempt and doubles the base.
func (b *backoff) Next() time.Duration {
	d := b.next
	if d <= 0 {
		d = backoffMin
	}
	b.next = min(d*2, backoffMax)
	return b.spread(d)
}

// Reset starts the sequence over at 500ms.
func (b *backoff) Reset() { b.next = 0 }

// Fresh reports whether the sequence is at its start.
func (b *backoff) Fresh() bool { return b.next == 0 }
