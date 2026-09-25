package herdr_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
)

// never is a poll interval that no test reaches: it proves an update came
// through the event path, not through polling.
const never = time.Hour

type testListener struct {
	mu      sync.Mutex
	changes [][]herdr.Change
	onlines []bool
	pongs   []herdr.Pong

	inFlight atomic.Int32
	overlap  atomic.Bool // set if two callbacks ever ran at once
}

func (l *testListener) enter() func() {
	if l.inFlight.Add(1) > 1 {
		l.overlap.Store(true)
	}
	return func() { l.inFlight.Add(-1) }
}

func (l *testListener) OnChanges(changes []herdr.Change) {
	defer l.enter()()
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]herdr.Change, len(changes))
	copy(cp, changes)
	l.changes = append(l.changes, cp)
}

func (l *testListener) OnHerdrOnline(online bool, pong herdr.Pong) {
	defer l.enter()()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onlines = append(l.onlines, online)
	l.pongs = append(l.pongs, pong)
}

func (l *testListener) allChanges() []herdr.Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []herdr.Change
	for _, batch := range l.changes {
		out = append(out, batch...)
	}
	return out
}

func (l *testListener) onlinesList() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]bool, len(l.onlines))
	copy(out, l.onlines)
	return out
}

func (l *testListener) hasChange(kind herdr.ChangeKind, paneID, status string) bool {
	for _, c := range l.allChanges() {
		if c.Kind == kind && c.Agent.PaneID == paneID && (status == "" || c.Agent.AgentStatus == status) {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	waitUntilMsg(t, timeout, cond, func() string { return "" })
}

func waitUntilMsg(t *testing.T, timeout time.Duration, cond func() bool, msg func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v %s", timeout, msg())
}

func testClient(srv *herdrtest.Server) *herdr.Client {
	return &herdr.Client{SocketPath: srv.SocketPath, Timeout: 2 * time.Second}
}

// runSyncer starts Run and, at cleanup, cancels it and requires it to return.
func runSyncer(t *testing.T, s *herdr.Syncer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("Syncer.Run did not return after ctx was cancelled")
		}
	})
}

func agentRow(paneID, status string, seq int) map[string]any {
	return map[string]any{"pane_id": paneID, "agent": "claude", "agent_status": status, "state_change_seq": seq}
}

func callsOf(srv *herdrtest.Server, method string) int {
	n := 0
	for _, c := range srv.Calls() {
		if c.Method == method {
			n++
		}
	}
	return n
}

// subscribedPanes returns the pane ids of the status subscriptions in a call.
func subscribedPanes(c herdrtest.Call) []string {
	var out []string
	raw, _ := c.Params["subscriptions"].([]any)
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			if p, _ := m["pane_id"].(string); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// subscribedTo reports whether herdr got a subscribe covering paneID.
func subscribedTo(srv *herdrtest.Server, paneID string) bool {
	for _, c := range srv.Calls() {
		if c.Method == "events.subscribe" && slices.Contains(subscribedPanes(c), paneID) {
			return true
		}
	}
	return false
}

// listedAfterSubscribeTo reports whether herdr got a subscribe covering
// paneID and, after it, an agent.list: the stream is up and the list that
// follows every (re)subscribe is done, so any later list is event-driven.
func listedAfterSubscribeTo(srv *herdrtest.Server, paneID string) bool {
	subscribed := false
	for _, c := range srv.Calls() {
		switch {
		case c.Method == "events.subscribe" && slices.Contains(subscribedPanes(c), paneID):
			subscribed = true
		case c.Method == "agent.list" && subscribed:
			return true
		}
	}
	return false
}

func waitArmed(t *testing.T, clk *herdr.FakeClock, d time.Duration, msg func() string) {
	t.Helper()
	waitUntilMsg(t, 2*time.Second, func() bool { return slices.Contains(clk.Armed(), d) },
		func() string { return fmt.Sprintf("waiting for a %v timer (armed: %v) %s", d, clk.Armed(), msg()) })
}

func TestSyncerAddUpdateRemove(t *testing.T) {
	srv := herdrtest.New(t)
	listener := &testListener{}
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}

	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	runSyncer(t, syncer)

	// 1. Initial list → Added.
	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		return len(changes) == 1 && changes[0].Kind == herdr.Added && changes[0].Agent.PaneID == "w1:p1"
	})
	waitUntil(t, 2*time.Second, func() bool { return listedAfterSubscribeTo(srv, "w1:p1") })

	// 2. Status change → the event triggers a re-list → Updated.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "working", 2)})
	srv.EmitStatusChanged("w1:p1", "working")
	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		last := changes[len(changes)-1]
		return last.Kind == herdr.Updated && last.Agent.AgentStatus == "working" && last.Prev != nil && last.Prev.AgentStatus == "idle"
	})

	// 3. Pane closed → event → Removed.
	srv.SetAgents([]map[string]any{})
	srv.EmitGlobal("pane.closed", map[string]any{"pane_id": "w1:p1"})
	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		last := changes[len(changes)-1]
		return last.Kind == herdr.Removed && last.Agent.PaneID == "w1:p1"
	})
}

