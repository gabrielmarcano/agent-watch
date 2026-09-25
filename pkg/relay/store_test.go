package relay

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

// chanHandler renders every slog record to a channel, so a test can wait
// for a log line written by a background goroutine.
type chanHandler struct{ ch chan string }

func (h chanHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h chanHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h chanHandler) WithGroup(string) slog.Handler            { return h }
func (h chanHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.String())
		return true
	})
	select {
	case h.ch <- b.String():
	default:
	}
	return nil
}

func logChannel(t *testing.T) <-chan string {
	t.Helper()
	ch := make(chan string, 256)
	prev := slog.Default()
	slog.SetDefault(slog.New(chanHandler{ch: ch}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return ch
}

func waitLog(t *testing.T, logs <-chan string, substr string, within time.Duration) string {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case line := <-logs:
			if strings.Contains(line, substr) {
				return line
			}
		case <-deadline:
			t.Fatalf("no log line containing %q within %v", substr, within)
		}
	}
}

// setSaveHooksForTest swaps the save delay and fsync hooks (nil keeps one).
func (s *Store) setSaveHooksForTest(delay time.Duration, file func(*os.File) error, dir func(string) error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if delay > 0 {
		s.saveDelay = delay
	}
	if file != nil {
		s.syncFile = file
	}
	if dir != nil {
		s.syncDir = dir
	}
}

// A failed background save is logged (it used to be discarded) and retried
// by the next save.
func TestStore_LogsBackgroundSaveError(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	logs := logChannel(t)
	store.setSaveHooksForTest(10*time.Millisecond, func(*os.File) error { return errors.New("disk full") }, nil)

	if _, err := store.AddDevice("Retry Watch", Sha256Hex("tok")); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	line := waitLog(t, logs, "disk full", 5*time.Second)
	if !strings.HasPrefix(line, "ERROR") {
		t.Fatalf("save failure logged as %q, want ERROR level", line)
	}

	store.setSaveHooksForTest(0, func(f *os.File) error { return f.Sync() }, nil)
	if err := store.Close(); err != nil {
		t.Fatalf("Close after the disk recovered: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	if !strings.Contains(string(data), "Retry Watch") {
		t.Fatalf("the change lost by the failed save was not saved on Close")
	}
}

// After the rename the data dir is fsynced, or a crash can bring back the old
// store.json.
func TestStore_FsyncsDirectoryAfterRename(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	var synced []string
	sawNewFile := false
	store.setSaveHooksForTest(time.Hour, nil, func(d string) error {
		synced = append(synced, d)
		data, _ := os.ReadFile(filepath.Join(d, "store.json"))
		sawNewFile = strings.Contains(string(data), "Dir Watch")
		return nil
	})
	if _, err := store.AddDevice("Dir Watch", Sha256Hex("tok")); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(synced) != 1 || synced[0] != dir || !sawNewFile {
		t.Fatalf("dir fsyncs = %v (after rename: %v), want one fsync of %s after the rename", synced, sawNewFile, dir)
	}
}

// A slow fsync must not stall the relay: the store stays readable and
// writable while a save is on disk.
func TestStore_FsyncDoesNotHoldStoreLock(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	releaseFsync := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		releaseFsync()
		_ = store.Close()
	}()
	store.setSaveHooksForTest(time.Hour, func(f *os.File) error {
		enterOnce.Do(func() { close(entered) })
		<-release
		return f.Sync()
	}, nil)

	dev, _ := store.AddDevice("Watch", Sha256Hex("tok"))
	flushed := make(chan error, 1)
	go func() { flushed <- store.Flush() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("Flush never reached fsync")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		store.FindDeviceByTokenHash(Sha256Hex("tok"))
		store.ListDevices()
		store.AddHistory(model.HistoryItem{ID: "during-fsync", PaneID: "w1:p1", CompletedAt: model.Now()})
		store.UpdateDeviceFCMToken(dev.ID, "fcm-token")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		releaseFsync()
		t.Fatalf("store operations blocked while a save was in fsync")
	}

	releaseFsync()
	if err := <-flushed; err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

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

	// Relative to now: panes older than 7 days are pruned on every add, so
	// fixed dates would break this test a week after they were written.
	base := time.Now().UTC().Add(-2 * time.Hour)
	at := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }

	// 1. Dedup by ID
	it1 := model.HistoryItem{
		ID:          "dedup-id",
		PaneID:      "w1:p1",
		CompletedAt: at(time.Second),
	}
	if !store.AddHistory(it1) {
		t.Fatalf("first AddHistory reported the item as not stored")
	}
	if store.AddHistory(it1) { // Duplicate should be ignored
		t.Fatalf("duplicate AddHistory reported the item as stored")
	}
	items := store.GetHistory("w1:p1", 50)
	if len(items) != 1 {
		t.Fatalf("expected 1 item after duplicate insertion, got %d", len(items))
	}

	// 2. Max 20 per pane
	for i := 0; i < 25; i++ {
		store.AddHistory(model.HistoryItem{
			ID:          "pane-item-" + strconv.Itoa(i),
			PaneID:      "w1:p1",
			CompletedAt: at(time.Duration(10+i) * time.Second),
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
				CompletedAt: at(time.Hour),
			})
		}
	}
	all := store.GetHistory("", 300)
	if len(all) != 200 {
		t.Fatalf("expected total history to be capped at exactly 200, got %d", len(all))
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
