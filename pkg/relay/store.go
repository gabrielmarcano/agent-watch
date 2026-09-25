package relay

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	storeVersion    = 1
	maxHistoryPane  = 20
	maxHistoryTotal = 200
	historyMaxAge   = 7 * 24 * time.Hour
)

// Device represents a paired client device.
type Device struct {
	ID        string `json:"id"`                  // random 16 hex chars
	Name      string `json:"name"`                // client display name
	TokenHash string `json:"token_hash"`          // hex(sha256(token))
	CreatedAt string `json:"created_at"`          // RFC 3339 UTC
	LastSeen  string `json:"last_seen"`           // RFC 3339 UTC
	FCMToken  string `json:"fcm_token,omitempty"` // push token
}

type storeFile struct {
	Version int                            `json:"version"`
	Devices []Device                       `json:"devices"`
	History map[string][]model.HistoryItem `json:"history"` // pane_id -> newest first
}

// Store provides thread-safe persistence for devices and history.
type Store struct {
	mu       sync.RWMutex
	filePath string
	devices  []Device
	history  map[string][]model.HistoryItem // pane_id -> items (newest first)
	lastSeen map[string]time.Time           // device_id -> in-memory last seen timestamp

	dirty       bool
	saveTimer   *time.Timer
	saveClosing bool

	saveMu    sync.Mutex             // serializes writes of store.json; taken before mu
	saveDelay time.Duration          // coalescing delay before a background save
	syncFile  func(f *os.File) error // fsync of the temp file (tests override)
	syncDir   func(dir string) error // fsync of the data dir (tests override)
}

// NewStore loads an existing store file or initializes a new one.
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dataDir, err)
	}

	filePath := filepath.Join(dataDir, "store.json")
	s := &Store{
		filePath:  filePath,
		history:   make(map[string][]model.HistoryItem),
		lastSeen:  make(map[string]time.Time),
		saveDelay: time.Second,
		syncFile:  func(f *os.File) error { return f.Sync() },
		syncDir:   syncDir,
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Brand new store, write initial empty file
			s.dirty = true
			if err := s.save(false); err != nil {
				return nil, fmt.Errorf("init store file: %w", err)
			}
			return s, nil
		}
		return nil, fmt.Errorf("read store file %s: %w", filePath, err)
	}

	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("corrupt store file %s: %w", filePath, err)
	}

	if sf.Devices != nil {
		s.devices = sf.Devices
		for _, d := range s.devices {
			if t, err := time.Parse(time.RFC3339, d.LastSeen); err == nil {
				s.lastSeen[d.ID] = t
			}
		}
	}
	if sf.History != nil {
		s.history = sf.History
	}

	return s, nil
}

// AddDevice registers a new device with its token hash.
func (s *Store) AddDevice(name, tokenHash string) (Device, error) {
	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return Device{}, fmt.Errorf("generate device id: %w", err)
	}
	id := hex.EncodeToString(idBytes[:])
	now := model.Now()

	dev := Device{
		ID:        id,
		Name:      name,
		TokenHash: tokenHash,
		CreatedAt: now,
		LastSeen:  now,
	}

	s.mu.Lock()
	s.devices = append(s.devices, dev)
	if t, err := time.Parse(time.RFC3339, now); err == nil {
		s.lastSeen[id] = t
	}
	s.scheduleSaveLocked()
	s.mu.Unlock()

	return dev, nil
}

// FindDeviceByTokenHash performs constant-time comparison against stored token hashes.
func (s *Store) FindDeviceByTokenHash(tokenHash string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	target := []byte(tokenHash)
	for _, d := range s.devices {
		stored := []byte(d.TokenHash)
		if len(stored) == len(target) && subtle.ConstantTimeCompare(stored, target) == 1 {
			return d, true
		}
	}
	return Device{}, false
}

