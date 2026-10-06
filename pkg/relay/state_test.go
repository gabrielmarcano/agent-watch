package relay

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestState_SnapshotAndOrdering(t *testing.T) {
	state := NewState()

	state.ReplaceAll([]model.AgentState{
		{PaneID: "p1", Label: "beta", Status: model.StatusIdle},
		{PaneID: "p2", Label: "alpha", Status: model.StatusBlocked},
		{PaneID: "p3", Label: "charlie", Status: model.StatusWorking},
		{PaneID: "p4", Label: "delta", Status: model.StatusBlocked},
	})

	snap := state.Snapshot()
	if len(snap.Agents) != 4 {
		t.Fatalf("expected 4 agents, got %d", len(snap.Agents))
	}

	// StatusBlocked > StatusWorking > StatusIdle
	// For equal severity (p2 alpha vs p4 delta), sorted by label asc: alpha < delta
	expectedOrder := []string{"alpha", "delta", "charlie", "beta"}
	for i, expected := range expectedOrder {
		if snap.Agents[i].Label != expected {
			t.Errorf("agent[%d] expected label %s, got %s", i, expected, snap.Agents[i].Label)
		}
	}
}

func TestState_UpsertAndRemove(t *testing.T) {
	state := NewState()
	sub, subCh := state.Subscribe()
	defer state.Unsubscribe(sub)

	// Initial upsert (prev == nil)
	a1 := model.AgentState{PaneID: "w1:p1", Label: "claude", Status: model.StatusIdle}
	prev := state.Upsert(a1)
	if prev != nil {
		t.Fatalf("expected nil prev on first insert")
	}

	ev := <-subCh
	if ev.Name != "agent" {
		t.Fatalf("expected 'agent' event, got %s", ev.Name)
	}

	// Update agent (prev should reflect old state)
	a1Updated := a1
	a1Updated.Status = model.StatusWorking
	a1Updated.BackgroundAgents = 2
	prev = state.Upsert(a1Updated)
	if prev == nil || prev.Status != model.StatusIdle {
		t.Fatalf("expected prev status idle, got %+v", prev)
	}

	ev = <-subCh
	if ev.Name != "agent" {
		t.Fatalf("expected 'agent' event, got %s", ev.Name)
	}
	// The relay forwards the bridge's fields as they are (contracts.md §1.2).
	var got model.AgentState
	if err := json.Unmarshal(ev.Data, &got); err != nil || got.BackgroundAgents != 2 {
		t.Fatalf("agent event lost background_agents: %s", ev.Data)
	}

	// Remove agent
	state.Remove("w1:p1")
	if state.HasPane("w1:p1") {
		t.Fatalf("pane w1:p1 should have been removed")
	}

	ev = <-subCh
	if ev.Name != "agent_removed" {
		t.Fatalf("expected 'agent_removed' event, got %s", ev.Name)
	}
	var remPayload map[string]string
	_ = json.Unmarshal(ev.Data, &remPayload)
	if remPayload["pane_id"] != "w1:p1" {
		t.Fatalf("unexpected pane_id in removed event: %v", remPayload)
	}
}

func TestState_SetHost(t *testing.T) {
	state := NewState()
	sub, subCh := state.Subscribe()
	defer state.Unsubscribe(sub)

	// Initial change
	state.SetHost(true, true)
	select {
	case ev := <-subCh:
		if ev.Name != "host" {
			t.Fatalf("expected 'host' event, got %s", ev.Name)
		}
	default:
		t.Fatalf("expected host event on state change")
	}

	// Same state: should NOT broadcast
	state.SetHost(true, true)
	select {
	case ev := <-subCh:
		t.Fatalf("did not expect event on identical SetHost, got %s", ev.Name)
	default:
	}

	// Change herdr status
	state.SetHost(true, false)
	select {
	case ev := <-subCh:
		if ev.Name != "host" {
			t.Fatalf("expected 'host' event, got %s", ev.Name)
		}
	default:
		t.Fatalf("expected host event on state change")
	}
}

func TestState_SlowSubscriberDropped(t *testing.T) {
	state := NewState()

	// Slow subscriber: never reads from its channel.
	slowSub, slowCh := state.Subscribe()
	defer state.Unsubscribe(slowSub)

	// Fast subscriber: reads every event right after it is broadcast.
	fastSub, fastCh := state.Subscribe()
	defer state.Unsubscribe(fastSub)

	const events = subscriberBufferSize + 16
	for i := 0; i < events; i++ {
		state.Upsert(model.AgentState{
			PaneID: fmt.Sprintf("pane-%d", i),
			Status: model.StatusWorking,
		})
		// broadcast is synchronous, so the event is already queued.
		select {
		case ev, ok := <-fastCh:
			if !ok || ev.Name != "agent" {
				t.Fatalf("fast subscriber: event %d = %+v (open %v), want an agent event", i, ev, ok)
			}
		default:
			t.Fatalf("fast subscriber missed event %d (dropped although it keeps up?)", i)
		}
	}

	// The slow subscriber holds a full buffer and then a closed channel, so
	// its client reconnects and resyncs from a snapshot. Never blocks: if the
	// channel were still open, the default branch fails the test.
	buffered := 0
	for closed := false; !closed; {
		select {
		case _, ok := <-slowCh:
			if !ok {
				closed = true
			} else {
				buffered++
			}
		default:
			t.Fatalf("slow subscriber channel still open after %d buffered events", buffered)
		}
	}
	if buffered != subscriberBufferSize {
		t.Fatalf("slow subscriber got %d events before being dropped, want %d (a full buffer)", buffered, subscriberBufferSize)
	}

	state.mu.RLock()
	_, slowStillSubscribed := state.subscribers[slowSub]
	_, fastStillSubscribed := state.subscribers[fastSub]
	state.mu.RUnlock()
	if slowStillSubscribed || !fastStillSubscribed {
		t.Fatalf("subscribers after the drop: slow %v fast %v, want only the fast one", slowStillSubscribed, fastStillSubscribed)
	}
}

// TestState_BroadcastUnsubscribeChurn races broadcasters against subscribers
// that disconnect (Unsubscribe) or get dropped as slow by another broadcaster.
// A send on a channel closed concurrently panics and takes the host WebSocket
// handler down with it.
func TestState_BroadcastUnsubscribeChurn(t *testing.T) {
	state := NewState()
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Broadcasters.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				state.Upsert(model.AgentState{PaneID: fmt.Sprintf("p%d-%d", i, n%8), Status: model.StatusWorking})
			}
		}(i)
	}

	// Subscribers that disconnect after reading a little, and slow ones that
	// never read, so they are dropped while other broadcasters are sending.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(slow bool) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				sub, ch := state.Subscribe()
				if slow {
					// Never read: the buffer fills and a broadcaster drops us.
					time.Sleep(200 * time.Microsecond)
				} else {
				read:
					for j := 0; j < 3; j++ {
						select {
						case _, ok := <-ch:
							if !ok {
								break read
							}
						case <-stop:
							break read
						}
					}
				}
				state.Unsubscribe(sub)
			}
		}(i%2 == 0)
	}

	time.Sleep(time.Second)
	close(stop)

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("broadcasters or subscribers did not finish (blocked broadcaster?)")
	}
}
