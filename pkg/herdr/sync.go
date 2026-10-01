package herdr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
//
// Callbacks are never run concurrently with each other, and batches arrive in
// the order their lists were fetched. A callback must not call Syncer.Refresh
// synchronously (it would wait for itself); start a goroutine instead. For
// the same reason, never call Refresh while holding a lock a callback takes.
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

	initOnce sync.Once
	// turn serializes list → diff → apply → Listener, so snapshots are
	// applied in the order they were fetched and callbacks never overlap.
	// A channel instead of a mutex so waiting honours ctx.
	turn chan struct{}
	// paneSetChanged is signalled whenever an applied list changes the set of
	// agent panes, whoever called Refresh, so Run can resubscribe.
	paneSetChanged chan struct{}

	listFailures int // consecutive agent.list failures; guarded by turn

	mu          sync.Mutex
	current     map[string]AgentInfo // pane_id -> AgentInfo
	list        []AgentInfo          // same agents, in herdr's order
	online      bool
	onlineKnown bool
	lastPong    Pong

	// Test hooks, set before Run: the clock timers come from and the jitter
	// applied to every backoff step.
	clock  clock
	spread func(time.Duration) time.Duration
}

type syncConfig struct {
	debounce     time.Duration
	pollHealthy  time.Duration
	pollDegraded time.Duration
}

func (s *Syncer) init() {
	s.initOnce.Do(func() {
		s.turn = make(chan struct{}, 1)
		s.paneSetChanged = make(chan struct{}, 1)
	})
}

func (s *Syncer) config() syncConfig {
	cfg := syncConfig{debounce: s.Debounce, pollHealthy: s.PollHealthy, pollDegraded: s.PollDegraded}
	if cfg.debounce <= 0 {
		cfg.debounce = 150 * time.Millisecond
	}
	if cfg.pollHealthy <= 0 {
		cfg.pollHealthy = 15 * time.Second
	}
	if cfg.pollDegraded <= 0 {
		cfg.pollDegraded = 2 * time.Second
	}
	return cfg
}

func (s *Syncer) clk() clock {
	if s.clock != nil {
		return s.clock
	}
	return realClock{}
}

func (s *Syncer) newBackoff() *backoff {
	spread := s.spread
	if spread == nil {
		spread = randomJitter
	}
	return &backoff{spread: spread}
}

func (s *Syncer) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.New(discardHandler{})
}

// Run blocks until ctx is cancelled, maintaining the sync loop against herdr.
// It never returns an error when herdr is down; it keeps retrying with backoff.
//
//	ping ─fail→ OnHerdrOnline(false) once, back off 500ms…30s (±20%), ping again
//	  │ok → OnHerdrOnline(true) once per transition
//	list, subscribe (then list again, to cover the gap)
//	  event          → debounced list
//	  poll           → list every PollHealthy (stream up) / PollDegraded (down)
//	  pane set moved → resubscribe at once
//	  stream drop    → list, then resubscribe after a backoff
//	  herdr gone     → back to ping
func (s *Syncer) Run(ctx context.Context) error {
	s.init()
	cfg := s.config()
	retry := s.newBackoff()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		pong, err := s.Client.Ping(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.setOnline(ctx, false, Pong{}, err)
			if !s.sleep(ctx, retry.Next()) {
				return ctx.Err()
			}
			continue
		}

		s.setOnline(ctx, true, pong, nil)
		if s.runOnline(ctx, cfg) {
			retry.Reset()
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// herdr answers ping but never managed a list: pace the retries.
		if !s.sleep(ctx, retry.Next()) {
			return ctx.Err()
		}
	}
}