// TouchDevice updates the LastSeen timestamp for a device at most once per minute.
func (s *Store) TouchDevice(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	last, ok := s.lastSeen[deviceID]
	now := time.Now()
	if ok && now.Sub(last) < time.Minute {
		return
	}

	s.lastSeen[deviceID] = now
	nowStr := model.Now()
	for i := range s.devices {
		if s.devices[i].ID == deviceID {
			s.devices[i].LastSeen = nowStr
			s.scheduleSaveLocked()
			break
		}
	}
}

// UpdateDeviceFCMToken updates the FCM token for a device.
func (s *Store) UpdateDeviceFCMToken(deviceID, fcmToken string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.devices {
		if s.devices[i].ID == deviceID {
			if s.devices[i].FCMToken != fcmToken {
				s.devices[i].FCMToken = fcmToken
				s.scheduleSaveLocked()
			}
			return true
		}
	}
	return false
}

// AllFCMTokens returns a deduplicated slice of all registered device FCM tokens.
func (s *Store) AllFCMTokens() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]bool)
	var tokens []string
	for _, d := range s.devices {
		if d.FCMToken != "" && !seen[d.FCMToken] {
			seen[d.FCMToken] = true
			tokens = append(tokens, d.FCMToken)
		}
	}
	return tokens
}

// RemoveFCMToken clears the FCM token from any device that has it.
func (s *Store) RemoveFCMToken(fcmToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for i := range s.devices {
		if s.devices[i].FCMToken == fcmToken {
			s.devices[i].FCMToken = ""
			changed = true
		}
	}
	if changed {
		s.scheduleSaveLocked()
	}
}

// ListDevices returns a copy of all registered devices.
func (s *Store) ListDevices() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Device, len(s.devices))
	copy(out, s.devices)
	return out
}

// RevokeDevice removes a device by ID and returns the removed device.
// ok is false when no device has that ID.
func (s *Store) RevokeDevice(deviceID string) (removed Device, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, d := range s.devices {
		if d.ID == deviceID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Device{}, false
	}

	removed = s.devices[idx]
	s.devices = append(s.devices[:idx], s.devices[idx+1:]...)
	delete(s.lastSeen, deviceID)
	s.scheduleSaveLocked()
	return removed, true
}

// AddHistory appends a history item for a pane, enforcing dedup and size limits.
func (s *Store) AddHistory(item model.HistoryItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneOldPanesLocked()

	items := s.history[item.PaneID]
	for _, existing := range items {
		if existing.ID == item.ID {
			// Duplicate item already stored for this pane
			return
		}
	}

	// Prepend newest item
	items = append([]model.HistoryItem{item}, items...)
	if len(items) > maxHistoryPane {
		items = items[:maxHistoryPane]
	}
	s.history[item.PaneID] = items

	// Enforce global maximum of 200 items across all panes
	s.enforceTotalLimitLocked()

	s.scheduleSaveLocked()
}

// GetHistory retrieves history items matching the filter, newest first.
func (s *Store) GetHistory(paneID string, limit int) []model.HistoryItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	} else if limit > maxHistoryTotal {
		limit = maxHistoryTotal
	}

	if paneID != "" {
		items := s.history[paneID]
		if len(items) > limit {
			out := make([]model.HistoryItem, limit)
			copy(out, items[:limit])
			return out
		}
		out := make([]model.HistoryItem, len(items))
		copy(out, items)
		return out
	}

	// Aggregate from all panes
	var all []model.HistoryItem
	for _, items := range s.history {
		all = append(all, items...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CompletedAt > all[j].CompletedAt
	})

	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// pruneOldPanesLocked removes panes where the newest item is older than 7 days.
func (s *Store) pruneOldPanesLocked() {
	now := time.Now()
	for paneID, items := range s.history {
		if len(items) == 0 {
			delete(s.history, paneID)
			continue
		}
		// items are newest first, check items[0]
		t, err := time.Parse(time.RFC3339, items[0].CompletedAt)
		if err == nil && now.Sub(t) > historyMaxAge {
			delete(s.history, paneID)
		}
	}
}

