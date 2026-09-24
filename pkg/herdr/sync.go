package herdr

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

// ChangeKind describes whether an agent was added, updated, or removed.
type ChangeKind int

const (
	Added ChangeKind = iota
	Updated
	Removed
)

// Change represents a state transition for an agent.
type Change struct {
	Kind  ChangeKind
	Agent AgentInfo  // current value (for Removed: the last known value)
	Prev  *AgentInfo // nil for Added
}

// Listener receives change batches and connectivity transitions from Syncer.
type Listener interface {
	OnChanges(changes []Change) // called with a batch, never concurrently
	OnHerdrOnline(online bool, pong Pong)
}

// Syncer monitors herdr, turning snapshot polling and event subscriptions into a clean diff stream.
type Syncer struct {
	Client       *Client
	Listener     Listener
	Debounce     time.Duration // default 150ms
	PollHealthy  time.Duration // default 15s
	PollDegraded time.Duration // default 2s
	Logger       *slog.Logger

	mu       sync.Mutex
	current  map[string]AgentInfo // pane_id -> AgentInfo
	online   bool
	lastPong Pong
}

// Run blocks until ctx is cancelled, maintaining the sync loop against herdr.
// It never returns an error when herdr is down; it keeps retrying with backoff.
func (s *Syncer) Run(ctx context.Context) error {
	debounce := s.Debounce
	if debounce <= 0 {
		debounce = 150 * time.Millisecond
	}
	pollHealthy := s.PollHealthy
	if pollHealthy <= 0 {
		pollHealthy = 15 * time.Second
	}
	pollDegraded := s.PollDegraded
	if pollDegraded <= 0 {
		pollDegraded = 2 * time.Second
	}

	backoff := 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pong, err := s.Client.Ping(ctx)
		if err != nil {
			s.mu.Lock()
			wasOnline := s.online
			s.online = false
			s.mu.Unlock()

			if wasOnline && s.Listener != nil {
				s.Listener.OnHerdrOnline(false, Pong{})
			}

			sleepJitter(ctx, backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}

		// Ping succeeded
		s.mu.Lock()
		wasOnline := s.online
		s.online = true
		s.lastPong = pong
		s.mu.Unlock()

		if !wasOnline {
			backoff = 500 * time.Millisecond
			if s.Listener != nil {
				s.Listener.OnHerdrOnline(true, pong)
			}
		}

		// Initial list
		agents, err := s.Client.ListAgents(ctx)
		if err != nil {
			sleepJitter(ctx, backoff)
			continue
		}
		s.applyAgents(agents)

		// Run stream / degraded polling loop
		s.runStream(ctx, pollHealthy, pollDegraded, debounce)
	}
}

// runStream manages the event subscription and degraded fallback polling.
func (s *Syncer) runStream(ctx context.Context, pollHealthy, pollDegraded, debounce time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stream, cancelSub, ok := s.obtainStream(ctx, pollDegraded)
		if !ok {
			return
		}

		s.handleStream(ctx, stream, cancelSub, pollHealthy, debounce)
	}
}

func (s *Syncer) obtainStream(ctx context.Context, pollDegraded time.Duration) (<-chan Event, context.CancelFunc, bool) {
	subs := s.subscriptionsFor(s.Snapshot())
	subCtx, cancelSub := context.WithCancel(ctx)
	stream, err := s.Client.Subscribe(subCtx, subs)
	if err == nil {
		return stream, cancelSub, true
	}
	cancelSub()

	// Degraded polling mode until we can re-subscribe or herdr goes offline
	degradedTicker := time.NewTicker(pollDegraded)
	defer degradedTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, nil, false
		case <-degradedTicker.C:
			if _, pingErr := s.Client.Ping(ctx); pingErr != nil {
				return nil, nil, false
			}
			_, _ = s.Refresh(ctx)

			subs = s.subscriptionsFor(s.Snapshot())
			subCtx, cancelSub = context.WithCancel(ctx)
			stream, err = s.Client.Subscribe(subCtx, subs)
			if err == nil {
				return stream, cancelSub, true
			}
			cancelSub()
		}
	}
}

