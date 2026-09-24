package herdr_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/herdr"
	"github.com/gabrielmarcano/agent-monitor/pkg/herdrtest"
)

type testListener struct {
	mu      sync.Mutex
	changes [][]herdr.Change
	onlines []bool
	pongs   []herdr.Pong
}

func (l *testListener) OnChanges(changes []herdr.Change) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := make([]herdr.Change, len(changes))
	copy(cp, changes)
	l.changes = append(l.changes, cp)
}

func (l *testListener) OnHerdrOnline(online bool, pong herdr.Pong) {
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

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestSyncerAddUpdateRemove(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)
	listener := &testListener{}

	syncer := &herdr.Syncer{
		Client:       client,
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  500 * time.Millisecond,
		PollDegraded: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initial agent
	agentName := "claude"
	srv.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"agent":            agentName,
			"agent_status":     "idle",
			"state_change_seq": 1,
		},
	})

	go func() {
		_ = syncer.Run(ctx)
	}()

	// 1. Wait for initial Added change
	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		return len(changes) == 1 && changes[0].Kind == herdr.Added && changes[0].Agent.PaneID == "w1:p1"
	})

	// 2. Update agent status to working, and emit status changed event
	srv.SetAgents([]map[string]any{
		{
			"pane_id":          "w1:p1",
			"agent":            agentName,
			"agent_status":     "working",
			"state_change_seq": 2,
		},
	})
	srv.EmitStatusChanged("w1:p1", "working")

	// Wait for Updated change
	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		if len(changes) < 2 {
			return false
		}
		last := changes[len(changes)-1]
		return last.Kind == herdr.Updated && last.Agent.AgentStatus == "working" && last.Prev != nil && last.Prev.AgentStatus == "idle"
	})

	// 3. Remove agent (empty list) and emit event
	srv.SetAgents([]map[string]any{})
	srv.EmitGlobal("pane.closed", map[string]any{"pane_id": "w1:p1"})

	// Wait for Removed change
	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		if len(changes) < 3 {
			return false
		}
		last := changes[len(changes)-1]
		return last.Kind == herdr.Removed && last.Agent.PaneID == "w1:p1"
	})
}

func TestSyncerResubscribesOnNewPane(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)
	listener := &testListener{}

	syncer := &herdr.Syncer{
		Client:       client,
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  500 * time.Millisecond,
		PollDegraded: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentClaude := "claude"
	srv.SetAgents([]map[string]any{
		{"pane_id": "w1:p1", "agent": agentClaude, "agent_status": "idle"},
	})

	go func() {
		_ = syncer.Run(ctx)
	}()

	// Wait for initial subscription
	waitUntil(t, 2*time.Second, func() bool {
		for _, c := range srv.Calls() {
			if c.Method == "events.subscribe" {
				return true
			}
		}
		return false
	})

	// Add a second pane
	agentAgy := "agy"
	srv.SetAgents([]map[string]any{
		{"pane_id": "w1:p1", "agent": agentClaude, "agent_status": "idle"},
		{"pane_id": "w1:p2", "agent": agentAgy, "agent_status": "idle"},
	})
	srv.EmitGlobal("pane.created", map[string]any{"pane_id": "w1:p2"})

	// Verify that a new events.subscribe call arrives containing pane_id "w1:p2"
	waitUntil(t, 2*time.Second, func() bool {
		for _, c := range srv.Calls() {
			if c.Method != "events.subscribe" {
				continue
			}
			subs, _ := c.Params["subscriptions"].([]herdr.Subscription)
			if subs == nil {
				// unmarshaled as []any or slice
				rawSubs, _ := c.Params["subscriptions"].([]any)
				for _, r := range rawSubs {
					if m, ok := r.(map[string]any); ok {
						if m["pane_id"] == "w1:p2" {
							return true
						}
					}
				}
			} else {
				for _, s := range subs {
					if s.PaneID == "w1:p2" {
						return true
					}
				}
			}
		}
		return false
	})
}

func TestSyncerOfflineOnline(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)
	listener := &testListener{}

	syncer := &herdr.Syncer{
		Client:       client,
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  200 * time.Millisecond,
		PollDegraded: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentClaude := "claude"
	srv.SetAgents([]map[string]any{
		{"pane_id": "w1:p1", "agent": agentClaude, "agent_status": "idle"},
	})

	go func() {
		_ = syncer.Run(ctx)
	}()

	// Wait for online=true
	waitUntil(t, 2*time.Second, func() bool {
		list := listener.onlinesList()
		return len(list) > 0 && list[0] == true
	})

	// Stop server: simulate herdr going offline
	srv.Stop()

	// Wait for OnHerdrOnline(false)
	waitUntil(t, 3*time.Second, func() bool {
		list := listener.onlinesList()
		return len(list) >= 2 && list[len(list)-1] == false
	})

	// Restart server
	srv.Start()

	// Wait for OnHerdrOnline(true) again
	waitUntil(t, 3*time.Second, func() bool {
		list := listener.onlinesList()
		return len(list) >= 3 && list[len(list)-1] == true
	})
}

func TestSyncerIgnoresPlainShells(t *testing.T) {
	srv := herdrtest.New(t)
	client := herdr.NewClient(srv.SocketPath)
	listener := &testListener{}

	syncer := &herdr.Syncer{
		Client:       client,
		Listener:     listener,
		Debounce:     10 * time.Millisecond,
		PollHealthy:  500 * time.Millisecond,
		PollDegraded: 50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentName := "claude"
	srv.SetAgents([]map[string]any{
		// Real agent
		{
			"pane_id":      "w1:p1",
			"agent":        agentName,
			"agent_status": "idle",
		},
		// Plain shell: agent null and status unknown
		{
			"pane_id":      "w1:shell",
			"agent":        nil,
			"agent_status": "unknown",
		},
	})

	go func() {
		_ = syncer.Run(ctx)
	}()

	waitUntil(t, 2*time.Second, func() bool {
		changes := listener.allChanges()
		return len(changes) == 1
	})

	changes := listener.allChanges()
	if changes[0].Agent.PaneID != "w1:p1" {
		t.Errorf("expected only w1:p1 in changes, got: %s", changes[0].Agent.PaneID)
	}

	snap := syncer.Snapshot()
	if len(snap) != 1 || snap[0].PaneID != "w1:p1" {
		t.Errorf("expected snapshot to contain only w1:p1, got: %v", snap)
	}
}
