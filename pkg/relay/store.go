package relay

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gabrielmarcano/agent-monitor/pkg/model"
)

const (
	// storeVersion 2 added hosts and keyed history by (host, pane_id).
	storeVersion    = 2
	maxHistoryPane  = 20
	maxHistoryTotal = 200
	historyMaxAge   = 7 * 24 * time.Hour

	// DefaultHostID is AW_HOST_ID's default: the host AW_HOST_TOKEN
	// authenticates, and the owner of the history a version 1 store held.
	DefaultHostID = "main"
	// maxHostNameRunes caps a host's display name.
	maxHostNameRunes = 64
)

var (
	// ErrHostExists means a host with that id is already registered.
	ErrHostExists = errors.New("a host with that id already exists")
	// ErrInvalidHostID means the id does not match [a-z0-9-]{1,32}.
	ErrInvalidHostID = errors.New("host id must be 1 to 32 characters of a-z, 0-9 and -")
	// ErrInvalidHostName means the display name is too long or has control characters.
	ErrInvalidHostName = fmt.Errorf("host name must be at most %d characters, without control characters", maxHostNameRunes)

	errEmptyTokenHash = errors.New("host token hash is empty")

	hostIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// ValidHostID reports whether id is a valid host id ([a-z0-9-]{1,32},
// contracts.md §1.6).
func ValidHostID(id string) bool {
	return hostIDPattern.MatchString(id)
}

// NormalizeHostName trims name and checks it; an empty name becomes id.
func NormalizeHostName(id, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return id, nil
	}
	if utf8.RuneCountInString(name) > maxHostNameRunes || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidHostName
	}
	return name, nil
}

// Host is a bridge registered on the relay with its own token (`hosts add`).
// The host of AW_HOST_TOKEN is not stored: it comes from the environment.
type Host struct {
	ID        string `json:"id"`         // [a-z0-9-]{1,32}, chosen by the owner
	Name      string `json:"name"`       // what the watch shows
	TokenHash string `json:"token_hash"` // hex(sha256(token))
	CreatedAt string `json:"created_at"` // RFC 3339 UTC
	LastSeen  string `json:"last_seen"`  // RFC 3339 UTC; its last connect or disconnect, "" before the first
}

// paneKey identifies an agent: herdr numbers its own panes, so a pane id is
// unique only within one host.
type paneKey struct {
	host string
	pane string
}

// historyFileKey is a history bucket's key in store.json: "<host>/<pane_id>".
// A host id never contains "/". A flat map keeps the file readable by a relay
// that predates hosts (after a rollback it simply finds no pane by these keys).
func historyFileKey(k paneKey) string {
	return k.host + "/" + k.pane
}

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
	Version int      `json:"version"`
	Hosts   []Host   `json:"hosts"`
	Devices []Device `json:"devices"`
	// History buckets, newest first: version 2 keys them "<host>/<pane_id>",
	// version 1 by pane_id alone.
	History map[string][]model.HistoryItem `json:"history"`
}

// Store provides thread-safe persistence for hosts, devices and history.
type Store struct {
	mu       sync.RWMutex
	filePath string
	// legacyHost owns history that names no host: a version 1 file's, and
	// an item added without one (the hub always stamps it).
	legacyHost string
	hosts      []Host
	devices    []Device
	history    map[paneKey][]model.HistoryItem // (host, pane_id) -> items (newest first)
	lastSeen   map[string]time.Time            // device_id -> in-memory last seen timestamp

	dirty       bool
	saveTimer   *time.Timer
	saveClosing bool

	saveMu    sync.Mutex             // serializes writes of store.json; taken before mu
	saveDelay time.Duration          // coalescing delay before a background save
	syncFile  func(f *os.File) error // fsync of the temp file (tests override)
	syncDir   func(dir string) error // fsync of the data dir (tests override)
}

// NewStore loads an existing store file or initializes a new one. A version 1
// file's history goes to DefaultHostID (OpenStore names another host).
func NewStore(dataDir string) (*Store, error) {
	return OpenStore(dataDir, DefaultHostID)
}