func (s *Syncer) handleStream(ctx context.Context, stream <-chan Event, cancelSub context.CancelFunc, pollHealthy, debounce time.Duration) {
	defer cancelSub()

	pollTicker := time.NewTicker(pollHealthy)
	defer pollTicker.Stop()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	defer func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case _, ok := <-stream:
			if !ok {
				// Stream dropped
				return
			}

			if debounceC == nil {
				debounceTimer = time.NewTimer(debounce)
				debounceC = debounceTimer.C
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(debounce)
			}

		case <-debounceC:
			debounceC = nil
			panesBefore := s.paneIDSet()
			_, _ = s.Refresh(ctx)
			panesAfter := s.paneIDSet()
			if !setsEqual(panesBefore, panesAfter) {
				return
			}

		case <-pollTicker.C:
			panesBefore := s.paneIDSet()
			_, _ = s.Refresh(ctx)
			panesAfter := s.paneIDSet()
			if !setsEqual(panesBefore, panesAfter) {
				return
			}
		}
	}
}

// Snapshot returns the last known agent list (copy). Safe for concurrent use.
func (s *Syncer) Snapshot() []AgentInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]AgentInfo, 0, len(s.current))
	for _, a := range s.current {
		res = append(res, a)
	}
	return res
}

// Refresh forces an immediate re-list (used by the bridge right before executing a command).
func (s *Syncer) Refresh(ctx context.Context) ([]AgentInfo, error) {
	agents, err := s.Client.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	s.applyAgents(agents)
	return s.Snapshot(), nil
}

func (s *Syncer) applyAgents(rawAgents []AgentInfo) []Change {
	newMap := make(map[string]AgentInfo)
	for _, a := range rawAgents {
		// Filter out plain shells
		if a.Agent == nil && a.AgentStatus == "unknown" {
			continue
		}
		newMap[a.PaneID] = a
	}

	s.mu.Lock()
	if s.current == nil {
		s.current = make(map[string]AgentInfo)
	}

	var changes []Change

	// Added and Updated
	for paneID, newAgent := range newMap {
		if oldAgent, exists := s.current[paneID]; exists {
			if agentDiffers(oldAgent, newAgent) {
				prevCopy := oldAgent
				changes = append(changes, Change{
					Kind:  Updated,
					Agent: newAgent,
					Prev:  &prevCopy,
				})
			}
		} else {
			changes = append(changes, Change{
				Kind:  Added,
				Agent: newAgent,
				Prev:  nil,
			})
		}
	}

	// Removed
	for paneID, oldAgent := range s.current {
		if _, exists := newMap[paneID]; !exists {
			prevCopy := oldAgent
			changes = append(changes, Change{
				Kind:  Removed,
				Agent: oldAgent,
				Prev:  &prevCopy,
			})
		}
	}

	s.current = newMap
	s.mu.Unlock()

	if len(changes) > 0 && s.Listener != nil {
		s.Listener.OnChanges(changes)
	}

	return changes
}

func (s *Syncer) subscriptionsFor(agents []AgentInfo) []Subscription {
	subs := []Subscription{
		{Type: "pane.created"},
		{Type: "pane.closed"},
		{Type: "pane.exited"},
		{Type: "pane.agent_detected"},
	}
	for _, a := range agents {
		subs = append(subs, Subscription{
			Type:   "pane.agent_status_changed",
			PaneID: a.PaneID,
		})
	}
	return subs
}

func (s *Syncer) paneIDSet() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make(map[string]bool, len(s.current))
	for k := range s.current {
		res[k] = true
	}
	return res
}

func setsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func ptrStrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func sessionEqual(a, b *AgentSession) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func agentDiffers(a, b AgentInfo) bool {
	if a.AgentStatus != b.AgentStatus ||
		a.StateChangeSeq != b.StateChangeSeq ||
		a.Focused != b.Focused ||
		!ptrStrEqual(a.Agent, b.Agent) ||
		!ptrStrEqual(a.Name, b.Name) ||
		!ptrStrEqual(a.TerminalTitleStripped, b.TerminalTitleStripped) ||
		!ptrStrEqual(a.CWD, b.CWD) ||
		!ptrStrEqual(a.ForegroundCWD, b.ForegroundCWD) ||
		!sessionEqual(a.AgentSession, b.AgentSession) {
		return true
	}
	return false
}

func sleepJitter(ctx context.Context, base time.Duration) {
	factor := 0.8 + 0.4*rand.Float64()
	d := time.Duration(float64(base) * factor)
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