func TestSyncerResubscribesOnNewPane(t *testing.T) {
	srv := herdrtest.New(t)
	listener := &testListener{}
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}

	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	runSyncer(t, syncer)
	waitUntil(t, 2*time.Second, func() bool { return listedAfterSubscribeTo(srv, "w1:p1") })

	// A second agent pane appears; only the pane.created event can reveal it.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1), agentRow("w1:p2", "idle", 1)})
	srv.EmitGlobal("pane.created", map[string]any{"pane_id": "w1:p2"})

	waitUntil(t, 2*time.Second, func() bool { return subscribedTo(srv, "w1:p2") })
}

// A pane first seen by somebody else's Refresh (the bridge re-lists after
// every command) must still get its status subscription.
func TestSyncerSubscribesPaneFoundByAnotherRefresh(t *testing.T) {
	srv := herdrtest.New(t)
	listener := &testListener{}
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}

	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	runSyncer(t, syncer)
	waitUntil(t, 2*time.Second, func() bool { return listedAfterSubscribeTo(srv, "w1:p1") })

	// herdr says nothing; an external Refresh discovers w1:p2.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1), agentRow("w1:p2", "idle", 1)})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := syncer.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	waitUntilMsg(t, 2*time.Second, func() bool { return listedAfterSubscribeTo(srv, "w1:p2") },
		func() string { return "(w1:p2 never got a pane.agent_status_changed subscription)" })

	// Its status events now reach the Syncer.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1), agentRow("w1:p2", "working", 2)})
	srv.EmitStatusChanged("w1:p2", "working")
	waitUntil(t, 2*time.Second, func() bool { return listener.hasChange(herdr.Updated, "w1:p2", "working") })
}

func TestSyncerIgnoresPlainShells(t *testing.T) {
	srv := herdrtest.New(t)
	listener := &testListener{}
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}

	srv.SetAgents([]map[string]any{
		agentRow("w1:p1", "idle", 1),
		{"pane_id": "w1:shell", "agent": nil, "agent_status": "unknown"}, // plain shell
	})
	runSyncer(t, syncer)

	waitUntil(t, 2*time.Second, func() bool { return len(listener.allChanges()) == 1 })

	changes := listener.allChanges()
	if changes[0].Agent.PaneID != "w1:p1" {
		t.Errorf("expected only w1:p1 in changes, got: %s", changes[0].Agent.PaneID)
	}

	snap := syncer.Snapshot()
	if len(snap) != 1 || snap[0].PaneID != "w1:p1" {
		t.Errorf("expected snapshot to contain only w1:p1, got: %v", snap)
	}
	for _, c := range srv.Calls() {
		if slices.Contains(subscribedPanes(c), "w1:shell") {
			t.Errorf("subscribed to status events of a plain shell: %v", c.Params)
		}
	}
}

