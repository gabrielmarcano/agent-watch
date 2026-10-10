package relay

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	subscriberBufferSize = 64
)

// sseEvent represents a single Server-Sent Event to be sent to subscribers.
type sseEvent struct {
	Name string
	Data []byte // must be single-line JSON without trailing newline
}

// subscriber is one SSE client. Its channel is closed exactly once, under mu,
// and every send also happens under mu, so a send can never hit a closed
// channel. Sends are non-blocking, so holding mu never blocks a broadcaster.
type subscriber struct {
	mu     sync.Mutex
	ch     chan sseEvent
	closed bool
}

// trySend delivers ev without blocking. It returns false when the buffer is
// full. Sending to a closed subscriber is a no-op that reports success.
func (sub *subscriber) trySend(ev sseEvent) bool {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return true
	}
	select {
	case sub.ch <- ev:
		return true
	default:
		return false
	}
}

// close closes the channel once; the SSE handler sees it and returns.
func (sub *subscriber) close() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if !sub.closed {
		sub.closed = true
		close(sub.ch)
	}
}

// hostEntry is one host's flags and its agents, keyed by pane_id.
type hostEntry struct {
	info   model.HostInfo
	agents map[string]model.AgentState
}

// State maintains the hosts, their agents and online flags, and the active
// SSE subscribers. Agents are keyed by (host, pane_id): every herdr numbers
// its own panes.
type State struct {
	// pub serializes each change with its broadcast, so subscribers get the
	// events in the order the changes happened, also when several hosts send
	// at once. Taken before mu; readers never take it.
	pub sync.Mutex

	mu          sync.RWMutex
	hosts       map[string]*hostEntry
	subscribers map[*subscriber]struct{}
}

// NewState creates an empty State.
func NewState() *State {
	return &State{
		hosts:       make(map[string]*hostEntry),
		subscribers: make(map[*subscriber]struct{}),
	}
}

// entryLocked returns the host's entry, creating it (named after its id) for
// a host not registered yet.
func (s *State) entryLocked(host string) *hostEntry {
	e, ok := s.hosts[host]
	if !ok {
		e = &hostEntry{
			info:   model.HostInfo{ID: host, Name: host},
			agents: make(map[string]model.AgentState),
		}
		s.hosts[host] = e
	}
	return e
}

// Snapshot returns an AgentsSnapshot of the current state.
func (s *State) Snapshot() model.AgentsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.snapshotLocked()
}

func (s *State) snapshotLocked() model.AgentsSnapshot {
	list := make([]model.AgentState, 0)
	for _, e := range s.hosts {
		for _, a := range e.agents {
			list = append(list, a)
		}
	}
	model.SortAgents(list)

	hostOnline, herdrOnline := s.aggregatesLocked()
	return model.AgentsSnapshot{
		HostOnline:  hostOnline,
		HerdrOnline: herdrOnline,
		Hosts:       s.hostsLocked(),
		Agents:      list,
		GeneratedAt: model.Now(),
	}
}

