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
	"strconv"
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

// adminHandler serves the admin API. It is only ever mounted on the Unix
// socket, never on the public listener.
func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, deviceInfos(s.store.ListDevices()))
	})
	mux.HandleFunc("DELETE /devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		found, err := s.RevokeDevice(r.PathValue("id"))
		switch {
		case !found:
			writeJSON(w, http.StatusNotFound, adminErrorBody{Error: ErrDeviceNotFound.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, adminErrorBody{Error: err.Error()})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("GET /hosts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.hostInfos())
	})
	mux.HandleFunc("POST /hosts", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var req addHostRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, adminErrorBody{Error: "malformed request body"})
			return
		}
		h, err := s.AddHost(req.ID, req.Name, req.TokenHash)
		switch {
		case errors.Is(err, ErrHostExists), errors.Is(err, ErrHostIsEnv):
			writeJSON(w, http.StatusConflict, adminErrorBody{Error: err.Error()})
		case errors.Is(err, ErrInvalidHostID), errors.Is(err, ErrInvalidHostName), errors.Is(err, errEmptyTokenHash):
			writeJSON(w, http.StatusBadRequest, adminErrorBody{Error: err.Error()})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, adminErrorBody{Error: err.Error()})
		default:
			writeJSON(w, http.StatusCreated, HostAdminInfo{ID: h.ID, Name: h.Name, CreatedAt: h.CreatedAt, LastSeen: h.LastSeen})
		}
	})
	mux.HandleFunc("DELETE /hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		found, err := s.RevokeHost(r.PathValue("id"))
		switch {
		case errors.Is(err, ErrHostIsEnv):
			writeJSON(w, http.StatusConflict, adminErrorBody{Error: err.Error()})
		case !found:
			writeJSON(w, http.StatusNotFound, adminErrorBody{Error: ErrHostNotFound.Error()})
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
	var out []HostAdminInfo
	if legacy, ok := s.auth.LegacyHost(); ok {
		out = append(out, HostAdminInfo{ID: legacy.ID, Name: legacy.Name, Online: s.state.HostOnline(legacy.ID), Env: true})
	}
	for _, h := range s.store.ListHosts() {
		out = append(out, HostAdminInfo{ID: h.ID, Name: h.Name, CreatedAt: h.CreatedAt, LastSeen: h.LastSeen, Online: s.state.HostOnline(h.ID)})
	}
	if out == nil {
		out = []HostAdminInfo{}
	}
	return out
}

// AddHost registers a host in the running relay: its token (by hash) works
// from now on, watches see it in the host list (offline until its bridge
// connects), and the store is flushed at once.
func (s *Server) AddHost(id, name, tokenHash string) (Host, error) {
	if legacy, ok := s.auth.LegacyHost(); ok && legacy.ID == id {
		return Host{}, ErrHostIsEnv
	}
	h, err := s.store.AddHost(id, name, tokenHash)
	if err != nil {
		return Host{}, err
	}
	s.state.AddHost(h.ID, h.Name)
	slog.Info("host registered", "host", h.ID)
	if err := s.store.Flush(); err != nil {
		return h, fmt.Errorf("host registered in the running relay, but saving the store failed: %w", err)
	}
	return h, nil
}

// RevokeHost removes a registered host from the running relay: its token is
// rejected from the next handshake on, its connection is closed (in-flight
// commands fail with host_offline), its agents leave the watch's list, and the
// store is flushed at once. Its history stays until it ages out. found is
// false when no registered host has that id.
func (s *Server) RevokeHost(id string) (found bool, err error) {
	if legacy, ok := s.auth.LegacyHost(); ok && legacy.ID == id {
		return false, ErrHostIsEnv
	}
	if _, ok := s.store.RevokeHost(id); !ok {
		return false, nil
	}
	// The token is gone from the store first: a handshake that races with
	// this drop re-checks its token once registered (Hub.ServeHost).
	connected := s.hub.RevokeHost(id)
	s.state.RemoveHost(id)
	slog.Info("host revoked", "host", id, "was_connected", connected)
	if err := s.store.Flush(); err != nil {
		return true, fmt.Errorf("host revoked in the running relay, but saving the store failed: %w", err)
	}
	return true, nil
}

// RevokeDevice removes a device from the running relay. Its token is rejected
// from the next request on, its in-flight requests and SSE streams are
// cancelled, and the store is flushed at once so the removal survives a crash.
// found is false when no device has that id.
func (s *Server) RevokeDevice(deviceID string) (found bool, err error) {
	dev, ok := s.store.RevokeDevice(deviceID)
	if !ok {
		return false, nil
	}
	closed := s.auth.CloseDeviceSessions(dev.TokenHash)
	slog.Info("device revoked", "device_id", deviceID, "closed_requests", closed)
	if err := s.store.Flush(); err != nil {
		return true, fmt.Errorf("device revoked in the running relay, but saving the store failed: %w", err)
	}
	return true, nil
}

// AdminListDevices lists the registered devices of the relay whose data dir is
// dataDir, asking the running relay when there is one.
func AdminListDevices(ctx context.Context, dataDir string) ([]DeviceInfo, error) {
	var out []DeviceInfo
	_, err := withRelayOrStore(ctx, dataDir,
		func(st *Store) error {
			out = deviceInfos(st.ListDevices())
			return nil
		},
		func(c *http.Client) error {
			out = nil
			return adminCall(ctx, c, http.MethodGet, "/devices", nil, &out, ErrDeviceNotFound)
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
	viaRelay, err = withRelayOrStore(ctx, dataDir,
		func(st *Store) error {
			if _, ok := st.RevokeDevice(deviceID); !ok {
				return ErrDeviceNotFound
			}
			return nil
		},
		func(c *http.Client) error {
			return adminCall(ctx, c, http.MethodDelete, "/devices/"+url.PathEscape(deviceID), nil, nil, ErrDeviceNotFound)
		})
	if errors.Is(err, errNoDataDir) {
		return false, ErrDeviceNotFound
	}
	return viaRelay, err
}

// AdminListHosts lists the hosts of the relay whose data dir is dataDir. A
// running relay also reports AW_HOST_TOKEN's host and which hosts are online;
// without one, only the hosts in store.json are listed.
func AdminListHosts(ctx context.Context, dataDir string) ([]HostAdminInfo, error) {
	var out []HostAdminInfo
	_, err := withRelayOrStore(ctx, dataDir,
		func(st *Store) error {
			for _, h := range st.ListHosts() {
				out = append(out, HostAdminInfo{ID: h.ID, Name: h.Name, CreatedAt: h.CreatedAt, LastSeen: h.LastSeen})
			}
			return nil
		},
		func(c *http.Client) error {
			out = nil
			return adminCall(ctx, c, http.MethodGet, "/hosts", nil, &out, ErrHostNotFound)
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
// needed). It returns ErrHostExists for an id already registered.
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
	req := addHostRequest{ID: id, Name: name, TokenHash: Sha256Hex(token)}
	viaRelay, err = withRelayOrStore(ctx, dataDir,
		func(st *Store) error {
			h, err := st.AddHost(req.ID, req.Name, req.TokenHash)
			info = HostAdminInfo{ID: h.ID, Name: h.Name, CreatedAt: h.CreatedAt}
			return err
		},
		func(c *http.Client) error {
			return adminCall(ctx, c, http.MethodPost, "/hosts", req, &info, ErrHostNotFound)
		})
	if err != nil {
		return HostAdminInfo{}, "", viaRelay, err
	}
	return info, token, viaRelay, nil
}

// AdminRevokeHost revokes a registered host. When a relay is running on
// dataDir the running relay performs it and closes the host's connection
// (viaRelay is true); otherwise store.json is edited under the data-dir lock.
// It returns ErrHostNotFound for an unknown id.
func AdminRevokeHost(ctx context.Context, dataDir, id string) (viaRelay bool, err error) {
	viaRelay, err = withRelayOrStore(ctx, dataDir,
		func(st *Store) error {
			if _, ok := st.RevokeHost(id); !ok {
				return ErrHostNotFound
			}
			return nil
		},
		func(c *http.Client) error {
			return adminCall(ctx, c, http.MethodDelete, "/hosts/"+url.PathEscape(id), nil, nil, ErrHostNotFound)
		})
	if errors.Is(err, errNoDataDir) {
		return false, ErrHostNotFound
	}
	return viaRelay, err
}

// withRelayOrStore runs online against the running relay, or offline against
// the on-disk store when no relay holds the data-dir lock.
func withRelayOrStore(ctx context.Context, dataDir string, offline func(*Store) error, online func(*http.Client) error) (viaRelay bool, err error) {
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
			return false, runOffline(dataDir, lock, offline)
		}
		if !errors.Is(err, ErrRelayRunning) {
			return false, err
		}

		err = online(client)
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

func runOffline(dataDir string, lock *dataDirLock, fn func(*Store) error) error {
	defer lock.release()
	// A version 1 store is migrated on open: its history goes to the host of
	// AW_HOST_TOKEN, so the CLI needs the service's AW_HOST_ID (default main).
	legacyHostID := os.Getenv("AW_HOST_ID")
	if legacyHostID == "" {
		legacyHostID = DefaultHostID
	}
	store, err := OpenStore(dataDir, legacyHostID)
	if err != nil {
		return fmt.Errorf("read store from %s: %w", dataDir, err)
	}
	opErr := fn(store)
	if err := store.Close(); err != nil && opErr == nil {
		opErr = fmt.Errorf("save store: %w", err)
	}
	return opErr
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
			if resp.StatusCode == http.StatusConflict && eb.Error == ErrHostExists.Error() {
				return ErrHostExists
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
