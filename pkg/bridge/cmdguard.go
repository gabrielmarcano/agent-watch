package bridge

import (
	"context"
	"errors"
	"sync"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
)

// Replay protection for commands that press keys or create sessions.
//
// The commandGuard serialises commands per pane, serialises session creation,
// deduplicates request_ids, and ensures prompt key presses and prompt submissions
// are only executed once per state_change_seq.

const (
	// requestIDCap bounds how many recent request_ids are remembered.
	requestIDCap = 256
	// consumedCap bounds the fingerprints remembered per pane at one seq.
	consumedCap = 8
)

// requestIDLog remembers the most recent request_ids in a fixed ring.
type requestIDLog struct {
	mu   sync.Mutex
	seen map[string]struct{}
	ring [requestIDCap]string
	next int
}

// observe records id and reports whether it had already been seen. An empty
// id cannot be deduplicated and is never reported as seen.
func (l *requestIDLog) observe(id string) bool {
	if id == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = make(map[string]struct{}, requestIDCap)
	}
	if _, ok := l.seen[id]; ok {
		return true
	}
	if old := l.ring[l.next]; old != "" {
		delete(l.seen, old)
	}
	l.ring[l.next] = id
	l.seen[id] = struct{}{}
	l.next = (l.next + 1) % requestIDCap
	return false
}

// consumedPrompts lists the prompts already answered or cancelled on a pane
// at one herdr seq. It is dropped as soon as herdr reports another seq.
type consumedPrompts struct {
	seq          uint64
	fingerprints []string
}

// paneLock serialises commands on one pane. It lives only while commands for
// that pane are waiting or running.
type paneLock struct {
	held chan struct{} // cap 1: a value in it means a command holds the pane
	refs int           // commands waiting for or holding the lock (guarded by commandGuard.mu)
}

// commandGuard owns command serialization, request deduplication, and prompt
// claim protection, independent of Engine.mu.
type commandGuard struct {
	mu          sync.Mutex
	consumed    map[string]*consumedPrompts
	paneLocks   map[string]*paneLock
	sessionOnce sync.Once
	sessionLock chan struct{}
	requests    requestIDLog
}

func newCommandGuard() *commandGuard {
	return &commandGuard{
		consumed:    make(map[string]*consumedPrompts),
		paneLocks:   make(map[string]*paneLock),
		sessionLock: make(chan struct{}, 1),
	}
}

// AcquirePane acquires the per-pane lock within ctx and checks/records requestID
// under that lock.
func (g *commandGuard) AcquirePane(ctx context.Context, paneID, requestID string) (release func(), duplicate bool, err error) {
	g.mu.Lock()
	if g.paneLocks == nil {
		g.paneLocks = make(map[string]*paneLock)
	}
	pl, ok := g.paneLocks[paneID]
	if !ok {
		pl = &paneLock{held: make(chan struct{}, 1)}
		g.paneLocks[paneID] = pl
	}
	pl.refs++
	g.mu.Unlock()

	decRef := func() {
		g.mu.Lock()
		pl.refs--
		if pl.refs == 0 {
			delete(g.paneLocks, paneID)
		}
		g.mu.Unlock()
	}

	select {
	case pl.held <- struct{}{}:
	case <-ctx.Done():
		decRef()
		return nil, false, ctx.Err()
	}
	// Both cases can be ready at once; never start past the deadline.
	if err := ctx.Err(); err != nil {
		<-pl.held
		decRef()
		return nil, false, err
	}

	unlock := func() {
		<-pl.held
		decRef()
	}

	if g.requests.observe(requestID) {
		unlock()
		return func() {}, true, nil
	}

	return unlock, false, nil
}

// AcquireSession acquires the host-session lock within ctx and checks/records requestID.
func (g *commandGuard) AcquireSession(ctx context.Context, requestID string) (release func(), duplicate bool, err error) {
	g.sessionOnce.Do(func() {
		if g.sessionLock == nil {
			g.sessionLock = make(chan struct{}, 1)
		}
	})

	select {
	case g.sessionLock <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-g.sessionLock
		return nil, false, err
	}

	unlock := func() {
		<-g.sessionLock
	}

	if g.requests.observe(requestID) {
		unlock()
		return func() {}, true, nil
	}

	return unlock, false, nil
}

// DoClaim atomically claims (paneID, seq, claim), runs fn(), and automatically
// rolls back the claim if fn() fails with errors.Is(err, herdr.ErrUnavailable)
// or herdr.IsCode(err, "blocked") / herdr.IsCode(err, "agent_blocked").
func (g *commandGuard) DoClaim(paneID string, seq uint64, claim string, fn func() error) (alreadyClaimed bool, err error) {
	g.mu.Lock()
	if g.consumed == nil {
		g.consumed = make(map[string]*consumedPrompts)
	}
	c, ok := g.consumed[paneID]
	if !ok || c.seq != seq {
		c = &consumedPrompts{seq: seq}
		g.consumed[paneID] = c
	}
	for _, f := range c.fingerprints {
		if f == claim {
			g.mu.Unlock()
			return true, nil
		}
	}
	if len(c.fingerprints) >= consumedCap {
		c.fingerprints = c.fingerprints[1:]
	}
	c.fingerprints = append(c.fingerprints, claim)
	g.mu.Unlock()

	err = fn()
	if err != nil && (errors.Is(err, herdr.ErrUnavailable) || herdr.IsCode(err, "blocked") || herdr.IsCode(err, "agent_blocked")) {
		g.mu.Lock()
		if cur, ok := g.consumed[paneID]; ok && cur.seq == seq {
			for i, f := range cur.fingerprints {
				if f == claim {
					cur.fingerprints = append(cur.fingerprints[:i:i], cur.fingerprints[i+1:]...)
					break
				}
			}
		}
		g.mu.Unlock()
	}
	return false, err
}

// Forget drops the pane's record when the pane is gone or herdr reports a different seq.
func (g *commandGuard) Forget(paneID string, seq uint64, gone bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.consumed[paneID]
	if !ok {
		return
	}
	if gone || c.seq != seq {
		delete(g.consumed, paneID)
	}
}

// Prune drops records for panes that are no longer in live or whose seq moved on.
func (g *commandGuard) Prune(live []herdr.AgentInfo) {
	seqs := make(map[string]uint64, len(live))
	for _, a := range live {
		seqs[a.PaneID] = a.StateChangeSeq
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for paneID, c := range g.consumed {
		if seq, ok := seqs[paneID]; !ok || seq != c.seq {
			delete(g.consumed, paneID)
		}
	}
}

// ActiveLocks returns the count of panes with active or waiting command locks.
func (g *commandGuard) ActiveLocks() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.paneLocks)
}

// ObserveRequest records id and reports whether it had already been seen.
func (g *commandGuard) ObserveRequest(id string) bool {
	return g.requests.observe(id)
}
