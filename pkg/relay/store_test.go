package relay

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

func TestStore_DurabilityAndPermissions(t *testing.T) {
	dir := t.TempDir()

	store1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	dev, err := store1.AddDevice("Watch 1", Sha256Hex("secret-token"))
	if err != nil {
		t.Fatalf("AddDevice failed: %v", err)
	}

	item := model.HistoryItem{
		ID:          "hist-1",
		PaneID:      "w1:p1",
		Agent:       "claude",
		Label:       "claude",
		Query:       "test query",
		Response:    "test response",
		Source:      "transcript",
		CompletedAt: model.Now(),
	}
	store1.AddHistory(item)

	if err := store1.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify file permissions 0600
	storePath := filepath.Join(dir, "store.json")
	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat store.json failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected store file perm 0600, got %04o", perm)
	}

	// Verify raw secret-token is NOT in store.json
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) == "" || string(data) == "secret-token" {
		t.Fatalf("unexpected content")
	}

	// Reopen store from same directory and verify data survives
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (reopen) failed: %v", err)
	}
	defer store2.Close()

	foundDev, ok := store2.FindDeviceByTokenHash(Sha256Hex("secret-token"))
	if !ok {
		t.Fatalf("expected to find device by token hash")
	}
	if foundDev.ID != dev.ID || foundDev.Name != "Watch 1" {
		t.Fatalf("unexpected device data: %+v", foundDev)
	}

	items := store2.GetHistory("w1:p1", 10)
	if len(items) != 1 || items[0].ID != "hist-1" {
		t.Fatalf("unexpected history items: %+v", items)
	}
}

func TestStore_CorruptFileFailsStartup(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.json")

	// Write garbage to store.json
	if err := os.WriteFile(storePath, []byte("NOT JSON AT ALL"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := NewStore(dir)
	if err == nil {
		t.Fatalf("expected NewStore to fail on corrupt file, got nil")
	}
}

func TestStore_HistoryBoundsAndDedup(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// 1. Dedup by ID
	it1 := model.HistoryItem{
		ID:          "dedup-id",
		PaneID:      "w1:p1",
		CompletedAt: "2026-09-24T00:00:01Z",
	}
	store.AddHistory(it1)
	store.AddHistory(it1) // Duplicate should be ignored
	items := store.GetHistory("w1:p1", 50)
	if len(items) != 1 {
		t.Fatalf("expected 1 item after duplicate insertion, got %d", len(items))
	}

	// 2. Max 20 per pane
	for i := 0; i < 25; i++ {
		store.AddHistory(model.HistoryItem{
			ID:          "pane-item-" + strconv.Itoa(i),
			PaneID:      "w1:p1",
			CompletedAt: "2026-09-24T00:00:" + strconv.Itoa(10+i) + "Z",
		})
	}
	paneItems := store.GetHistory("w1:p1", 50)
	if len(paneItems) != 20 {
		t.Fatalf("expected max 20 items per pane, got %d", len(paneItems))
	}
	// Newest should be at index 0
	if paneItems[0].ID != "pane-item-24" {
		t.Fatalf("expected newest item first, got %s", paneItems[0].ID)
	}

	// 3. Max 200 total across panes
	// Add 15 items across 20 panes (300 items total)
	for p := 0; p < 20; p++ {
		paneID := "pane-" + strconv.Itoa(p)
		for i := 0; i < 15; i++ {
			store.AddHistory(model.HistoryItem{
				ID:          paneID + "-it-" + strconv.Itoa(i),
				PaneID:      paneID,
				CompletedAt: "2026-09-24T01:00:00Z",
			})
		}
	}
	all := store.GetHistory("", 300)
	if len(all) > 200 {
		t.Fatalf("expected total history to be capped at 200, got %d", len(all))
	}
}

func TestStore_PruneOldPanes(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	oldTime := time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	store.AddHistory(model.HistoryItem{
		ID:          "old-item",
		PaneID:      "old-pane",
		CompletedAt: oldTime,
	})

	// Adding a new item triggers prune
	store.AddHistory(model.HistoryItem{
		ID:          "new-item",
		PaneID:      "new-pane",
		CompletedAt: model.Now(),
	})

	oldItems := store.GetHistory("old-pane", 10)
	if len(oldItems) != 0 {
		t.Fatalf("expected old pane to be pruned, got %d items", len(oldItems))
	}

	newItems := store.GetHistory("new-pane", 10)
	if len(newItems) != 1 {
		t.Fatalf("expected new pane to exist, got %d items", len(newItems))
	}
}