// OpenStore loads an existing store file or initializes a new one. A version 1
// file (from a relay that predates hosts) is migrated at once: its history
// belongs to legacyHostID, the host of AW_HOST_TOKEN (AW_HOST_ID).
func OpenStore(dataDir, legacyHostID string) (*Store, error) {
	if !ValidHostID(legacyHostID) {
		return nil, fmt.Errorf("legacy host id %q: %w", legacyHostID, ErrInvalidHostID)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dataDir, err)
	}

	filePath := filepath.Join(dataDir, "store.json")
	s := &Store{
		filePath:   filePath,
		legacyHost: legacyHostID,
		history:    make(map[paneKey][]model.HistoryItem),
		lastSeen:   make(map[string]time.Time),
		saveDelay:  time.Second,
		syncFile:   func(f *os.File) error { return f.Sync() },
		syncDir:    syncDir,
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
	if sf.Version > storeVersion {
		return nil, fmt.Errorf("store file %s is version %d, newer than this relay reads (%d): run a newer relay", filePath, sf.Version, storeVersion)
	}

	for _, h := range sf.Hosts {
		if !ValidHostID(h.ID) || h.TokenHash == "" {
			return nil, fmt.Errorf("corrupt store file %s: invalid host %q", filePath, h.ID)
		}
	}
	s.hosts = sf.Hosts

	migrate := sf.Version < 2
	for fileKey, items := range sf.History {
		key := paneKey{host: legacyHostID, pane: fileKey}
		if !migrate {
			host, pane, ok := strings.Cut(fileKey, "/")
			if !ok || !ValidHostID(host) || pane == "" {
				return nil, fmt.Errorf("corrupt store file %s: invalid history key %q", filePath, fileKey)
			}
			key = paneKey{host: host, pane: pane}
		}
		for i := range items {
			items[i].Host = key.host
		}
		s.history[key] = items
	}

	if sf.Devices != nil {
		s.devices = sf.Devices
		for _, d := range s.devices {
			if t, err := time.Parse(time.RFC3339, d.LastSeen); err == nil {
				s.lastSeen[d.ID] = t
			}
		}
	}

	if migrate {
		// Written at once, so the file on disk says which host the history
		// belongs to even if this relay never changes anything else.
		s.dirty = true
		if err := s.save(false); err != nil {
			return nil, fmt.Errorf("migrate store file %s to version %d: %w", filePath, storeVersion, err)
		}
		slog.Info("store migrated", "path", filePath, "from_version", sf.Version, "to_version", storeVersion, "history_host", legacyHostID)
	}

	return s, nil
}

// AddHost registers a host with the hash of its token. name defaults to id.
func (s *Store) AddHost(id, name, tokenHash string) (Host, error) {
	if !ValidHostID(id) {
		return Host{}, ErrInvalidHostID
	}
	name, err := NormalizeHostName(id, name)
	if err != nil {
		return Host{}, err
	}
	if tokenHash == "" {
		return Host{}, errEmptyTokenHash
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hosts {
		if h.ID == id {
			return Host{}, ErrHostExists
		}
	}
	h := Host{ID: id, Name: name, TokenHash: tokenHash, CreatedAt: model.Now()}
	s.hosts = append(s.hosts, h)
	s.scheduleSaveLocked()
	return h, nil
}

// FindHostByTokenHash performs constant-time comparison against stored host token hashes.
func (s *Store) FindHostByTokenHash(tokenHash string) (Host, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	target := []byte(tokenHash)
	found := -1
	for i, h := range s.hosts {
		stored := []byte(h.TokenHash)
		if len(stored) == len(target) && subtle.ConstantTimeCompare(stored, target) == 1 {
			found = i
		}
	}
	if found < 0 {
		return Host{}, false
	}
	return s.hosts[found], true
}

// ListHosts returns a copy of the registered hosts.
func (s *Store) ListHosts() []Host {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Host, len(s.hosts))
	copy(out, s.hosts)
	return out
}

// RevokeHost removes a host by id and returns it; ok is false when no host has
// that id. Its history stays until it ages out.
func (s *Store) RevokeHost(id string) (removed Host, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, h := range s.hosts {
		if h.ID == id {
			s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
			s.scheduleSaveLocked()
			return h, true
		}
	}
	return Host{}, false
}