// runOnline keeps the agent list in sync while herdr answers. It returns when
// ctx ends or herdr stops answering; listed reports whether any agent.list
// succeeded meanwhile.
func (s *Syncer) runOnline(ctx context.Context, cfg syncConfig) (listed bool) {
	clk := s.clk()
	log := s.log()

	// relist re-lists herdr; false means herdr looks gone.
	relist := func() bool {
		_, err := s.Refresh(ctx)
		if err == nil {
			listed = true
			return true
		}
		return !s.offline(ctx, err)
	}

	if !relist() {
		return listed
	}

	var (
		stream      <-chan Event
		cancelSub   = func() {}
		subscribed  map[string]bool // the pane set the open stream covers
		dialNow     = true
		debouncing  = false
		subFailing  = false
		streamRetry = s.newBackoff()
	)
	closeStream := func() {
		cancelSub()
		cancelSub = func() {}
		stream = nil
		subscribed = nil
	}
	defer func() { cancelSub() }()

	retryT := newStoppedTimer(clk)
	defer stopTimer(retryT)
	debounceT := newStoppedTimer(clk)
	defer stopTimer(debounceT)
	pollT := clk.NewTimer(cfg.pollDegraded)
	defer stopTimer(pollT)

	for {
		if stream == nil && dialNow {
			dialNow = false
			st, cancel, set, err := s.openStream(ctx)
			switch {
			case err == nil:
				stream, cancelSub, subscribed = st, cancel, set
				if subFailing {
					log.Info("herdr: event stream restored")
					subFailing = false
				}
				resetTimer(pollT, cfg.pollHealthy)
				// List once more: anything that changed between the last
				// list and the subscription produced no event.
				debouncing = true
				resetTimer(debounceT, cfg.debounce)
			case ctx.Err() != nil:
				return listed
			case s.offline(ctx, err):
				return listed
			default:
				d := streamRetry.Next()
				if !subFailing {
					log.Warn("herdr: events.subscribe failed; polling until it works", "err", err, "retry_in", d)
					subFailing = true
				} else {
					log.Debug("herdr: events.subscribe failed again", "err", err, "retry_in", d)
				}
				resetTimer(retryT, d)
			}
		}

		select {
		case <-ctx.Done():
			return listed

		case <-retryT.C():
			dialNow = true

		case _, ok := <-stream:
			if !ok {
				first := streamRetry.Fresh()
				closeStream()
				stopTimer(debounceT)
				debouncing = false
				d := streamRetry.Next()
				if first {
					log.Info("herdr: event stream closed; re-listing and polling until it is back", "retry_in", d)
				} else {
					log.Debug("herdr: event stream closed again", "retry_in", d)
				}
				if !relist() {
					return listed
				}
				resetTimer(retryT, d)
				resetTimer(pollT, cfg.pollDegraded)
				continue
			}
			if !debouncing {
				debouncing = true
				resetTimer(debounceT, cfg.debounce)
			}

		case <-debounceT.C():
			debouncing = false
			if !relist() {
				return listed
			}

		case <-pollT.C():
			interval := cfg.pollDegraded
			if stream != nil {
				// The stream survived a whole healthy interval.
				streamRetry.Reset()
				interval = cfg.pollHealthy
			}
			if !relist() {
				return listed
			}
			resetTimer(pollT, interval)

		case <-s.paneSetChanged:
			if stream != nil && !setsEqual(s.paneIDSet(), subscribed) {
				log.Debug("herdr: agent panes changed; resubscribing")
				closeStream()
				stopTimer(retryT)
				dialNow = true
			}
		}
	}
}

// openStream subscribes for the agents currently known and returns the pane
// set the subscription covers.
func (s *Syncer) openStream(ctx context.Context) (<-chan Event, context.CancelFunc, map[string]bool, error) {
	subs, set := subscriptionsFor(s.Snapshot())
	subCtx, cancel := context.WithCancel(ctx)
	stream, err := s.Client.Subscribe(subCtx, subs)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	return stream, cancel, set, nil
}

// offline decides whether err means herdr is gone (or ctx is done).
func (s *Syncer) offline(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, ErrUnavailable) {
		return true
	}
	var herdrErr *Error
	if errors.As(err, &herdrErr) {
		return false // herdr answered, just not with what we wanted
	}
	// Transport trouble (timeout, reset, garbage): ask herdr directly.
	_, pingErr := s.Client.Ping(ctx)
	return pingErr != nil
}

// setOnline records herdr's reachability and tells the Listener once per
// transition, including the first observation (so "offline at startup" is
// reported too). Only Run calls it.
func (s *Syncer) setOnline(ctx context.Context, online bool, pong Pong, cause error) {
	s.mu.Lock()
	changed := !s.onlineKnown || s.online != online
	s.onlineKnown = true
	s.online = online
	if online {
		s.lastPong = pong
	}
	s.mu.Unlock()
	if !changed {
		return
	}

	if online {
		s.log().Info("herdr: online", "version", pong.Version, "protocol", pong.Protocol)
	} else {
		s.log().Warn("herdr: offline; retrying with backoff", "err", cause)
	}
	if s.Listener == nil {
		return
	}
	if s.acquire(ctx) != nil {
		return // shutting down
	}
	defer s.release()
	s.Listener.OnHerdrOnline(online, pong)
}

// sleep waits d on the Syncer's clock; false if ctx ended first.
func (s *Syncer) sleep(ctx context.Context, d time.Duration) bool {
	t := s.clk().NewTimer(d)
	defer stopTimer(t)
	select {
	case <-t.C():
		return true
	case <-ctx.Done():
		return false
	}
}

