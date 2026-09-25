package relay

// Local administration of devices (`agent-watch-relay devices list|revoke`).
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
	return mux
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
			return adminCall(ctx, c, http.MethodGet, "/devices", &out)
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
			return adminCall(ctx, c, http.MethodDelete, "/devices/"+url.PathEscape(deviceID), nil)
		})
	if errors.Is(err, errNoDataDir) {
		return false, ErrDeviceNotFound
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
	store, err := NewStore(dataDir)
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

func adminCall(ctx context.Context, c *http.Client, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, adminBaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read admin response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrDeviceNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		var eb adminErrorBody
		if json.Unmarshal(body, &eb) == nil && eb.Error != "" {
			return fmt.Errorf("relay: %s", eb.Error)
		}
		return fmt.Errorf("relay admin socket: status %d", resp.StatusCode)
	case out != nil:
		if err := json.Unmarshal(body, out); err != nil {
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