// enforceTotalLimitLocked trims oldest items across all panes when total count exceeds 200.
func (s *Store) enforceTotalLimitLocked() {
	total := 0
	for _, items := range s.history {
		total += len(items)
	}
	if total <= maxHistoryTotal {
		return
	}

	// Flatten references to all items with paneID and index
	type itemRef struct {
		paneID      string
		index       int
		completedAt string
	}
	var refs []itemRef
	for paneID, items := range s.history {
		for i, it := range items {
			refs = append(refs, itemRef{paneID: paneID, index: i, completedAt: it.CompletedAt})
		}
	}

	// Sort newest first
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].completedAt > refs[j].completedAt
	})

	// Retain only top maxHistoryTotal items
	keep := make(map[string]map[int]bool)
	for i := 0; i < maxHistoryTotal && i < len(refs); i++ {
		p := refs[i].paneID
		if keep[p] == nil {
			keep[p] = make(map[int]bool)
		}
		keep[p][refs[i].index] = true
	}

	for paneID, items := range s.history {
		var filtered []model.HistoryItem
		for i, it := range items {
			if keep[paneID] != nil && keep[paneID][i] {
				filtered = append(filtered, it)
			}
		}
		if len(filtered) == 0 {
			delete(s.history, paneID)
		} else {
			s.history[paneID] = filtered
		}
	}
}

// scheduleSaveLocked marks the store dirty and saves it in the background
// after saveDelay (1 s), coalescing the changes made meanwhile.
func (s *Store) scheduleSaveLocked() {
	s.dirty = true
	if s.saveTimer != nil || s.saveClosing {
		return
	}
	s.saveTimer = time.AfterFunc(s.saveDelay, s.backgroundSave)
}

func (s *Store) backgroundSave() {
	s.mu.Lock()
	s.saveTimer = nil
	s.mu.Unlock()
	if err := s.save(true); err != nil {
		// The store stays dirty: the next change, Flush or Close retries.
		slog.Error("save store failed; will retry on the next change or at shutdown", "path", s.filePath, "err", err)
	}
}

// save writes the store to disk if it is dirty. The snapshot is taken under
// mu; the slow part (write, fsync, rename, directory fsync) runs without it,
// so a slow disk never stalls requests or the host. saveMu keeps saves in
// order, so a later save always carries newer data. A background save
// (the coalescing timer) is skipped once Close has started: Close saves.
func (s *Store) save(background bool) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	s.mu.Lock()
	if !s.dirty || (background && s.saveClosing) {
		s.mu.Unlock()
		return nil
	}
	data, err := s.marshalLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.dirty = false
	s.mu.Unlock()

	if err := s.writeFile(data); err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Store) marshalLocked() ([]byte, error) {
	sf := storeFile{
		Version: storeVersion,
		Devices: s.devices,
		History: s.history,
	}
	if sf.Devices == nil {
		sf.Devices = []Device{}
	}
	if sf.History == nil {
		sf.History = make(map[string][]model.HistoryItem)
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal store file: %w", err)
	}
	return data, nil
}

// writeFile writes data to store.json.tmp, fsyncs it, renames it over
// store.json and fsyncs the directory so the rename survives a crash.
// The caller holds saveMu.
func (s *Store) writeFile(data []byte) error {
	tmpPath := s.filePath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open tmp store file: %w", err)
	}

	// Under sudo, keep the file owned by the data dir's owner (the service
	// user), or the relay could no longer read its own store.
	if err := matchDirOwner(f, filepath.Dir(s.filePath)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write tmp store file: %w", err)
	}

	if err := s.syncFile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("fsync tmp store file: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close tmp store file: %w", err)
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename store file: %w", err)
	}

	return s.syncDir(filepath.Dir(s.filePath))
}

// Flush immediately flushes any pending writes to disk.
func (s *Store) Flush() error {
	s.mu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.mu.Unlock()
	return s.save(false)
}

// Close flushes data and closes the store. Changes made after Close are
// kept in memory only.
func (s *Store) Close() error {
	s.mu.Lock()
	s.saveClosing = true
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.mu.Unlock()
	return s.save(false)
}