// Snapshot returns the last known agent list (copy), in herdr's order. Safe for concurrent use.
func (s *Syncer) Snapshot() []AgentInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]AgentInfo, len(s.list))
	copy(res, s.list)
	return res
}

// Refresh forces an immediate re-list (used by the bridge right before executing a command).
//
// Refreshes are serialized with each other and with Run's own lists: each one
// fetches, diffs, applies and notifies the Listener before the next starts, so
// an older list can never overwrite a newer one. It returns the (plain-shell
// filtered) list it applied, in herdr's order. Waiting for an in-flight
// refresh honours ctx.
func (s *Syncer) Refresh(ctx context.Context) ([]AgentInfo, error) {
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()

	agents, err := s.Client.ListAgents(ctx)
	if err != nil {
		s.noteListFailure(ctx, err)
		return nil, err
	}
	s.noteListSuccess()
	return s.apply(agents), nil
}

func (s *Syncer) acquire(ctx context.Context) error {
	s.init()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("herdr refresh: %w", err)
	}
	select {
	case s.turn <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("herdr refresh: %w", ctx.Err())
	}
}

func (s *Syncer) release() { <-s.turn }

// noteListFailure logs a failed agent.list once per streak. Caller holds the turn.
func (s *Syncer) noteListFailure(ctx context.Context, err error) {
	log := s.log()
	switch {
	case ctx.Err() != nil:
		log.Debug("herdr: agent.list cancelled", "err", err)
	case errors.Is(err, ErrUnavailable):
		log.Debug("herdr: agent.list failed; herdr unavailable", "err", err) // Run reports offline
	case s.listFailures == 0:
		s.listFailures++
		log.Warn("herdr: agent.list failed", "err", err)
	default:
		s.listFailures++
		log.Debug("herdr: agent.list failed again", "err", err, "failures", s.listFailures)
	}
}

// noteListSuccess ends a failure streak. Caller holds the turn.
func (s *Syncer) noteListSuccess() {
	if s.listFailures > 0 {
		s.log().Info("herdr: agent.list recovered", "failures", s.listFailures)
		s.listFailures = 0
	}
}

// apply diffs raw against the current list, stores it, signals a pane-set
// change and notifies the Listener. Caller holds the turn.
func (s *Syncer) apply(raw []AgentInfo) []AgentInfo {
	list := make([]AgentInfo, 0, len(raw))
	newMap := make(map[string]AgentInfo, len(raw))
	for _, a := range raw {
		// Plain shells are not agents.
		if a.Agent == nil && a.AgentStatus == "unknown" {
			continue
		}
		if _, dup := newMap[a.PaneID]; dup {
			continue
		}
		newMap[a.PaneID] = a
		list = append(list, a)
	}

	s.mu.Lock()
	oldMap, oldList := s.current, s.list
	var changes []Change
	for _, a := range list {
		old, exists := oldMap[a.PaneID]
		switch {
		case !exists:
			changes = append(changes, Change{Kind: Added, Agent: a})
		case agentDiffers(old, a):
			prev := old
			changes = append(changes, Change{Kind: Updated, Agent: a, Prev: &prev})
		}
	}
	for _, old := range oldList {
		if _, exists := newMap[old.PaneID]; !exists {
			prev := old
			changes = append(changes, Change{Kind: Removed, Agent: old, Prev: &prev})
		}
	}
	paneSetMoved := len(oldMap) != len(newMap)
	if !paneSetMoved {
		for id := range newMap {
			if _, ok := oldMap[id]; !ok {
				paneSetMoved = true
				break
			}
		}
	}
	s.current = newMap
	s.list = list
	s.mu.Unlock()

	if paneSetMoved {
		select {
		case s.paneSetChanged <- struct{}{}:
		default: // a signal is already pending
		}
	}
	if len(changes) > 0 && s.Listener != nil {
		s.Listener.OnChanges(changes)
	}

	out := make([]AgentInfo, len(list))
	copy(out, list)
	return out
}

// subscriptionsFor returns the subscriptions for agents and the pane set they
// cover: the global pane events plus one status subscription per agent pane.
func subscriptionsFor(agents []AgentInfo) ([]Subscription, map[string]bool) {
	subs := []Subscription{
		{Type: "pane.created"},
		{Type: "pane.closed"},
		{Type: "pane.exited"},
		{Type: "pane.agent_detected"},
	}
	set := make(map[string]bool, len(agents))
	for _, a := range agents {
		subs = append(subs, Subscription{
			Type:   "pane.agent_status_changed",
			PaneID: a.PaneID,
		})
		set[a.PaneID] = true
	}
	return subs, set
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
		a.TabID != b.TabID ||
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

// discardHandler drops every record (slog.DiscardHandler needs Go 1.24).
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }
