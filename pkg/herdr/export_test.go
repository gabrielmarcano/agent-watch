package herdr

import (
	"sort"
	"sync"
	"time"
)

// FakeClock is a manual clock for Syncer tests: timers fire only on Advance.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	active map[*fakeTimer]struct{}
}

type fakeTimer struct {
	c    *FakeClock
	ch   chan time.Time
	when time.Duration // absolute fake time it fires at
	dur  time.Duration // duration it was armed with
}

// NewFakeClock returns a clock at time zero with no timers.
func NewFakeClock() *FakeClock {
	return &FakeClock{active: make(map[*fakeTimer]struct{})}
}

// UseFakeClock makes s wait on c and removes jitter, so every backoff step is
// exact. Call it before Run.
func UseFakeClock(s *Syncer, c *FakeClock) {
	s.clock = c
	s.spread = func(d time.Duration) time.Duration { return d }
}

func (c *FakeClock) NewTimer(d time.Duration) timer {
	t := &fakeTimer{c: c, ch: make(chan time.Time, 1)}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.arm(t, d)
	return t
}

func (c *FakeClock) arm(t *fakeTimer, d time.Duration) {
	t.when = c.now + d
	t.dur = d
	c.active[t] = struct{}{}
	if d <= 0 {
		c.fire(t)
	}
}

func (c *FakeClock) fire(t *fakeTimer) {
	delete(c.active, t)
	select {
	case t.ch <- time.Unix(0, int64(c.now)):
	default:
	}
}

// Advance moves the clock forward and fires every timer that is due, in
// deadline order.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	var due []*fakeTimer
	for t := range c.active {
		if t.when <= c.now {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].when < due[j].when })
	for _, t := range due {
		c.fire(t)
	}
}

// Armed returns the durations the currently armed timers were set with, sorted.
func (c *FakeClock) Armed() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, 0, len(c.active))
	for t := range c.active {
		out = append(out, t.dur)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	_, wasActive := t.c.active[t]
	delete(t.c.active, t)
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	_, wasActive := t.c.active[t]
	t.c.arm(t, d)
	return wasActive
}
