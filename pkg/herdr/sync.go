package herdr

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ChangeKind represents the type of change detected for an agent pane.
type ChangeKind string

const (
	Added   ChangeKind = "added"
	Updated ChangeKind = "updated"
	Removed ChangeKind = "removed"
)

// Change represents a state transition for a single agent pane.
type Change struct {
	Kind  ChangeKind
	Agent AgentInfo
	Prev  *AgentInfo // nil for Added
}

// Listener receives callbacks from the Syncer as herdr-core's state evolves.
type Listener interface {
	OnChanges(changes []Change)
	OnHerdrOnline(online bool, pong Pong)
}

// Syncer maintains a live, debounced snapshot of all agent terminals in herdr-core.
type Syncer struct {
	Client       *Client
	Listener     Listener
	Logger       *slog.Logger
	Debounce     time.Duration
	PollHealthy  time.Duration
	PollDegraded time.Duration

	clock  clock                             // nil means realClock
	spread func(time.Duration) time.Duration // nil means ±10% jitter

	fetchMu sync.Mutex // serializes Refresh calls so lists apply in fetch order

	mu        sync.RWMutex
	snapshot  map[string]AgentInfo
	order     []string // pane_ids in herdr-core's terminal.list order
	online    bool
	pong      Pong
	listFails int // consecutive Refresh failures; 0 when healthy

	cbMu sync.Mutex // serializes Listener callbacks so they never overlap
}

func (s *Syncer) initDefaults() {
	if s.Debounce <= 0 {
		s.Debounce = 60 * time.Millisecond
	}
	if s.PollHealthy <= 0 {
		s.PollHealthy = 30 * time.Second
	}
	if s.PollDegraded <= 0 {
		s.PollDegraded = 2 * time.Second
	}
	if s.clock == nil {
		s.clock = realClock{}
	}
	if s.spread == nil {
		s.spread = randomJitter
	}
	s.mu.Lock()
	if s.snapshot == nil {
		s.snapshot = make(map[string]AgentInfo)
	}
	s.mu.Unlock()
}

// Snapshot returns a copy of the currently known active agent panes, in the
// order herdr-core's terminal.list reported them.
func (s *Syncer) Snapshot() []AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]AgentInfo, 0, len(s.order))
	for _, id := range s.order {
		if info, ok := s.snapshot[id]; ok {
			res = append(res, info)
		}
	}
	return res
}

// Refresh forces an immediate terminal.list call, diffs against the snapshot,
// and notifies the listener of any changes. Overlapping calls are serialized so an
// older list can never overwrite a newer one.
func (s *Syncer) Refresh(ctx context.Context) ([]AgentInfo, error) {
	if err := s.lockFetch(ctx); err != nil {
		return nil, err
	}
	defer s.fetchMu.Unlock()

	agents, err := s.Client.ListAgents(ctx)
	if err != nil {
		s.noteListResult(err)
		return nil, err
	}
	s.noteListResult(nil)

	s.applyList(agents, false)
	return s.Snapshot(), nil
}

// lockFetch acquires fetchMu or returns ctx.Err() if ctx expires first.
func (s *Syncer) lockFetch(ctx context.Context) error {
	if s.fetchMu.TryLock() {
		return nil
	}
	acquired := make(chan struct{})
	go func() {
		s.fetchMu.Lock()
		close(acquired)
	}()
	select {
	case <-acquired:
		return nil
	case <-ctx.Done():
		go func() {
			<-acquired
			s.fetchMu.Unlock()
		}()
		return ctx.Err()
	}
}

// noteListResult logs the first failure of a streak at Warn and recovery at Info.
func (s *Syncer) noteListResult(err error) {
	s.mu.Lock()
	if err != nil {
		s.listFails++
		first := s.listFails == 1
		s.mu.Unlock()
		if first && s.Logger != nil && s.Client != nil {
			s.Logger.Warn("herdr terminal.list failed", "socket", s.Client.SocketPath, "err", err)
		}
		return
	}
	failed := s.listFails
	s.listFails = 0
	s.mu.Unlock()
	if failed > 0 && s.Logger != nil && s.Client != nil {
		s.Logger.Info("herdr terminal.list recovered", "socket", s.Client.SocketPath, "failed_attempts", failed)
	}
}

