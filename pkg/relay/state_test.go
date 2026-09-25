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
	prev = state.Upsert(a1Updated)
	if prev == nil || prev.Status != model.StatusIdle {
		t.Fatalf("expected prev status idle, got %+v", prev)
	}

	ev = <-subCh
	if ev.Name != "agent" {
		t.Fatalf("expected 'agent' event, got %s", ev.Name)
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

	// Slow subscriber: never reads from channel
	slowSub, slowCh := state.Subscribe()
	defer state.Unsubscribe(slowSub)

	// Fast subscriber: continuously reads
	fastSub, fastCh := state.Subscribe()
	defer state.Unsubscribe(fastSub)

	fastReceived := 0
	fastDone := make(chan struct{})
	go func() {
		for range fastCh {
			fastReceived++
		}
		close(fastDone)
	}()

	// Buffer is 64. Send 80 events with slight pacing so fast subscriber keeps up.
	for i := 0; i < 80; i++ {
		state.Upsert(model.AgentState{
			PaneID: fmt.Sprintf("pane-%d", i),
			Status: model.StatusWorking,
		})
		time.Sleep(50 * time.Microsecond)
	}

	// The slow subscriber's channel should have been closed when dropped
	dropped := false
	for range slowCh {
	}
	dropped = true

	if !dropped {
		t.Fatalf("slow subscriber channel was not closed")
	}

	// Fast subscriber can unsubscribe and verify it got events without blocking
	state.Unsubscribe(fastSub)
	<-fastDone
	if fastReceived < 80 {
		t.Fatalf("expected fast subscriber to receive 80 events, got %d", fastReceived)
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