func TestSyncerOfflineOnline(t *testing.T) {
	srv := herdrtest.New(t)
	listener := &testListener{}
	clk := herdr.NewFakeClock()
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}
	herdr.UseFakeClock(syncer, clk)

	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	runSyncer(t, syncer)
	waitUntil(t, 2*time.Second, func() bool {
		return slices.Equal(listener.onlinesList(), []bool{true}) && listener.hasChange(herdr.Added, "w1:p1", "")
	})

	// herdr goes away: the stream drops, the re-list fails, Ping fails.
	srv.Stop()
	onlines := func() string { return fmt.Sprintf("(onlines %v)", listener.onlinesList()) }
	waitArmed(t, clk, 500*time.Millisecond, onlines)
	if got := listener.onlinesList(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("onlines = %v, want [true false]", got)
	}

	// Two more failed pings (backoff 500ms, then 1s): still exactly one false.
	clk.Advance(500 * time.Millisecond)
	waitArmed(t, clk, time.Second, onlines)
	clk.Advance(time.Second)
	waitArmed(t, clk, 2*time.Second, onlines)
	if got := listener.onlinesList(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("onlines = %v after repeated failed pings, want exactly one false", got)
	}

	// herdr comes back with a different herd.
	srv.SetAgents([]map[string]any{agentRow("w1:p2", "working", 5)})
	listsBefore := callsOf(srv, "agent.list")
	srv.Start()
	clk.Advance(2 * time.Second)

	waitUntilMsg(t, 2*time.Second, func() bool {
		return slices.Equal(listener.onlinesList(), []bool{true, false, true}) &&
			listener.hasChange(herdr.Removed, "w1:p1", "") &&
			listener.hasChange(herdr.Added, "w1:p2", "working")
	}, func() string {
		return fmt.Sprintf("(onlines %v, changes %d)", listener.onlinesList(), len(listener.allChanges()))
	})

	if callsOf(srv, "agent.list") <= listsBefore {
		t.Error("no agent.list after herdr came back")
	}
	snap := syncer.Snapshot()
	if len(snap) != 1 || snap[0].PaneID != "w1:p2" {
		t.Errorf("snapshot after reconnect = %v, want exactly w1:p2", snap)
	}
	waitUntil(t, 2*time.Second, func() bool { return subscribedTo(srv, "w1:p2") })
	if listener.overlap.Load() {
		t.Error("listener callbacks overlapped")
	}
}

func TestSyncerReportsOfflineAtStartup(t *testing.T) {
	srv := herdrtest.New(t)
	srv.Stop() // herdr is not running when the bridge starts
	listener := &testListener{}
	clk := herdr.NewFakeClock()
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}
	herdr.UseFakeClock(syncer, clk)
	runSyncer(t, syncer)

	onlines := func() string { return fmt.Sprintf("(onlines %v)", listener.onlinesList()) }
	waitArmed(t, clk, 500*time.Millisecond, onlines)
	clk.Advance(500 * time.Millisecond)
	waitArmed(t, clk, time.Second, onlines)
	if got := listener.onlinesList(); !slices.Equal(got, []bool{false}) {
		t.Fatalf("onlines = %v, want exactly one false while herdr is down at startup", got)
	}

	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	srv.Start()
	clk.Advance(time.Second)
	waitUntil(t, 2*time.Second, func() bool {
		return slices.Equal(listener.onlinesList(), []bool{false, true}) && listener.hasChange(herdr.Added, "w1:p1", "")
	})
}

// A stream that herdr acks and closes at once must not be resubscribed in a
// hot loop: every drop re-lists, then waits 500ms, 1s, 2s … capped at 30s.
func TestSyncerStreamDropRelistsThenBacksOff(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	srv.SetDropStreamsAfterAck(true)
	clk := herdr.NewFakeClock()
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     &testListener{},
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: never,
	}
	herdr.UseFakeClock(syncer, clk)
	runSyncer(t, syncer)

	subs := func() string { return fmt.Sprintf("(%d events.subscribe so far)", callsOf(srv, "events.subscribe")) }
	steps := []time.Duration{
		500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	for i, d := range steps {
		waitArmed(t, clk, d, subs)
		calls := srv.Calls()
		if n := callsOf(srv, "events.subscribe"); n != i+1 {
			t.Fatalf("before backoff step %d (%v): %d subscribes, want %d", i, d, n, i+1)
		}
		if last := calls[len(calls)-1]; last.Method != "agent.list" {
			t.Fatalf("after drop %d the last call was %s; want the agent.list re-list", i+1, last.Method)
		}
		clk.Advance(d)
	}
	waitArmed(t, clk, 30*time.Second, subs)
	if n := callsOf(srv, "events.subscribe"); n != len(steps)+1 {
		t.Errorf("%d subscribes, want %d", n, len(steps)+1)
	}
}