// TouchHost sets a registered host's LastSeen to now (a connect or a
// disconnect, so rare enough to save each time). Unknown ids are ignored.
func (s *Store) TouchHost(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.hosts {
		if s.hosts[i].ID == id {
			s.hosts[i].LastSeen = model.Now()
			s.scheduleSaveLocked()
			return
		}
	}
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

// AddHistory appends a history item for its (Host, PaneID), enforcing dedup
// and size limits. It reports whether the item is now stored: false for a
// duplicate, and for an item so old that the size limits dropped it at once.
func (s *Store) AddHistory(item model.HistoryItem) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneOldPanesLocked()

	if item.Host == "" {
		item.Host = s.legacyHost
	}
	key := paneKey{host: item.Host, pane: item.PaneID}
	items := s.history[key]
	for _, existing := range items {
		if existing.ID == item.ID {
			// Duplicate item already stored for this pane
			return false
		}
	}

	// Prepend newest item
	items = append([]model.HistoryItem{item}, items...)
	if len(items) > maxHistoryPane {
		items = items[:maxHistoryPane]
	}
	s.history[key] = items

	// Enforce global maximum of 200 items across all panes
	s.enforceTotalLimitLocked()

	s.scheduleSaveLocked()

	for _, kept := range s.history[key] {
		if kept.ID == item.ID {
			return true
		}
	}
	return false
}

// GetHistory retrieves history items matching the filter, newest first. host
// and paneID are optional: an empty host matches every host, an empty paneID
// every pane. A request for one pane never returns more than maxHistoryPane
// items, also when several hosts have that pane.
func (s *Store) GetHistory(host, paneID string, limit int) []model.HistoryItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	} else if limit > maxHistoryTotal {
		limit = maxHistoryTotal
	}
	if paneID != "" && limit > maxHistoryPane {
		limit = maxHistoryPane
	}

	if host != "" && paneID != "" {
		items := s.history[paneKey{host: host, pane: paneID}]
		if len(items) > limit {
			items = items[:limit]
		}
		out := make([]model.HistoryItem, len(items))
		copy(out, items)
		return out
	}

	var all []model.HistoryItem
	for key, items := range s.history {
		if (host == "" || key.host == host) && (paneID == "" || key.pane == paneID) {
			all = append(all, items...)
		}
	}

	sort.SliceStable(all, func(i, j int) bool {
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
	for key, items := range s.history {
		if len(items) == 0 {
			delete(s.history, key)
			continue
		}
		// items are newest first, check items[0]
		t, err := time.Parse(time.RFC3339, items[0].CompletedAt)
		if err == nil && now.Sub(t) > historyMaxAge {
			delete(s.history, key)
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

	// Flatten references to all items with their bucket and index
	type itemRef struct {
		key         paneKey
		index       int
		completedAt string
	}
	var refs []itemRef
	for key, items := range s.history {
		for i, it := range items {
			refs = append(refs, itemRef{key: key, index: i, completedAt: it.CompletedAt})
		}
	}

	// Sort newest first
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].completedAt > refs[j].completedAt
	})

	// Retain only top maxHistoryTotal items
	keep := make(map[paneKey]map[int]bool)
	for i := 0; i < maxHistoryTotal && i < len(refs); i++ {
		k := refs[i].key
		if keep[k] == nil {
			keep[k] = make(map[int]bool)
		}
		keep[k][refs[i].index] = true
	}

	for key, items := range s.history {
		var filtered []model.HistoryItem
		for i, it := range items {
			if keep[key] != nil && keep[key][i] {
				filtered = append(filtered, it)
			}
		}
		if len(filtered) == 0 {
			delete(s.history, key)
		} else {
			s.history[key] = filtered
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
		Hosts:   s.hosts,
		Devices: s.devices,
		History: make(map[string][]model.HistoryItem, len(s.history)),
	}
	if sf.Hosts == nil {
		sf.Hosts = []Host{}
	}
	if sf.Devices == nil {
		sf.Devices = []Device{}
	}
	for key, items := range s.history {
		sf.History[historyFileKey(key)] = items
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
