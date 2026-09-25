package bridge

import (
	"sync"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
)

// Replay protection for commands that press keys.
//
// The per-pane mutex only serialises commands; it does not stop a duplicate
// from re-validating against the same seq and screen before herdr and the TUI
// reflect the first key press. A second "1" would then land on whatever menu
// comes next. Duplicates are real: transport replays (same request_id) and
// second taps (new request_id, same expected_seq + fingerprint) from a retry,
// a notification action plus the in-app button, or a re-enabled button.

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

// claimPrompt marks the prompt fp at seq as acted on. It returns false, and
// changes nothing, if that prompt was already claimed at the same seq.
func (e *Engine) claimPrompt(paneID string, seq uint64, fp string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.consumed == nil {
		e.consumed = make(map[string]*consumedPrompts)
	}
	c, ok := e.consumed[paneID]
	if !ok || c.seq != seq {
		c = &consumedPrompts{seq: seq}
		e.consumed[paneID] = c
	}
	for _, f := range c.fingerprints {
		if f == fp {
			return false
		}
	}
	if len(c.fingerprints) >= consumedCap {
		c.fingerprints = c.fingerprints[1:]
	}
	c.fingerprints = append(c.fingerprints, fp)
	return true
}

// forgetConsumedLocked drops the pane's record when the pane is gone or herdr
// reports a different seq. Callers hold e.mu.
func (e *Engine) forgetConsumedLocked(paneID string, gone bool, seq uint64) {
	c, ok := e.consumed[paneID]
	if !ok {
		return
	}
	if gone || c.seq != seq {
		delete(e.consumed, paneID)
	}
}

// pruneConsumed drops records for panes that are no longer in live or whose
// seq moved on, so the map never outgrows the live pane set.
func (e *Engine) pruneConsumed(live []herdr.AgentInfo) {
	seqs := make(map[string]uint64, len(live))
	for _, a := range live {
		seqs[a.PaneID] = a.StateChangeSeq
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for paneID, c := range e.consumed {
		if seq, ok := seqs[paneID]; !ok || seq != c.seq {
			delete(e.consumed, paneID)
		}
	}
}

// paneLock serialises commands on one pane. It lives only while commands for
// that pane are waiting or running.
type paneLock struct {
	mu   sync.Mutex
	refs int
}

// lockPane blocks until no other command runs on paneID and returns the
// matching unlock. The entry is removed once no command references it.
func (e *Engine) lockPane(paneID string) func() {
	e.lockMu.Lock()
	if e.paneLocks == nil {
		e.paneLocks = make(map[string]*paneLock)
	}
	pl, ok := e.paneLocks[paneID]
	if !ok {
		pl = &paneLock{}
		e.paneLocks[paneID] = pl
	}
	pl.refs++
	e.lockMu.Unlock()

	pl.mu.Lock()
	return func() {
		pl.mu.Unlock()
		e.lockMu.Lock()
		pl.refs--
		if pl.refs == 0 {
			delete(e.paneLocks, paneID)
		}
		e.lockMu.Unlock()
	}
}