// While the stream is down, agent.list is polled every PollDegraded and the
// stream is retried only when its backoff elapses.
func TestSyncerPollsDegradedWhileStreamDown(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	srv.SetDropStreamsAfterAck(true)
	clk := herdr.NewFakeClock()
	listener := &testListener{}
	const degraded = 100 * time.Millisecond
	syncer := &herdr.Syncer{
		Client:       testClient(srv),
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  never,
		PollDegraded: degraded,
	}
	herdr.UseFakeClock(syncer, clk)
	runSyncer(t, syncer)

	subs := func() string { return fmt.Sprintf("(%d subscribes)", callsOf(srv, "events.subscribe")) }
	waitArmed(t, clk, 500*time.Millisecond, subs) // first drop: retry in 500ms
	waitArmed(t, clk, degraded, subs)
	lists := callsOf(srv, "agent.list")

	// Four degraded polls fit before the retry is due.
	for i := 1; i <= 4; i++ {
		clk.Advance(degraded)
		waitUntilMsg(t, 2*time.Second, func() bool {
			return callsOf(srv, "agent.list") == lists+i && slices.Contains(clk.Armed(), degraded)
		}, subs)
		if n := callsOf(srv, "events.subscribe"); n != 1 {
			t.Fatalf("poll %d: %d subscribes, want 1 until the 500ms backoff elapses", i, n)
		}
	}

	// A change made while degraded is picked up by the next poll.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "blocked", 2)})
	clk.Advance(degraded) // t=500ms: the poll and the stream retry are both due
	waitUntilMsg(t, 2*time.Second, func() bool {
		return listener.hasChange(herdr.Updated, "w1:p1", "blocked") && callsOf(srv, "events.subscribe") == 2
	}, subs)
}

// A subscribe that herdr accepts but never acks must not keep Run from
// returning when ctx is cancelled.
func TestSyncerShutdownWithHungSubscribe(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	hold := srv.HoldNext("events.subscribe")
	syncer := &herdr.Syncer{
		Client:       &herdr.Client{SocketPath: srv.SocketPath, Timeout: time.Minute},
		Listener:     &testListener{},
		PollHealthy:  never,
		PollDegraded: never,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = syncer.Run(ctx)
	}()
	select {
	case <-hold.Received():
	case <-time.After(2 * time.Second):
		t.Fatal("Syncer never subscribed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run still blocked in a subscribe that never acks after ctx was cancelled")
	}
}

// Overlapping Refresh calls must apply lists in the order they were fetched:
// an older list can never overwrite a newer one.
func TestRefreshAppliesListsInFetchOrder(t *testing.T) {
	srv := herdrtest.New(t)
	listener := &testListener{}
	syncer := &herdr.Syncer{
		Client:   &herdr.Client{SocketPath: srv.SocketPath, Timeout: 10 * time.Second},
		Listener: listener,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type result struct {
		agents []herdr.AgentInfo
		err    error
	}
	refresh := func() <-chan result {
		ch := make(chan result, 1)
		go func() {
			agents, err := syncer.Refresh(ctx)
			ch <- result{agents, err}
		}()
		return ch
	}

	// A lists while the pane is blocked; its answer is held in flight.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "blocked", 1)})
	hold := srv.HoldNext("agent.list")
	aDone := refresh()
	select {
	case <-hold.Received():
	case <-time.After(2 * time.Second):
		t.Fatal("first Refresh never reached herdr")
	}

	// The pane moves on; B starts after A was fetched.
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "working", 2)})
	bDone := refresh()

	// Give an unserialized implementation the chance to finish B first.
	var b result
	bFinished := false
	select {
	case b = <-bDone:
		bFinished = true
	case <-time.After(200 * time.Millisecond):
	}

	hold.Release()
	a := <-aDone
	if !bFinished {
		b = <-bDone
	}
	if a.err != nil || b.err != nil {
		t.Fatalf("Refresh errors: a=%v b=%v", a.err, b.err)
	}

	// Each Refresh returns the list it applied.
	if len(a.agents) != 1 || a.agents[0].AgentStatus != "blocked" {
		t.Errorf("first Refresh returned %+v, want its own list (blocked)", a.agents)
	}
	if len(b.agents) != 1 || b.agents[0].AgentStatus != "working" {
		t.Errorf("second Refresh returned %+v, want its own list (working)", b.agents)
	}

	snap := syncer.Snapshot()
	if len(snap) != 1 || snap[0].AgentStatus != "working" || snap[0].StateChangeSeq != 2 {
		t.Fatalf("snapshot = %+v; a stale list overwrote the newer one", snap)
	}
	var seqs []uint64
	for _, c := range listener.allChanges() {
		seqs = append(seqs, c.Agent.StateChangeSeq)
	}
	if !slices.IsSorted(seqs) {
		t.Errorf("listener saw state_change_seq %v; it went backwards", seqs)
	}
	if listener.overlap.Load() {
		t.Error("OnChanges ran concurrently")
	}
}

