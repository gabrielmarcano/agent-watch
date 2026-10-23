package relay

// Local administration of devices and hosts (`agent-watch-relay devices
// list|revoke`, `agent-watch-relay hosts add|list|revoke`).
//
// A running relay holds an exclusive lock on AW_DATA_DIR/relay.lock for its
// whole life and serves a small admin API on the Unix socket
// AW_DATA_DIR/admin.sock (mode 0600, inside the 0700 data dir). The socket is
// never exposed over TCP. The CLI tries the lock first:
//   - lock acquired → no relay is running: edit store.json directly, under the lock;
//   - lock busy     → a relay is running: ask it over the admin socket, so the
//     change happens in its memory (and is flushed by it) instead of being
//     overwritten by its next save.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	lockFileName    = "relay.lock"
	adminSocketName = "admin.sock"
	adminBaseURL    = "http://relay-admin" // host is ignored: the transport always dials the socket

	// adminWaitTimeout bounds how long the CLI waits for the admin socket of a
	// relay that holds the lock but is still starting up or shutting down.
	adminWaitTimeout = 5 * time.Second
)

var (
	// ErrRelayRunning means another process, a running relay, holds the data-dir lock.
	ErrRelayRunning = errors.New("a relay is already running on this data dir")
	// ErrDeviceNotFound means no device has the given id.
	ErrDeviceNotFound = errors.New("device not found")
	// ErrHostNotFound means no registered host has the given id.
	ErrHostNotFound = errors.New("host not found")
	// ErrHostIsEnv means the id is the host of AW_HOST_TOKEN, which lives in
	// the relay's environment, not in store.json.
	ErrHostIsEnv = errors.New("that host id is AW_HOST_ID, the host of AW_HOST_TOKEN: it is managed in the relay's environment")

	errLocked    = errors.New("lock held by another process")
	errNoDataDir = errors.New("data dir does not exist")
)

// DeviceInfo is the admin view of a device. It never carries the token hash
// or the push token.
type DeviceInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	LastSeen  string `json:"last_seen"`
}

func deviceInfos(devs []Device) []DeviceInfo {
	out := make([]DeviceInfo, 0, len(devs))
	for _, d := range devs {
		out = append(out, DeviceInfo{ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, LastSeen: d.LastSeen})
	}
	return out
}

// HostAdminInfo is the admin view of a host. It never carries the token or
// its hash.
type HostAdminInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
	Online    bool   `json:"online"`        // its bridge is connected (false when no relay runs)
	Env       bool   `json:"env,omitempty"` // the host of AW_HOST_TOKEN, not in store.json
}

// addHostRequest is the body of POST /hosts. The CLI generates the token and
// sends only its hash, so the token never leaves the CLI.
type addHostRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"token_hash"`
}

// dataDirLock is the exclusive lock on AW_DATA_DIR/relay.lock.
type dataDirLock struct {
	f *os.File
}

// lockDataDir takes the data-dir lock without blocking. It returns
// ErrRelayRunning when another process holds it.
func lockDataDir(dir string) (*dataDirLock, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLocked) {
			return nil, ErrRelayRunning
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := matchDirOwner(f, dir); err != nil {
		_ = f.Close()
		return nil, err
	}
	// The pid is for operators only; the flock is what counts.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &dataDirLock{f: f}, nil
}

// release drops the lock (closing the descriptor releases the flock).
func (l *dataDirLock) release() {
	_ = l.f.Close()
}

func adminSocketPath(dataDir string) string {
	return filepath.Join(dataDir, adminSocketName)
}

// listenAdmin creates the admin Unix socket. The caller must hold the data-dir
// lock: that proves no live relay owns the path, so a file left by a crash can
// be removed safely.
func listenAdmin(dataDir string) (net.Listener, error) {
	path := adminSocketPath(dataDir)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale admin socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on admin socket %s: %w", path, err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod admin socket %s: %w", path, err)
	}
	return ln, nil
}

type adminErrorBody struct {
	Error string `json:"error"`
}

// ServiceEnvFile is the relay service's environment file (systemd's
// EnvironmentFile, /etc/agent-watch-relay/env). Tests override it.
var ServiceEnvFile = "/etc/agent-watch-relay/env"

// ServiceEnv returns an environment variable as the relay service sees it:
// the process's own value when set, else the service's env file (/etc/agent-watch-relay/env),
// which a sudo command does not load. An unreadable or missing file counts as empty.
func ServiceEnv(key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	data, err := os.ReadFile(ServiceEnvFile)
	if err != nil {
		return ""
	}
	value := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		value = v // systemd: the last assignment wins
	}
	return value
}

// EnvHostConfig describes the bootstrap host configured in the environment.
type EnvHostConfig struct {
	ID    string
	Name  string
	Token string
}

// LoadEnvHost reads the bootstrap host configuration from getenv.
// Returns nil if AW_HOST_TOKEN is not set.
func LoadEnvHost(getenv func(string) string) (*EnvHostConfig, error) {
	if getenv == nil {
		getenv = ServiceEnv
	}
	token := strings.TrimSpace(getenv("AW_HOST_TOKEN"))
	if token == "" {
		return nil, nil
	}
	id := strings.TrimSpace(getenv("AW_HOST_ID"))
	if id == "" {
		id = DefaultHostID
	}
	if !ValidHostID(id) {
		return nil, fmt.Errorf("AW_HOST_ID %q: %w", id, ErrInvalidHostID)
	}
	name, err := NormalizeHostName(id, getenv("AW_HOST_NAME"))
	if err != nil {
		return nil, fmt.Errorf("AW_HOST_NAME: %w", err)
	}
	return &EnvHostConfig{
		ID:    id,
		Name:  name,
		Token: token,
	}, nil
}

// Admin manages devices and hosts against a Store, optionally bound to an
// envHost (from AW_HOST_TOKEN) and live callbacks when attached to a running Server.
type Admin struct {
	store          *Store
	envHost        *HostAdminInfo // nil if AW_HOST_TOKEN unset or shadowed by stored host
	isOnline       func(hostID string) bool
	onAdd          func(h Host)
	onRevoke       func(hostID string)
	onRevokeDevice func(tokenHash string)
}

// NewAdmin creates an Admin over store and an optional envCfg.
// If a stored host already holds the same token hash as AW_HOST_TOKEN,
// AW_HOST_TOKEN is shadowed and ignored (matching NewServer).
func NewAdmin(store *Store, envCfg *EnvHostConfig) *Admin {
	var envHost *HostAdminInfo
	if envCfg != nil && envCfg.Token != "" {
		shadowed := false
		if store != nil {
			if _, ok := store.FindHostByTokenHash(Sha256Hex(envCfg.Token)); ok {
				shadowed = true
			}
		}
		if !shadowed {
			envHost = &HostAdminInfo{
				ID:   envCfg.ID,
				Name: envCfg.Name,
				Env:  true,
			}
		}
	}
	return &Admin{
		store:   store,
		envHost: envHost,
	}
}

func newServerAdmin(store *Store, state *State, auth *AuthManager, hub *Hub) *Admin {
	adm := &Admin{
		store: store,
		isOnline: func(hostID string) bool {
			if state == nil {
				return false
			}
			return state.HostOnline(hostID)
		},
		onAdd: func(h Host) {
			if state != nil {
				state.AddHost(h.ID, h.Name)
			}
			slog.Info("host registered", "host", h.ID)
		},
		onRevoke: func(hostID string) {
			var connected bool
			if hub != nil {
				connected = hub.RevokeHost(hostID)
			}
			if state != nil {
				state.RemoveHost(hostID)
			}
			slog.Info("host revoked", "host", hostID, "was_connected", connected)
		},
		onRevokeDevice: func(tokenHash string) {
			var closed int
			if auth != nil {
				closed = auth.CloseDeviceSessions(tokenHash)
			}
			slog.Info("device revoked", "closed_requests", closed)
		},
	}
	if auth != nil {
		if legacy, ok := auth.LegacyHost(); ok {
			adm.envHost = &HostAdminInfo{
				ID:   legacy.ID,
				Name: legacy.Name,
				Env:  true,
			}
		}
	}
	return adm
}

func (a *Admin) ListDevices(ctx context.Context) ([]DeviceInfo, error) {
	if a.store == nil {
		return []DeviceInfo{}, nil
	}
	return deviceInfos(a.store.ListDevices()), nil
}

func (a *Admin) RevokeDevice(ctx context.Context, id string) error {
	if a.store == nil {
		return ErrDeviceNotFound
	}
	dev, ok := a.store.RevokeDevice(id)
	if !ok {
		return ErrDeviceNotFound
	}
	if a.onRevokeDevice != nil {
		a.onRevokeDevice(dev.TokenHash)
	}
	return a.store.Flush()
}

func (a *Admin) ListHosts(ctx context.Context) ([]HostAdminInfo, error) {
	var out []HostAdminInfo
	if a.envHost != nil {
		online := false
		if a.isOnline != nil {
			online = a.isOnline(a.envHost.ID)
		}
		out = append(out, HostAdminInfo{
			ID:     a.envHost.ID,
			Name:   a.envHost.Name,
			Online: online,
			Env:    true,
		})
	}
	if a.store != nil {
		for _, h := range a.store.ListHosts() {
			online := false
			if a.isOnline != nil {
				online = a.isOnline(h.ID)
			}
			out = append(out, HostAdminInfo{
				ID:        h.ID,
				Name:      h.Name,
				CreatedAt: h.CreatedAt,
				LastSeen:  h.LastSeen,
				Online:    online,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if out == nil {
		out = []HostAdminInfo{}
	}
	return out, nil
}

func (a *Admin) AddHost(ctx context.Context, id, name, tokenHash string) (HostAdminInfo, error) {
	if !ValidHostID(id) {
		return HostAdminInfo{}, ErrInvalidHostID
	}
	var err error
	if name, err = NormalizeHostName(id, name); err != nil {
		return HostAdminInfo{}, err
	}
	if tokenHash == "" {
		return HostAdminInfo{}, errEmptyTokenHash
	}
	if a.envHost != nil && a.envHost.ID == id {
		return HostAdminInfo{}, ErrHostIsEnv
	}
	if a.store == nil {
		return HostAdminInfo{}, errors.New("no store configured")
	}
	h, err := a.store.AddHost(id, name, tokenHash)
	if err != nil {
		return HostAdminInfo{}, err
	}
	if a.onAdd != nil {
		a.onAdd(h)
	}
	if err := a.store.Flush(); err != nil {
		return HostAdminInfo{}, fmt.Errorf("saving store: %w", err)
	}
	return HostAdminInfo{
		ID:        h.ID,
		Name:      h.Name,
		CreatedAt: h.CreatedAt,
		LastSeen:  h.LastSeen,
	}, nil
}

func (a *Admin) RevokeHost(ctx context.Context, id string) error {
	if a.envHost != nil && a.envHost.ID == id {
		return ErrHostIsEnv
	}
	if a.store == nil {
		return ErrHostNotFound
	}
	_, ok := a.store.RevokeHost(id)
	if !ok {
		return ErrHostNotFound
	}
	if a.onRevoke != nil {
		a.onRevoke(id)
	}
	if err := a.store.Flush(); err != nil {
		return fmt.Errorf("saving store: %w", err)
	}
	return nil
}

// AdminClient is the interface for administering devices and hosts, implemented
// both by offline *Admin and by the online admin socket client.
type AdminClient interface {
	ListDevices(ctx context.Context) ([]DeviceInfo, error)
	RevokeDevice(ctx context.Context, id string) error
	ListHosts(ctx context.Context) ([]HostAdminInfo, error)
	AddHost(ctx context.Context, id, name, tokenHash string) (HostAdminInfo, error)
	RevokeHost(ctx context.Context, id string) error
}

type socketAdminClient struct {
	client *http.Client
}

func (c *socketAdminClient) ListDevices(ctx context.Context) ([]DeviceInfo, error) {
	var out []DeviceInfo
	err := adminCall(ctx, c.client, http.MethodGet, "/devices", nil, &out, ErrDeviceNotFound)
	return out, err
}

func (c *socketAdminClient) RevokeDevice(ctx context.Context, id string) error {
	return adminCall(ctx, c.client, http.MethodDelete, "/devices/"+url.PathEscape(id), nil, nil, ErrDeviceNotFound)
}

func (c *socketAdminClient) ListHosts(ctx context.Context) ([]HostAdminInfo, error) {
	var out []HostAdminInfo
	err := adminCall(ctx, c.client, http.MethodGet, "/hosts", nil, &out, ErrHostNotFound)
	return out, err
}

func (c *socketAdminClient) AddHost(ctx context.Context, id, name, tokenHash string) (HostAdminInfo, error) {
	req := addHostRequest{ID: id, Name: name, TokenHash: tokenHash}
	var info HostAdminInfo
	err := adminCall(ctx, c.client, http.MethodPost, "/hosts", req, &info, ErrHostNotFound)
	return info, err
}

func (c *socketAdminClient) RevokeHost(ctx context.Context, id string) error {
	return adminCall(ctx, c.client, http.MethodDelete, "/hosts/"+url.PathEscape(id), nil, nil, ErrHostNotFound)
}

// adminHandler serves the admin API. It is only ever mounted on the Unix
// socket, never on the public listener.
func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		devs, _ := s.admin.ListDevices(r.Context())
		writeJSON(w, http.StatusOK, devs)
	})
	mux.HandleFunc("DELETE /devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := s.admin.RevokeDevice(r.Context(), r.PathValue("id"))
		switch {
		case errors.Is(err, ErrDeviceNotFound):
			writeJSON(w, http.StatusNotFound, adminErrorBody{Error: err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, adminErrorBody{Error: err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("GET /hosts", func(w http.ResponseWriter, r *http.Request) {
		hosts, _ := s.admin.ListHosts(r.Context())
		writeJSON(w, http.StatusOK, hosts)
	})
	mux.HandleFunc("POST /hosts", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var req addHostRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, adminErrorBody{Error: "malformed request body"})
			return
		}
		h, err := s.admin.AddHost(r.Context(), req.ID, req.Name, req.TokenHash)
		switch {
		case errors.Is(err, ErrHostExists), errors.Is(err, ErrHostIsEnv):
			writeJSON(w, http.StatusConflict, adminErrorBody{Error: err.Error()})
		case errors.Is(err, ErrInvalidHostID), errors.Is(err, ErrInvalidHostName), errors.Is(err, errEmptyTokenHash):
			writeJSON(w, http.StatusBadRequest, adminErrorBody{Error: err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, adminErrorBody{Error: err.Error()})
		default:
			writeJSON(w, http.StatusCreated, h)
		}
	})
	mux.HandleFunc("DELETE /hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := s.admin.RevokeHost(r.Context(), r.PathValue("id"))
		switch {
		case errors.Is(err, ErrHostIsEnv):
			writeJSON(w, http.StatusConflict, adminErrorBody{Error: err.Error()})
		case errors.Is(err, ErrHostNotFound):
			writeJSON(w, http.StatusNotFound, adminErrorBody{Error: err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, adminErrorBody{Error: err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	return mux
}

// hostInfos lists AW_HOST_TOKEN's host (if set) and the registered hosts,
// with whether each is connected.
func (s *Server) hostInfos() []HostAdminInfo {
	hosts, _ := s.admin.ListHosts(context.Background())
	return hosts
}

// AddHost registers a host in the running relay: its token (by hash) works
// from now on, watches see it in the host list (offline until its bridge
// connects), and the store is flushed at once.
func (s *Server) AddHost(id, name, tokenHash string) (HostAdminInfo, error) {
	return s.admin.AddHost(context.Background(), id, name, tokenHash)
}

// RevokeHost removes a registered host from the running relay: its token is
// rejected from the next handshake on, its connection is closed (in-flight
// commands fail with host_offline), its agents leave the watch's list, and the
// store is flushed at once. Its history stays until it ages out. found is
// false when no registered host has that id.
func (s *Server) RevokeHost(id string) (found bool, err error) {
	err = s.admin.RevokeHost(context.Background(), id)
	if errors.Is(err, ErrHostNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RevokeDevice removes a device from the running relay. Its token is rejected
// from the next request on, its in-flight requests and SSE streams are
// cancelled, and the store is flushed at once so the removal survives a crash.
// found is false when no device has that id.
func (s *Server) RevokeDevice(deviceID string) (found bool, err error) {
	err = s.admin.RevokeDevice(context.Background(), deviceID)
	if errors.Is(err, ErrDeviceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AdminListDevices lists the registered devices of the relay whose data dir is
// dataDir, asking the running relay when there is one.
func AdminListDevices(ctx context.Context, dataDir string) ([]DeviceInfo, error) {
	var out []DeviceInfo
	_, err := WithAdminTarget(ctx, dataDir, func(adm AdminClient, _ bool) error {
		var err error
		out, err = adm.ListDevices(ctx)
		return err
	})
	if errors.Is(err, errNoDataDir) {
		return nil, nil
	}
	return out, err
}

// AdminRevokeDevice revokes a device. When a relay is running on dataDir the
// running relay performs it (viaRelay is true); otherwise store.json is edited
// under the data-dir lock. It returns ErrDeviceNotFound for an unknown id.
func AdminRevokeDevice(ctx context.Context, dataDir, deviceID string) (viaRelay bool, err error) {
	viaRelay, err = WithAdminTarget(ctx, dataDir, func(adm AdminClient, _ bool) error {
		return adm.RevokeDevice(ctx, deviceID)
	})
	if errors.Is(err, errNoDataDir) {
		return false, ErrDeviceNotFound
	}
	return viaRelay, err
}

// AdminListHosts lists the hosts of the relay whose data dir is dataDir. Both
// online and offline, it includes AW_HOST_TOKEN's host (when set) with Env: true,
// and reports online flags when the relay is running.
func AdminListHosts(ctx context.Context, dataDir string) ([]HostAdminInfo, error) {
	var out []HostAdminInfo
	_, err := WithAdminTarget(ctx, dataDir, func(adm AdminClient, _ bool) error {
		var err error
		out, err = adm.ListHosts(ctx)
		return err
	})
	if errors.Is(err, errNoDataDir) {
		return nil, nil
	}
	return out, err
}

// AdminAddHost registers a host and returns its new token, which exists only
// in this result: the relay stores its hash. When a relay is running on
// dataDir the running relay registers it (viaRelay is true); otherwise
// store.json is edited under the data-dir lock (the data dir is created if
// needed). It returns ErrHostExists for an id already registered, and
// ErrHostIsEnv for the host of AW_HOST_TOKEN (configured in the environment).
func AdminAddHost(ctx context.Context, dataDir, id, name string) (info HostAdminInfo, token string, viaRelay bool, err error) {
	if !ValidHostID(id) {
		return HostAdminInfo{}, "", false, ErrInvalidHostID
	}
	if name, err = NormalizeHostName(id, name); err != nil {
		return HostAdminInfo{}, "", false, err
	}
	token, err = GenerateDeviceToken()
	if err != nil {
		return HostAdminInfo{}, "", false, fmt.Errorf("generate host token: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return HostAdminInfo{}, "", false, fmt.Errorf("create data dir %s: %w", dataDir, err)
	}
	tokenHash := Sha256Hex(token)
	viaRelay, err = WithAdminTarget(ctx, dataDir, func(adm AdminClient, _ bool) error {
		info, err = adm.AddHost(ctx, id, name, tokenHash)
		return err
	})
	if err != nil {
		return HostAdminInfo{}, "", viaRelay, err
	}
	return info, token, viaRelay, nil
}

// AdminRevokeHost revokes a registered host. When a relay is running on
// dataDir the running relay performs it and closes the host's connection
// (viaRelay is true); otherwise store.json is edited under the data-dir lock.
// It returns ErrHostNotFound for an unknown id, and ErrHostIsEnv for AW_HOST_TOKEN's host.
func AdminRevokeHost(ctx context.Context, dataDir, id string) (viaRelay bool, err error) {
	viaRelay, err = WithAdminTarget(ctx, dataDir, func(adm AdminClient, _ bool) error {
		return adm.RevokeHost(ctx, id)
	})
	if errors.Is(err, errNoDataDir) {
		return false, ErrHostNotFound
	}
	return viaRelay, err
}

// WithAdminTarget runs fn against the running relay's admin socket (viaRelay: true),
// or offline against the on-disk store and env host (viaRelay: false).
func WithAdminTarget(ctx context.Context, dataDir string, fn func(adm AdminClient, viaRelay bool) error) (viaRelay bool, err error) {
	if _, err := os.Stat(dataDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, errNoDataDir
		}
		return false, err
	}

	client := adminClient(dataDir)
	defer client.CloseIdleConnections()

	deadline := time.Now().Add(adminWaitTimeout)
	for {
		lock, err := lockDataDir(dataDir)
		if err == nil {
			return false, runOfflineAdmin(dataDir, lock, func(adm AdminClient) error {
				return fn(adm, false)
			})
		}
		if !errors.Is(err, ErrRelayRunning) {
			return false, err
		}

		err = fn(&socketAdminClient{client: client}, true)
		if !isDialError(err) {
			return true, err
		}
		// The relay holds the lock but its socket is not up yet (starting) or
		// already gone (stopping). Retry: either it answers or the lock frees.
		if time.Now().After(deadline) {
			return true, fmt.Errorf("a relay holds the lock on %s but its admin socket does not answer: %w", dataDir, err)
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func runOfflineAdmin(dataDir string, lock *dataDirLock, fn func(AdminClient) error) error {
	defer lock.release()
	legacyHostID := ServiceEnv("AW_HOST_ID")
	if legacyHostID == "" {
		legacyHostID = DefaultHostID
	}
	store, err := OpenStore(dataDir, legacyHostID)
	if err != nil {
		return fmt.Errorf("read store from %s: %w", dataDir, err)
	}
	defer store.Close()

	envCfg, err := LoadEnvHost(ServiceEnv)
	if err != nil {
		return fmt.Errorf("the relay's environment: %w", err)
	}

	admin := NewAdmin(store, envCfg)
	return fn(admin)
}

func adminClient(dataDir string) *http.Client {
	sock := adminSocketPath(dataDir)
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

// adminCall sends body (when non-nil) as JSON and decodes the response into
// out (when non-nil). A 404 is notFound; a 409 for an existing host is
// ErrHostExists.
func adminCall(ctx context.Context, c *http.Client, method, path string, body, out any, notFound error) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal admin request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, adminBaseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read admin response: %w", err)
	}

	var eb adminErrorBody
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return notFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		if json.Unmarshal(respBody, &eb) == nil && eb.Error != "" {
			if resp.StatusCode == http.StatusConflict {
				for _, known := range []error{ErrHostExists, ErrHostIsEnv} {
					if eb.Error == known.Error() {
						return known
					}
				}
			}
			return fmt.Errorf("relay: %s", eb.Error)
		}
		return fmt.Errorf("relay admin socket: status %d", resp.StatusCode)
	case out != nil:
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode admin response: %w", err)
		}
	}
	return nil
}

// isDialError reports whether err is a failure to connect to the admin socket
// (nothing was sent, so retrying cannot apply an operation twice).
func isDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}