// hostsLocked lists every known host by name, then id (contracts.md §1.6).
func (s *State) hostsLocked() []model.HostInfo {
	out := make([]model.HostInfo, 0, len(s.hosts))
	for _, e := range s.hosts {
		out = append(out, e.info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// aggregatesLocked returns the snapshot's flags for clients that predate
// hosts: any host connected; every connected host reaches its herdr (false
// with none connected).
func (s *State) aggregatesLocked() (hostOnline, herdrOnline bool) {
	herdrOnline = true
	for _, e := range s.hosts {
		if e.info.Online {
			hostOnline = true
			herdrOnline = herdrOnline && e.info.HerdrOnline
		}
	}
	return hostOnline, hostOnline && herdrOnline
}

// Hosts returns every known host, by name, then id.
func (s *State) Hosts() []model.HostInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hostsLocked()
}

// HostName returns the name a host's notifications show and how many hosts
// are known (titles name the host only when there are several). An unknown
// host is named by its id.
func (s *State) HostName(host string) (name string, hosts int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name = host
	if e, ok := s.hosts[host]; ok {
		name = e.info.Name
	}
	return name, len(s.hosts)
}

// AddHost registers a host (offline until its bridge connects) or renames a
// known one, and broadcasts a "host" event when the list changed.
func (s *State) AddHost(id, name string) {
	s.pub.Lock()
	defer s.pub.Unlock()
	if name == "" {
		name = id
	}
	s.mu.Lock()
	e, known := s.hosts[id]
	changed := !known || e.info.Name != name
	e = s.entryLocked(id)
	e.info.Name = name
	ev := s.hostEventLocked()
	s.mu.Unlock()

	if changed {
		s.broadcastJSON("host", ev)
	}
}

// RemoveHost forgets a host and its agents (a revoked host) and broadcasts a
// "snapshot" event. Unknown ids are ignored.
func (s *State) RemoveHost(id string) {
	s.pub.Lock()
	defer s.pub.Unlock()
	s.mu.Lock()
	if _, ok := s.hosts[id]; !ok {
		s.mu.Unlock()
		return
	}
	delete(s.hosts, id)
	snap := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcastJSON("snapshot", snap)
}

// HostOnline returns whether the host's bridge is connected.
func (s *State) HostOnline(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.hosts[host]
	return ok && e.info.Online
}

// HerdrOnline returns whether the host's bridge is connected and reaches herdr.
func (s *State) HerdrOnline(host string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.hosts[host]
	return ok && e.info.HerdrOnline
}

// Get returns a pane's current state on a host.
func (s *State) Get(host, paneID string) (model.AgentState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.hosts[host]; ok {
		a, ok := e.agents[paneID]
		return a, ok
	}
	return model.AgentState{}, false
}

// HasPane returns whether a host's agents include a pane.
func (s *State) HasPane(host, paneID string) bool {
	_, ok := s.Get(host, paneID)
	return ok
}

// HostsWithPane returns the ids of the hosts whose agents include paneID,
// sorted.
func (s *State) HostsWithPane(paneID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for id, e := range s.hosts {
		if _, ok := e.agents[paneID]; ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// AgentCount returns the count of a host's agents.
func (s *State) AgentCount(host string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.hosts[host]; ok {
		return len(e.agents)
	}
	return 0
}

// ReplaceAll replaces one host's agents from its SnapshotMsg, stamping the
// host on each, and broadcasts a "snapshot" event. Other hosts keep theirs.
func (s *State) ReplaceAll(host string, agents []model.AgentState) {
	s.pub.Lock()
	defer s.pub.Unlock()
	s.mu.Lock()
	e := s.entryLocked(host)
	e.agents = make(map[string]model.AgentState, len(agents))
	for _, a := range agents {
		a.Host = host
		e.agents[a.PaneID] = a
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcastJSON("snapshot", snap)
}

// Upsert adds or updates an agent of a.Host, returning the previous state if
// present, and broadcasts an "agent" event.
func (s *State) Upsert(a model.AgentState) (prev *model.AgentState) {
	s.pub.Lock()
	defer s.pub.Unlock()
	s.mu.Lock()
	e := s.entryLocked(a.Host)
	if existing, ok := e.agents[a.PaneID]; ok {
		prevCopy := existing
		prev = &prevCopy
	}
	e.agents[a.PaneID] = a
	s.mu.Unlock()

	s.broadcastJSON("agent", a)
	return prev
}

// Remove removes a host's pane and broadcasts an "agent_removed" event.
func (s *State) Remove(host, paneID string) {
	s.pub.Lock()
	defer s.pub.Unlock()
	s.mu.Lock()
	if e, ok := s.hosts[host]; ok {
		delete(e.agents, paneID)
	}
	s.mu.Unlock()

	s.broadcastJSON("agent_removed", model.AgentRemovedEvent{Host: host, PaneID: paneID})
}

// SetHost updates a host's online flags (herdr_online is false while the host
// is offline). If they changed, it broadcasts a "host" event. Going offline
// never brings back a host that RemoveHost forgot.
func (s *State) SetHost(host string, online, herdrOnline bool) {
	s.pub.Lock()
	defer s.pub.Unlock()
	herdrOnline = online && herdrOnline
	s.mu.Lock()
	if _, known := s.hosts[host]; !known && !online {
		s.mu.Unlock() // a forgotten (revoked) host stays forgotten
		return
	}
	e := s.entryLocked(host)
	changed := e.info.Online != online || e.info.HerdrOnline != herdrOnline
	e.info.Online = online
	e.info.HerdrOnline = herdrOnline
	ev := s.hostEventLocked()
	s.mu.Unlock()

	if changed {
		s.broadcastJSON("host", ev)
	}
}

func (s *State) hostEventLocked() model.HostEvent {
	hostOnline, herdrOnline := s.aggregatesLocked()
	return model.HostEvent{HostOnline: hostOnline, HerdrOnline: herdrOnline, Hosts: s.hostsLocked()}
}

// broadcastJSON broadcasts v as the data of an event named name.
func (s *State) broadcastJSON(name string, v any) {
	data, err := json.Marshal(v)
	if err == nil {
		s.broadcast(sseEvent{Name: name, Data: data})
	}
}

// BroadcastHistory broadcasts a "history" event to all subscribers.
func (s *State) BroadcastHistory(item model.HistoryItem) {
	s.pub.Lock()
	defer s.pub.Unlock()
	s.broadcastJSON("history", item)
}

// Subscribe registers a new SSE subscriber with a buffered channel.
func (s *State) Subscribe() (*subscriber, <-chan sseEvent) {
	sub := &subscriber{
		ch: make(chan sseEvent, subscriberBufferSize),
	}

	s.mu.Lock()
	s.subscribers[sub] = struct{}{}
	s.mu.Unlock()

	return sub, sub.ch
}

// Unsubscribe unregisters an SSE subscriber.
func (s *State) Unsubscribe(sub *subscriber) {
	s.mu.Lock()
	delete(s.subscribers, sub)
	s.mu.Unlock()
	sub.close()
}

// broadcast sends ev to all subscribers and drops slow subscribers whose
// buffer is full (their channel is closed so the client reconnects and
// resyncs from a snapshot). It never blocks and never holds the state mutex
// while sending; each send is guarded by its subscriber's own lock.
func (s *State) broadcast(ev sseEvent) {
	s.mu.RLock()
	subs := make([]*subscriber, 0, len(s.subscribers))
	for sub := range s.subscribers {
		subs = append(subs, sub)
	}
	s.mu.RUnlock()

	var slowSubs []*subscriber
	for _, sub := range subs {
		if !sub.trySend(ev) {
			slowSubs = append(slowSubs, sub)
		}
	}

	if len(slowSubs) > 0 {
		s.mu.Lock()
		for _, sub := range slowSubs {
			delete(s.subscribers, sub)
		}
		s.mu.Unlock()
		for _, sub := range slowSubs {
			sub.close()
		}
	}
}