func TestRefreshWaitingForAnotherHonoursContext(t *testing.T) {
	srv := herdrtest.New(t)
	srv.SetAgents([]map[string]any{agentRow("w1:p1", "idle", 1)})
	syncer := &herdr.Syncer{
		Client:   &herdr.Client{SocketPath: srv.SocketPath, Timeout: 10 * time.Second},
		Listener: &testListener{},
	}

	hold := srv.HoldNext("agent.list")
	aDone := make(chan error, 1)
	go func() {
		_, err := syncer.Refresh(context.Background())
		aDone <- err
	}()
	<-hold.Received()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := syncer.Refresh(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Refresh behind an in-flight one: err = %v, want context.DeadlineExceeded", err)
	}
	if n := callsOf(srv, "agent.list"); n != 1 {
		t.Errorf("%d agent.list calls; the second Refresh must wait for the first", n)
	}

	hold.Release()
	if err := <-aDone; err != nil {
		t.Errorf("first Refresh: %v", err)
	}
}

func TestRefreshKeepsHerdrOrder(t *testing.T) {
	srv := herdrtest.New(t)
	syncer := &herdr.Syncer{Client: testClient(srv)}
	srv.SetAgents([]map[string]any{
		agentRow("w1:p3", "idle", 1),
		agentRow("w1:p1", "idle", 1),
		{"pane_id": "w1:sh", "agent": nil, "agent_status": "unknown"},
		agentRow("w2:p9", "idle", 1),
		agentRow("w1:p2", "idle", 1),
		agentRow("w3:p0", "idle", 1),
	})
	want := []string{"w1:p3", "w1:p1", "w2:p9", "w1:p2", "w3:p0"}

	got, err := syncer.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := func(agents []herdr.AgentInfo) []string {
		var out []string
		for _, a := range agents {
			out = append(out, a.PaneID)
		}
		return out
	}
	if !slices.Equal(ids(got), want) {
		t.Errorf("Refresh order = %v, want herdr's %v", ids(got), want)
	}
	if snap := ids(syncer.Snapshot()); !slices.Equal(snap, want) {
		t.Errorf("Snapshot order = %v, want herdr's %v", snap, want)
	}
}

type logRecorder struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec.Clone())
	return nil
}
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *logRecorder) WithGroup(string) slog.Handler      { return r }

func (r *logRecorder) count(level slog.Level, substr string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.recs {
		if rec.Level == level && strings.Contains(rec.Message, substr) {
			n++
		}
	}
	return n
}

// A failing agent.list is logged once per streak, not on every retry.
func TestRefreshLogsFailuresOncePerStreak(t *testing.T) {
	srv := herdrtest.New(t)
	rec := &logRecorder{}
	syncer := &herdr.Syncer{Client: testClient(srv), Logger: slog.New(rec)}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		srv.FailNext("agent.list", "server_error", "boom")
		if _, err := syncer.Refresh(ctx); err == nil {
			t.Fatal("Refresh succeeded despite FailNext")
		}
	}
	if n := rec.count(slog.LevelWarn, ""); n != 1 {
		t.Errorf("%d warnings for a streak of 3 agent.list failures, want 1", n)
	}

	if _, err := syncer.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if n := rec.count(slog.LevelInfo, "recovered"); n != 1 {
		t.Errorf("%d recovery logs, want 1", n)
	}

	srv.FailNext("agent.list", "server_error", "boom")
	_, _ = syncer.Refresh(ctx)
	if n := rec.count(slog.LevelWarn, ""); n != 2 {
		t.Errorf("a new streak after recovery should warn again: %d warnings, want 2", n)
	}
}
