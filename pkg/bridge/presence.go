package bridge

import (
	"bytes"
	"context"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// Presence is whether the owner is using this host: the time since the last
// keyboard or mouse input, and whether the screen is locked. The relay holds
// back pushes while they are there (contracts.md §4.3).
type Presence struct {
	Idle   time.Duration
	Locked bool
}

// PresenceReader reads the host's presence; false when it cannot tell, and
// then the bridge reports nothing (the relay pushes as usual).
type PresenceReader func(ctx context.Context) (Presence, bool)

// presenceTimeout bounds one read (two ioreg runs, ~20 ms each).
const presenceTimeout = 2 * time.Second

var (
	hidIdleRe      = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)
	screenLockedRe = regexp.MustCompile(`"CGSSessionScreenIsLocked"\s*=\s*Yes`)
)

// parsePresence reads ioreg's IOHIDSystem output (HIDIdleTime, nanoseconds)
// and its Root output (IOConsoleUsers, which carries
// CGSSessionScreenIsLocked=Yes only while the screen is locked).
func parsePresence(hid, root []byte) (Presence, bool) {
	m := hidIdleRe.FindSubmatch(hid)
	if m == nil || !bytes.Contains(root, []byte(`"IOConsoleUsers"`)) {
		return Presence{}, false
	}
	ns, err := strconv.ParseUint(string(m[1]), 10, 64)
	if err != nil {
		return Presence{}, false
	}
	idle := time.Duration(math.MaxInt64)
	if ns <= math.MaxInt64 { // checked before converting: a larger value would wrap negative
		idle = time.Duration(ns)
	}
	return Presence{Idle: idle, Locked: screenLockedRe.Match(root)}, true
}

// PresenceOffIdleSeconds is the idle time of the one report a bridge sends
// when the owner turns presence off: it reads as away, so the relay stops
// holding pushes back at once instead of when the last report goes stale.
const PresenceOffIdleSeconds = 365 * 24 * 60 * 60

// presenceMsg reads the presence once; false when there is nothing to send.
// While the owner has it off it sends nothing, except one away report right
// after it was turned off.
func (e *Engine) presenceMsg(ctx context.Context) (model.HostPresenceMsg, bool) {
	if e.PresenceEnabled == nil || !e.PresenceEnabled() {
		if e.presenceOn.Swap(false) {
			return model.HostPresenceMsg{Type: model.WireHostPresence, IdleSeconds: PresenceOffIdleSeconds}, true
		}
		return model.HostPresenceMsg{}, false
	}
	e.presenceOn.Store(true)
	if e.Presence == nil {
		return model.HostPresenceMsg{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, presenceTimeout)
	defer cancel()
	p, ok := e.Presence(ctx)
	if !ok {
		return model.HostPresenceMsg{}, false
	}
	return model.HostPresenceMsg{
		Type:        model.WireHostPresence,
		IdleSeconds: uint64(p.Idle / time.Second),
		Locked:      p.Locked,
	}, true
}

// StartPresence sends a presence report every PresenceInterval while the
// relay is connected. ConnectMessages sends the first one on each connect.
func (e *Engine) StartPresence(ctx context.Context) {
	if e.Presence == nil || e.PresenceInterval <= 0 || e.Relay == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(e.PresenceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.Relay.Connected() {
					continue
				}
				if msg, ok := e.presenceMsg(ctx); ok {
					e.Relay.Send(msg)
				}
			}
		}
	}()
}