// applyList updates the snapshot, keeping only agent panes (Agent != nil && *Agent != "").
// When replaceAll is false, a pane's state never moves backwards in StateChangeSeq.
func (s *Syncer) applyList(agents []AgentInfo, replaceAll bool) {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()

	next := make(map[string]AgentInfo, len(agents))
	order := make([]string, 0, len(agents))
	s.mu.Lock()
	for _, a := range agents {
		if a.Agent != nil && *a.Agent != "" {
			if _, seen := next[a.PaneID]; !seen {
				order = append(order, a.PaneID)
			}
			prev, havePrev := s.snapshot[a.PaneID]
			if replaceAll || !havePrev || a.StateChangeSeq >= prev.StateChangeSeq {
				next[a.PaneID] = a
			} else {
				next[a.PaneID] = prev
			}
		}
	}

	changes := diff(s.snapshot, next, order)
	s.snapshot = next
	s.order = order
	s.mu.Unlock()

	if len(changes) > 0 && s.Listener != nil {
		s.Listener.OnChanges(changes)
	}
}

func diff(oldMap, newMap map[string]AgentInfo, newOrder []string) []Change {
	var changes []Change

	for _, id := range newOrder {
		curr, ok := newMap[id]
		if !ok {
			continue
		}
		prev, existed := oldMap[id]
		if !existed {
			changes = append(changes, Change{
				Kind:  Added,
				Agent: curr,
			})
		} else if agentChanged(prev, curr) {
			prevCopy := prev
			changes = append(changes, Change{
				Kind:  Updated,
				Agent: curr,
				Prev:  &prevCopy,
			})
		}
	}

	for id, prev := range oldMap {
		if _, exists := newMap[id]; !exists {
			prevCopy := prev
			changes = append(changes, Change{
				Kind:  Removed,
				Agent: prev,
				Prev:  &prevCopy,
			})
		}
	}

	return changes
}

func agentChanged(prev, curr AgentInfo) bool {
	return prev.AgentStatus != curr.AgentStatus ||
		prev.StateChangeSeq != curr.StateChangeSeq ||
		prev.WorkspaceID != curr.WorkspaceID ||
		prev.WorkspaceLabel != curr.WorkspaceLabel ||
		prev.TabID != curr.TabID ||
		prev.TabLabel != curr.TabLabel ||
		prev.StatusMsg != curr.StatusMsg ||
		!ptrEqual(prev.StatusKind, curr.StatusKind) ||
		!ptrEqual(prev.Title, curr.Title) ||
		!ptrEqual(prev.Name, curr.Name) ||
		!ptrEqual(prev.Agent, curr.Agent) ||
		!intPtrEqual(prev.SubagentCount, curr.SubagentCount) ||
		!intPtrEqual(prev.BackgroundShellCount, curr.BackgroundShellCount) ||
		!intPtrEqual(prev.BackgroundMonitorCount, curr.BackgroundMonitorCount) ||
		!sessionEqual(prev.AgentSession, curr.AgentSession)
}

func ptrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func intPtrEqual(a, b *int) bool {
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

// Run starts the main synchronization loop until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) error {
	s.initDefaults()

	var (
		backoff      = 500 * time.Millisecond
		maxBackoff   = 30 * time.Second
		reportedDown = false // true after emitting a false OnHerdrOnline
	)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		pong, err := s.Client.Ping(ctx)
		if err != nil {
			s.setOnline(false, Pong{}, !reportedDown)
			reportedDown = true
			if err := s.sleep(ctx, s.spread(backoff)); err != nil {
				return err
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		agents, err := s.listForConnect(ctx)
		if err != nil {
			s.setOnline(false, Pong{}, !reportedDown)
			reportedDown = true
			if err := s.sleep(ctx, s.spread(backoff)); err != nil {
				return err
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		backoff = 500 * time.Millisecond
		reportedDown = false
		s.setOnline(true, pong, true)
		s.applyList(agents, true)

		s.streamLoop(ctx)
	}
}

func (s *Syncer) listForConnect(ctx context.Context) ([]AgentInfo, error) {
	if err := s.lockFetch(ctx); err != nil {
		return nil, err
	}
	defer s.fetchMu.Unlock()
	agents, err := s.Client.ListAgents(ctx)
	s.noteListResult(err)
	return agents, err
}

func (s *Syncer) setOnline(online bool, pong Pong, forceNotify bool) {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()

	s.mu.Lock()
	changed := s.online != online || (online && s.pong != pong)
	s.online = online
	s.pong = pong
	s.mu.Unlock()

	if (changed || forceNotify) && s.Listener != nil {
		s.Listener.OnHerdrOnline(online, pong)
	}
}

func (s *Syncer) streamLoop(ctx context.Context) {
	const (
		minStreamBackoff = 500 * time.Millisecond
		maxStreamBackoff = 30 * time.Second
	)
	var (
		stream        <-chan Event
		streamBackoff = minStreamBackoff
		retryTimer    timer
		retryCh       <-chan time.Time
	)
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()

	subscribe := func() bool {
		ch, err := s.Client.Subscribe(ctx)
		if err != nil {
			return false
		}
		stream = ch
		// Close the race window between the initial list and stream start.
		if _, err := s.Refresh(ctx); err != nil {
			return false
		}
		return true
	}

	armRetry := func() {
		d := s.spread(streamBackoff)
		streamBackoff = min(streamBackoff*2, maxStreamBackoff)
		if retryTimer == nil {
			retryTimer = s.clock.NewTimer(d)
		} else {
			retryTimer.Reset(d)
		}
		retryCh = retryTimer.C()
	}

	if !subscribe() {
		if _, err := s.Client.Ping(ctx); err != nil {
			return
		}
		armRetry()
	}

	pollInterval := func() time.Duration {
		if stream != nil {
			return s.PollHealthy
		}
		return s.PollDegraded
	}
	pollTimer := s.clock.NewTimer(pollInterval())
	defer pollTimer.Stop()

	var (
		debounceTimer timer
		debounceCh    <-chan time.Time
	)
	defer func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
	}()
	armDebounce := func() {
		if debounceTimer == nil {
			debounceTimer = s.clock.NewTimer(s.Debounce)
			debounceCh = debounceTimer.C()
		}
	}
	stopDebounce := func() {
		if debounceTimer != nil {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C():
				default:
				}
			}
			debounceTimer = nil
			debounceCh = nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case _, ok := <-stream:
			if !ok {
				stream = nil
				stopDebounce()
				if _, err := s.Refresh(ctx); err != nil {
					if _, pingErr := s.Client.Ping(ctx); pingErr != nil {
						return
					}
				}
				armRetry()
				pollTimer.Reset(pollInterval())
				continue
			}
			streamBackoff = minStreamBackoff
			armDebounce()

		case <-debounceCh:
			debounceTimer = nil
			debounceCh = nil
			if _, err := s.Refresh(ctx); err != nil {
				if _, pingErr := s.Client.Ping(ctx); pingErr != nil {
					return
				}
			}
			pollTimer.Reset(pollInterval())

		case <-retryCh:
			retryCh = nil
			if subscribe() {
				pollTimer.Reset(pollInterval())
				continue
			}
			if _, err := s.Client.Ping(ctx); err != nil {
				return
			}
			armRetry()

		case <-pollTimer.C():
			if _, err := s.Refresh(ctx); err != nil {
				if _, pingErr := s.Client.Ping(ctx); pingErr != nil {
					return
				}
			}
			pollTimer.Reset(pollInterval())
		}
	}
}

func (s *Syncer) sleep(ctx context.Context, d time.Duration) error {
	t := s.clock.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C():
		return nil
	}
}
