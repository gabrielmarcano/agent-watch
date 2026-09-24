package relay

import (
	"encoding/json"
	"fmt"
	"testing"

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

	// Buffer is 64. Send 80 events.
	for i := 0; i < 80; i++ {
		state.Upsert(model.AgentState{
			PaneID: fmt.Sprintf("pane-%d", i),
			Status: model.StatusWorking,
		})
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
