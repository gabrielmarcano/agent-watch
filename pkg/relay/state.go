package relay

import (
	"encoding/json"
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

type subscriber struct {
	ch      chan sseEvent
	dropped bool
}

// State maintains current agent states, host/herdr online flags, and active SSE subscribers.
type State struct {
	mu          sync.RWMutex
	agents      map[string]model.AgentState
	hostOnline  bool
	herdrOnline bool
	subscribers map[*subscriber]struct{}
}

// NewState creates an empty State.
func NewState() *State {
	return &State{
		agents:      make(map[string]model.AgentState),
		subscribers: make(map[*subscriber]struct{}),
	}
}

// Snapshot returns an AgentsSnapshot of the current state.
func (s *State) Snapshot() model.AgentsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.snapshotLocked()
}

func (s *State) snapshotLocked() model.AgentsSnapshot {
	list := make([]model.AgentState, 0, len(s.agents))
	for _, a := range s.agents {
		list = append(list, a)
	}
	model.SortAgents(list)

	return model.AgentsSnapshot{
		HostOnline:  s.hostOnline,
		HerdrOnline: s.herdrOnline,
		Agents:      list,
		GeneratedAt: model.Now(),
	}
}

// HostOnline returns whether the bridge host is connected.
func (s *State) HostOnline() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hostOnline
}

// HerdrOnline returns whether the host can reach herdr.
func (s *State) HerdrOnline() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.herdrOnline
}

// HasPane returns whether a pane exists in the current agents map.
func (s *State) HasPane(paneID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.agents[paneID]
	return ok
}

// AgentCount returns the count of active agents.
func (s *State) AgentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.agents)
}

// ReplaceAll replaces all agents from a SnapshotMsg and broadcasts a "snapshot" event.
func (s *State) ReplaceAll(agents []model.AgentState) {
	s.mu.Lock()
	s.agents = make(map[string]model.AgentState, len(agents))
	for _, a := range agents {
		s.agents[a.PaneID] = a
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()

	data, err := json.Marshal(snap)
	if err == nil {
		s.broadcast(sseEvent{Name: "snapshot", Data: data})
	}
}

// Upsert adds or updates an agent, returning the previous state if present, and broadcasts an "agent" event.
func (s *State) Upsert(a model.AgentState) (prev *model.AgentState) {
	s.mu.Lock()
	if existing, ok := s.agents[a.PaneID]; ok {
		prevCopy := existing
		prev = &prevCopy
	}
	s.agents[a.PaneID] = a
	s.mu.Unlock()

	data, err := json.Marshal(a)
	if err == nil {
		s.broadcast(sseEvent{Name: "agent", Data: data})
	}
	return prev
}

// Remove removes an agent pane and broadcasts an "agent_removed" event.
func (s *State) Remove(paneID string) {
	s.mu.Lock()
	delete(s.agents, paneID)
	s.mu.Unlock()

	payload := map[string]string{"pane_id": paneID}
	data, err := json.Marshal(payload)
	if err == nil {
		s.broadcast(sseEvent{Name: "agent_removed", Data: data})
	}
}

// SetHost updates the host and herdr online flags. If changed, broadcasts a "host" event.
func (s *State) SetHost(hostOnline, herdrOnline bool) {
	s.mu.Lock()
	changed := s.hostOnline != hostOnline || s.herdrOnline != herdrOnline
	s.hostOnline = hostOnline
	s.herdrOnline = herdrOnline
	s.mu.Unlock()

	if changed {
		payload := map[string]bool{
			"host_online":  hostOnline,
			"herdr_online": herdrOnline,
		}
		data, err := json.Marshal(payload)
		if err == nil {
			s.broadcast(sseEvent{Name: "host", Data: data})
		}
	}
}

// BroadcastHistory broadcasts a "history" event to all subscribers.
func (s *State) BroadcastHistory(item model.HistoryItem) {
	data, err := json.Marshal(item)
	if err == nil {
		s.broadcast(sseEvent{Name: "history", Data: data})
	}
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
	defer s.mu.Unlock()

	if _, ok := s.subscribers[sub]; ok {
		delete(s.subscribers, sub)
		if !sub.dropped {
			close(sub.ch)
		}
	}
}

// broadcast sends ev to all subscribers. Drops slow subscribers whose buffer is full.
// Never blocks and never holds the mutex while sending.
func (s *State) broadcast(ev sseEvent) {
	s.mu.RLock()
	subs := make([]*subscriber, 0, len(s.subscribers))
	for sub := range s.subscribers {
		subs = append(subs, sub)
	}
	s.mu.RUnlock()

	var slowSubs []*subscriber
	for _, sub := range subs {
		select {
		case sub.ch <- ev:
		default:
			slowSubs = append(slowSubs, sub)
		}
	}

	if len(slowSubs) > 0 {
		s.mu.Lock()
		for _, sub := range slowSubs {
			if _, ok := s.subscribers[sub]; ok {
				delete(s.subscribers, sub)
				sub.dropped = true
				close(sub.ch)
			}
		}
		s.mu.Unlock()
	}
}
