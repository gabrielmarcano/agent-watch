package push

import (
	"sort"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// PresenceStale is how long a presence report counts: three missed 15 s
// reports, like the SSE silence rule (contracts.md §2.3, §4.3).
const PresenceStale = 45 * time.Second

// presenceState is what a host last said about its owner, and the blocked
// pushes held back while they were there. It is one state for every host
// (the owner's decision): any host's report updates it, and while the owner
// is at that host no host's pushes go out.
type presenceState struct {
	reported    bool   // a report arrived since its host connected
	host        string // the host that sent the last report
	reportAt    time.Time
	lastInputAt time.Time
	locked      bool
	quiet       map[paneKey]struct{} // panes whose blocked push was held back
	timer       stopper              // fires when presence would end; nil when none
	gen         uint64               // tells a stale timer from the current one
}

// OnHostPresence implements relay.Notifier: a host's input idle time and
// screen lock, received now.
func (d *Dispatcher) OnHostPresence(host string, idle time.Duration, locked bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	now := d.Now()
	p := &d.presence
	p.reported, p.host, p.reportAt, p.locked = true, host, now, locked
	p.lastInputAt = now.Add(-idle)
	// A negative idle, or one that puts the last input in the future, is
	// unknown (away). A huge idle reads as long idle: Time.Sub saturates.
	if idle < 0 || p.lastInputAt.After(now) {
		p.lastInputAt = time.Time{}
	}
	d.Logger.Debug("host presence", "host", host, "idle", idle, "locked", locked)

	if d.presentLocked(now) {
		d.armPresenceTimerLocked(now)
		return
	}
	d.endPresenceLocked()
}

// OnHostOffline implements relay.Notifier: a host disconnected. When it sent
// the last report, presence ends without a catch-up; another host's going
// offline changes nothing. The held-back panes wait for the next report (a
// reconnected bridge reports right after its snapshot), and the catch-up then
// reads their current state.
func (d *Dispatcher) OnHostOffline(host string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()

	if !d.presence.reported || d.presence.host != host {
		return
	}
	d.stopPresenceTimerLocked()
	d.presence.reported = false
}

// presentLocked reports whether the owner is at the host at time now.
func (d *Dispatcher) presentLocked(now time.Time) bool {
	p := d.presence
	if d.PresenceIdle <= 0 || !p.reported || p.locked || p.lastInputAt.IsZero() {
		return false
	}
	return now.Sub(p.reportAt) < PresenceStale && now.Sub(p.lastInputAt) < d.PresenceIdle
}

// heldBackLocked holds back m while the owner is at the host. A blocked push
// remembers its pane for the catch-up.
func (d *Dispatcher) heldBackLocked(m Message) bool {
	if !d.presentLocked(d.Now()) {
		return false
	}
	if m.Event == EventBlocked && len(d.presence.quiet) < maxShownBlocked {
		d.presence.quiet[keyOf(m)] = struct{}{}
	}
	d.Logger.Info("push held back", "event", m.Event, "host", m.Host, "pane", m.PaneID, "reason", "host_present")
	return true
}

// armPresenceTimerLocked (re)arms the timer for when presence would end: the
// idle threshold or the report going stale, whichever comes first.
func (d *Dispatcher) armPresenceTimerLocked(now time.Time) {
	d.stopPresenceTimerLocked()
	p := &d.presence
	end := p.lastInputAt.Add(d.PresenceIdle)
	if stale := p.reportAt.Add(PresenceStale); stale.Before(end) {
		end = stale
	}
	p.gen++
	gen := p.gen
	p.timer = d.afterFunc(end.Sub(now), func() { d.presenceTimerFired(gen) })
}

func (d *Dispatcher) stopPresenceTimerLocked() {
	if d.presence.timer != nil {
		d.presence.timer.Stop()
		d.presence.timer = nil
	}
}

// presenceTimerFired ends presence unless a newer report re-armed the timer.
func (d *Dispatcher) presenceTimerFired(gen uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.presence.timer == nil || d.presence.gen != gen {
		return
	}
	d.presence.timer = nil
	if now := d.Now(); d.presentLocked(now) {
		d.armPresenceTimerLocked(now)
		return
	}
	d.endPresenceLocked()
}

// endPresenceLocked pushes every held-back pane that is still blocked now,
// built from its current state, through the normal debounce, window and digest.
func (d *Dispatcher) endPresenceLocked() {
	d.stopPresenceTimerLocked()
	quiet := d.presence.quiet
	if len(quiet) == 0 {
		return
	}
	panes := make([]paneKey, 0, len(quiet))
	for pane := range quiet {
		panes = append(panes, pane)
	}
	sort.Slice(panes, func(i, j int) bool {
		if panes[i].host != panes[j].host {
			return panes[i].host < panes[j].host
		}
		return panes[i].pane < panes[j].pane
	})
	clear(quiet)
	if d.Current == nil {
		return
	}
	for _, pane := range panes {
		cur, ok := d.Current(pane.host, pane.pane)
		if !ok || cur.Status != model.StatusBlocked {
			continue
		}
		d.enqueueLocked(blockedMessage(cur), cur)
	}
}
