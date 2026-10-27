package relayclient

import (
	"time"
)

type clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	NewTimer(d time.Duration) timer
}

type timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

type realClock struct{}

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) Since(t time.Time) time.Duration { return time.Since(t) }
func (realClock) NewTimer(d time.Duration) timer  { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
